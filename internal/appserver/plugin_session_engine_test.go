package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// stubEngineSessionFactory is a thread-bound external engine used to prove
// managed and plugin session creation binds a selected engine end to end:
// creation persists the binding, sends route turns through ThreadBinding,
// and the engine owns its model identity.
type stubEngineSessionFactory struct {
	id     agentengine.EngineID
	caps   []string
	result string

	mu        sync.Mutex
	bindings  []agentengine.ThreadBinding
	histories [][]providers.ChatMessage
}

func (f *stubEngineSessionFactory) Descriptor(context.Context) (agentengine.Descriptor, error) {
	return agentengine.Descriptor{ID: f.id, Version: "1", Capabilities: f.caps}, nil
}
func (f *stubEngineSessionFactory) Open(context.Context, agentengine.OpenRequest) (agentengine.Session, error) {
	return nil, errors.New("unused")
}
func (f *stubEngineSessionFactory) Resume(context.Context, agentengine.ResumeRequest) (agentengine.Session, error) {
	return nil, errors.New("unused")
}
func (f *stubEngineSessionFactory) SessionForThread(_ context.Context, binding agentengine.ThreadBinding) (agentengine.Session, error) {
	f.mu.Lock()
	f.bindings = append(f.bindings, binding)
	f.mu.Unlock()
	return &stubEngineSession{factory: f}, nil
}

type stubEngineSession struct{ factory *stubEngineSessionFactory }

func (s *stubEngineSession) RunTurn(_ context.Context, input agentengine.TurnInput, _ agentengine.EventSink) (agentengine.TurnResult, error) {
	s.factory.mu.Lock()
	s.factory.histories = append(s.factory.histories, input.History)
	result := s.factory.result
	s.factory.mu.Unlock()
	return agentengine.TurnResult{Result: agent.LoopResult{
		Content:     result,
		NewMessages: []providers.ChatMessage{{Role: "assistant", Content: result}},
	}}, nil
}
func (s *stubEngineSession) Interrupt(context.Context, string) error { return nil }
func (s *stubEngineSession) Close(context.Context) error             { return nil }

// engineServiceFixture publishes engine.<id> into the synthetic service graph
// so retainEngineScope can capture the declared engine scope the same way the
// production resolved registry does.
type engineServiceFixture struct {
	embeddedModelConsumer
	descriptor agentengine.Descriptor
}

func (*engineServiceFixture) ID() string { return "fixture.engine-service" }
func (f *engineServiceFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{
		Name: "engine." + string(f.descriptor.ID), Version: "1.0.0",
		Methods: []pluginhost.ServiceMethodDescriptor{{Name: "descriptor", InputSchema: "engine.descriptor.input.v1", OutputSchema: "engine.descriptor.output.v1"}},
	}}
}
func (f *engineServiceFixture) InvokeService(_ context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method != "descriptor" {
		return nil, fmt.Errorf("unexpected engine method %s", p.Method)
	}
	return json.Marshal(struct {
		ID           agentengine.EngineID `json:"id"`
		Version      string               `json:"version"`
		Capabilities []string             `json:"capabilities"`
		Enabled      bool                 `json:"enabled"`
	}{f.descriptor.ID, f.descriptor.Version, f.descriptor.Capabilities, true})
}

