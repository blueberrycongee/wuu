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
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{consumer.ID(): true}}}}
	catalog := []pluginpkg.Plugin{}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true,
		NativeProviders: []runtime.NativeProviderFactory{{ID: consumer.ID(), Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
			return &compositionWorkerConsumer{}, nil
		}}}})
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
	// A persisted binding must also fence a later engine change at admission.
	if _, err := rt.Sessions.SetEngine(sessionID, "codex"); err != nil {
		t.Fatal(err)
	}
	engine := &compositionAdmissionEngine{stubProtocolEngine: stubProtocolEngine{id: agentengine.EngineID("codex")}}
	engines := agentengine.NewRegistry()
	if err := engines.Register(engine); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(engines)
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"resume-external","method":"thread/resume","params":{"session_id":%q}}`, sessionID))); err != nil {
		t.Fatal(err)
	}
	if response := responseByID(t, parseOutput(t, out.String()), "resume-external"); response["error"] != nil {
		t.Fatalf("resume: %+v", response)
	}
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"external-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"must not dispatch"}}`, sessionID))); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "external-turn")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "native Wuu engine") {
		t.Fatalf("external composition admission was not rejected: %+v", response)
	}
	if engine.calls.Load() != 0 {
		t.Fatalf("rejected composition dispatched engine %d times", engine.calls.Load())
	}
	select {
	case model := <-requests:
		t.Fatalf("rejected composition dispatched model %q", model)
	default:
	}
	expected = scope.ID
	invoke("bind", pluginhost.CompositionBind{Session: sessionID, ID: "", Expected: &expected})

}

type compositionAdmissionEngine struct {
	stubProtocolEngine
	calls atomic.Int32
}

func (e *compositionAdmissionEngine) Open(ctx context.Context, input agentengine.OpenRequest) (agentengine.Session, error) {
	e.calls.Add(1)
	return e.stubProtocolEngine.Open(ctx, input)
}
func (e *compositionAdmissionEngine) Resume(ctx context.Context, input agentengine.ResumeRequest) (agentengine.Session, error) {
	e.calls.Add(1)
	return e.stubProtocolEngine.Resume(ctx, input)
}
