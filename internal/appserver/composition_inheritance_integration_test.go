package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestIdleParentCompositionInheritanceWithNativeWorker(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
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
	defer close(release)
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
	var oldScope, nextScope pluginhost.CompositionView
	if err := json.Unmarshal(invoke("create", pluginhost.CompositionCreate{Inherit: true, State: "parent", Model: &pluginhost.CompositionModel{Provider: "second", Model: "model-two"}}), &oldScope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(invoke("create", pluginhost.CompositionCreate{Inherit: true, State: "parent"}), &nextScope); err != nil {
		t.Fatal(err)
	}
	const parentID = "idle-parent-with-worker"
	if _, err := rt.Sessions.CreateWithMetadata(parentID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	expected := ""
	invoke("bind", pluginhost.CompositionBind{Session: parentID, ID: oldScope.ID, Expected: &expected})
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	parent, err := srv.ensureThreadLoaded(binding, parentID)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := srv.ensureThreadRuntime(parent)
	if err != nil {
		t.Fatal(err)
	}
	execution.AgentControl.StartWorkerTerminalRecovery()
	if _, err := execution.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "held", Prompt: "Wait for release."}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	expected = oldScope.ID
	invoke("bind", pluginhost.CompositionBind{Session: parentID, ID: nextScope.ID, Expected: &expected})
	srv.retireIdlePluginRuntimes()
	created, err := srv.createPluginSession(runtime.WithInvocationState(ctx, binding), consumer.ID(), pluginhost.SessionCreateParams{RequestID: "root-child", Visibility: "user", ParentSessionID: parentID, ContextSource: "fresh", Workspace: "shared", Provider: "first", Model: "model-one"})
	if err != nil {
		t.Fatal(err)
	}
	var inherited pluginhost.CompositionSessionView
	if err := json.Unmarshal(invoke("inspectSession", map[string]string{"session": created.SessionID}), &inherited); err != nil {
		t.Fatal(err)
	}
	if inherited.ID != nextScope.ID {
		t.Fatalf("new child inherited stale execution: got %q want current durable %q", inherited.ID, nextScope.ID)
	}
	child, err := rt.NewThreadRuntimeForState(binding, created.SessionID, rt.RootDir, runtime.ThreadModelSelection{})
	if err != nil {
		t.Fatal(err)
	}
	defer child.CloseComposition()
	if child.StreamRunner.Model != "model-one" {
		t.Fatalf("new child model = %q", child.StreamRunner.Model)
	}
	if execution.StreamRunner.Model != "model-two" {
		t.Fatalf("old worker execution model changed to %q", execution.StreamRunner.Model)
	}
}