// A plugin or managed caller can delegate a child to a selected external
// engine. Creation validates the choice, persists the engine binding, and
// turns run through that engine rather than the host loop.
func TestPluginSessionBindsSelectedEngine(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providersResponse("host")})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	journal, err := rt.Sessions.NewInferenceJournalRuntime("engine-test")
	if err != nil {
		t.Fatal(err)
	}
	rt.InferenceJournalRuntime = journal
	t.Cleanup(func() { _ = journal.Close() })
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	rt.StreamRunner.Tools = kit
	engine := &stubEngineSessionFactory{id: "fake-engine", caps: []string{"explicit-context"}, result: "engine done"}
	service := &engineServiceFixture{descriptor: agentengine.Descriptor{ID: "fake-engine", Version: "1", Capabilities: engine.caps}}
	owner := &pluginTurnLifecycleClient{id: "subagent", calls: make(chan pluginhost.AgentTurnLifecycleInput, 4)}
	attachTestModelGeneration(t, rt, owner, service)
	if err := rt.Engines().Register(engine); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, context.Background())

	parent, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{
		RequestID: "parent-1", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh,
	})
	if err != nil {
		t.Fatalf("parent create: %v", err)
	}

	// Host-managed context and catalog conveniences cannot cross the engine
	// boundary; each must fail explicitly instead of degrading silently.
	for name, params := range map[string]pluginhost.SessionCreateParams{
		"fork": {
			RequestID: "e-fork", Visibility: pluginhost.SessionVisibilityPlugin,
			ContextSource: pluginhost.SessionContextFork, ParentSessionID: parent.SessionID, Engine: "fake-engine",
		},
		"alias": {
			RequestID: "e-alias", Visibility: pluginhost.SessionVisibilityPlugin,
			ContextSource: pluginhost.SessionContextFresh, Engine: "fake-engine", ModelAlias: "review",
		},
		"tool_policy": {
			RequestID: "e-policy", Visibility: pluginhost.SessionVisibilityPlugin,
			ContextSource: pluginhost.SessionContextFresh, Engine: "fake-engine",
			ToolPolicy: &pluginhost.SessionToolPolicy{Allow: []string{"read_file"}},
		},
		"unknown_engine": {
			RequestID: "e-unknown", Visibility: pluginhost.SessionVisibilityPlugin,
			ContextSource: pluginhost.SessionContextFresh, Engine: "missing-engine",
		},
		// Delegated bounds are model-catalog grants; an external engine's
		// selection cannot be attested against them.
		"delegated_authorization": {
			RequestID: "e-delegated", Visibility: pluginhost.SessionVisibilityPlugin,
			ContextSource: pluginhost.SessionContextFresh, Engine: "fake-engine",
			ExecutionAuthorization: &pluginhost.ExecutionAuthorizationReference{Provider: "task", ID: "task-1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := rt.PluginSessionRouter.Create(ctx, owner.id, params); err == nil {
				t.Fatal("engine-incompatible create was admitted")
			}
		})
	}

	created, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{
		RequestID: "create-engine", Visibility: pluginhost.SessionVisibilityPlugin,
		ContextSource: pluginhost.SessionContextFresh, ParentSessionID: parent.SessionID,
		Engine: "fake-engine", Model: "native-model", Effort: "low", PermissionMode: "standard",
		Instructions: "engine session instructions",
	})
	if err != nil || created.SessionID == "" {
		t.Fatalf("create = %+v, %v", created, err)
	}
	persisted, ok, err := rt.Sessions.Find(created.SessionID)
	if err != nil || !ok {
		t.Fatalf("session row: %v", err)
	}
	if persisted.EngineID != "fake-engine" || persisted.Provider != "fake-engine" || persisted.Model != "native-model" || persisted.Effort != "low" {
		t.Fatalf("engine binding persisted = %+v", persisted)
	}

	result, err := rt.PluginSessionRouter.Send(ctx, owner.id, pluginhost.SessionSendParams{
		RequestID: "engine-turn", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "run the task"},
	})
	if err != nil || result.TurnID == "" {
		t.Fatalf("send = %+v, %v", result, err)
	}
	waitForTurnCompletedForThread(t, out, created.SessionID)

	select {
	case lifecycle := <-owner.calls:
		if lifecycle.State != pluginhost.TurnLifecycleCompleted || lifecycle.FinalOutput != "engine done" {
			t.Fatalf("lifecycle = %+v", lifecycle)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not receive terminal lifecycle")
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.bindings) != 1 || len(engine.histories) != 1 {
		t.Fatalf("engine bindings=%+v histories=%d", engine.bindings, len(engine.histories))
	}
	binding := engine.bindings[0]
	if binding.ThreadID != created.SessionID || binding.Model != "native-model" || binding.Effort != "low" || !strings.Contains(binding.Instructions, "engine session instructions") {
		t.Fatalf("engine binding = %+v", binding)
	}
	history := engine.histories[0]
	if last := history[len(history)-1]; last.Role != "user" || last.Content != "run the task" {
		t.Fatalf("engine turn input = %+v", history)
	}
	if history[0].Role != "system" || !strings.Contains(history[0].Content, "engine session instructions") {
		t.Fatalf("canonical session context missing from engine history: %+v", history[0])
	}

	// A reloaded thread keeps its persisted engine binding.
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	th.mu.Lock()
	engineID := th.EngineID
	th.mu.Unlock()
	if engineID != "fake-engine" {
		t.Fatalf("reloaded engine = %q", engineID)
	}
}
