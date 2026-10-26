package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type compositionWorkerConsumer struct{}

func (*compositionWorkerConsumer) ID() string { return "test.composition-worker" }
func (c *compositionWorkerConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*compositionWorkerConsumer) Close(context.Context) error                      { return nil }
func (*compositionWorkerConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*compositionWorkerConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginhost.CompositionService, MajorVersion: 1, Required: true}}
}

// The native worker's alias resolver is installed by appserver after runtime
// construction. It must use the admitted graph's models, not the imported
// session authority's defaults. This exercises real composition and HTTP model
// transport; the endpoint supplies a deterministic answer without credentials.
func TestCompositionNativeWorkerKeepsAdmittedModelRoles(t *testing.T) {
	requests := make(chan string, 8)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- input.Model
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n", input.Model)
	}))
	defer endpoint.Close()
	cfg := config.Default()
	cfg.DefaultProvider = "first"
	cfg.Providers = map[string]config.ProviderConfig{
		"first":  {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic", Model: "model-one"},
		"second": {Type: "openai-compatible", BaseURL: endpoint.URL + "/v1", APIKey: "synthetic", Model: "model-two"},
	}
	consumer := &compositionWorkerConsumer{}
	engine := &compositionAdmissionEngine{}
	cfg.Composition = &composition.Config{Services: []composition.Binding{{Service: "engine.codex", Major: 1, Provider: engine.ID()}}, Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{consumer.ID(): true, engine.ID(): true}}}}
	catalog := []pluginpkg.Plugin{}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true,
		NativeProviders: []runtime.NativeProviderFactory{{ID: consumer.ID(), Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
			return &compositionWorkerConsumer{}, nil
		}}, {ID: engine.ID(), Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) { return engine, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	invoke := func(method string, input any) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		output, hostErr := rt.PluginHost.CallService(ctx, consumer.ID(), pluginhost.CompositionService, method, raw)
		if hostErr != nil {
			t.Fatalf("composition %s: %v", method, hostErr)
		}
		return output
	}
	var scope pluginhost.CompositionView
	if err := json.Unmarshal(invoke("create", pluginhost.CompositionCreate{Inherit: true, State: "parent", Model: &pluginhost.CompositionModel{Provider: "second", Model: "model-two"}}), &scope); err != nil {
		t.Fatal(err)
	}
	const sessionID = "composed-native-worker"
	if _, err := rt.Sessions.Create(sessionID); err != nil {
		t.Fatal(err)
	}
	expected := ""
	invoke("bind", pluginhost.CompositionBind{Session: sessionID, ID: scope.ID, Expected: &expected})
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	thread, err := rt.NewThreadRuntimeForState(binding, sessionID, rt.RootDir, runtime.ThreadModelSelection{})
	if err != nil {
		t.Fatal(err)
	}
	subscription := srv.subscribeThreadRuntime(binding, sessionID, thread)
	defer srv.releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: thread, subscription: subscription})
	thread.AgentControl.StartWorkerTerminalRecovery()
	result, err := thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "verify", Description: "verify captured model", Prompt: "Report the selected model.", ModelAlias: "@verification", Synchronous: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Result != "model-two" {
		t.Fatalf("worker escaped admitted composition: %+v", result)
	}
	select {
	case model := <-requests:
		if model != "model-two" {
			t.Fatalf("HTTP inference used %q", model)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// A persisted composition remains selected when a different engine is admitted.
	// Set an explicit title so this check isolates execution from title inference.
	if _, err := rt.Sessions.UpdateTitle(sessionID, "Composition fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Sessions.SetEngine(sessionID, "codex"); err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"resume-external","method":"thread/resume","params":{"session_id":%q}}`, sessionID))); err != nil {
		t.Fatal(err)
	}
	if response := responseByID(t, parseOutput(t, out.String()), "resume-external"); response["error"] != nil {
		t.Fatalf("resume: %+v", response)
	}
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"external-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"use the selected external engine"}}`, sessionID))); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "external-turn")
	if response["error"] != nil {
		t.Fatalf("external composition admission failed: %+v", response)
	}
	waitForTurnCompletedForThread(t, out, sessionID)
	if engine.calls.Load() != 1 || !strings.Contains(out.String(), "external composition verified") {
		t.Fatalf("external composition did not execute exactly once: calls=%d output=%s", engine.calls.Load(), out.String())
	}
	select {
	case model := <-requests:
		t.Fatalf("external engine unexpectedly dispatched host inference %q", model)
	default:
	}
	var retained pluginhost.CompositionSessionView
	if err := json.Unmarshal(invoke("inspectSession", map[string]string{"session": sessionID}), &retained); err != nil {
		t.Fatal(err)
	}
	if retained.ID != scope.ID || retained.State != "active" {
		t.Fatalf("engine change lost admitted composition: %+v", retained)
	}
	expected = scope.ID
	invoke("bind", pluginhost.CompositionBind{Session: sessionID, ID: "", Expected: &expected})

}

// This selected service has no process launcher or model transport.
type compositionAdmissionEngine struct {
	compositionWorkerConsumer
	calls atomic.Int32
}

func (*compositionAdmissionEngine) ID() string { return "fixture.composition-engine" }
func (e *compositionAdmissionEngine) Status() pluginhost.Status {
	return pluginhost.Status{ID: e.ID(), State: pluginhost.StateActive}
}
func (*compositionAdmissionEngine) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*compositionAdmissionEngine) ProvidedServices() []pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"descriptor", "retain", "release", "bind", "run", "close", "history_fork"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: "engine." + strings.ReplaceAll(name, "_", "-") + ".input.v1", OutputSchema: "engine." + strings.ReplaceAll(name, "_", "-") + ".output.v1", Cleanup: name == "close" || name == "release"})
	}
	return []pluginhost.ServiceDescriptor{{Name: "engine.codex", Version: "1.0.0", Methods: methods}}
}
func (e *compositionAdmissionEngine) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch p.Method {
	case "descriptor":
		return json.Marshal(map[string]any{"id": agentengine.EngineID("codex"), "version": "fixture", "enabled": true})
	case "retain", "bind":
		return json.Marshal(pluginprotocol.EngineSessionParams{SessionID: "fixture-external-session"})
	case "run":
		e.calls.Add(1)
		return json.Marshal(pluginprotocol.EngineRunResult{Result: pluginprotocol.EngineResultV1{Version: 1, Content: "external composition verified", NewMessages: []pluginprotocol.DriverMessageV1{{Role: "assistant", Content: "external composition verified"}}}})
	case "release", "close", "history_fork":
		return json.RawMessage(`{}`), nil
	default:
		return nil, fmt.Errorf("unsupported fixture engine operation %q", p.Method)
	}
}
