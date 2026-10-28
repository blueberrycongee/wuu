package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

type externalToolFixture struct {
	*runtimeServiceConsumer
	fail         bool
	executeScope *pluginhost.ExecutionScope
	finalized    int
	prepared     int
}

func (*externalToolFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{toolservice.Descriptor()}
}
func (p *externalToolFixture) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if call.Caller != toolExecutionConsumer || call.ExecutionID == "" {
		return nil, errors.New("missing authenticated execution")
	}
	if p.fail {
		return nil, serviceError("external_failure", "external tool registry unavailable")
	}
	switch call.Method {
	case "list":
		return json.RawMessage(`{"tools":[{"name":"external_only","description":"Selected tool","input_schema":{"type":"object"}}],"catalog":[],"catalog_prompt":"external catalog","system_fragment":"external guidance"}`), nil
	case "resolve":
		return json.RawMessage(`{"found":true,"supports":true,"metadata":{"read_only":true,"concurrency_safe":true}}`), nil
	case "prepare":
		p.prepared++
		return json.RawMessage(`{"messages":[{"role":"user","content":"external preparation","hidden":true}]}`), nil
	case "finalize":
		p.finalized++
		var input toolservice.FinalizeRequest
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		if input.Result == nil {
			return nil, errors.New("missing result")
		}
		text := "external settlement"
		input.Result.ModelText = &text
		return json.Marshal(toolservice.ExecutionResult{Result: *input.Result})
	case "execute":
		scope, err := call.CallerExecution(true)
		if err != nil {
			return nil, err
		}
		p.executeScope = &scope
		return json.RawMessage(`{"result":{"content":[{"type":"text","text":"external result"}]}}`), nil
	default:
		return json.RawMessage(`{}`), nil
	}
}

func TestSelectedToolsNeverInitializeDefaultBackend(t *testing.T) {
	ctx := context.Background()
	provider := &externalToolFixture{runtimeServiceConsumer: &runtimeServiceConsumer{id: "external.tools", state: pluginhost.StatePrepared}}
	consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}}}
	registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
	host := pluginhost.New(provider, consumer)
	host.AttachServiceRegistry(registry, conflicts)
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	initialized := false
	scope := toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
		initialized = true
		return toolservice.Backend{}, errors.New("default backend must stay dormant")
	})
	executor := newSelectedToolExecutor(host, scope, selectedToolIdentity{threadID: "thread", actorID: "actor", cwd: "/workspace"})
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "thread", ExecutionID: "turn"})
	definitions, err := executor.DefinitionsContext(ctx)
	if err != nil || len(definitions) != 1 || definitions[0].Name != "external_only" || initialized {
		t.Fatalf("selected definitions: %+v %v initialized=%v", definitions, err, initialized)
	}
	if _, err := executor.ExecuteResult(ctx, providers.ToolCall{ID: "call", Name: "external_only", Arguments: `{"thread_id":"forged","actor_id":"forged","cwd":"/forged"}`}); err != nil {
		t.Fatal(err)
	}
	if provider.executeScope == nil || provider.executeScope.ThreadID != "thread" || provider.executeScope.TurnID != "turn" || provider.executeScope.ActorID != "actor" || provider.executeScope.CallID != "call" || provider.executeScope.CWD != "/workspace" {
		t.Fatalf("wrong authenticated scope: %+v", provider.executeScope)
	}
	messages, err := executor.PrepareModelStep(ctx, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "external preparation" || provider.prepared != 1 {
		t.Fatalf("prepare: %+v %v", messages, err)
	}
	if provider.finalized != 0 {
		t.Fatal("execute finalized early")
	}
	settled := executor.FinalizeToolResult(providers.ToolCall{ID: "call", Name: "external_only"}, toolresult.FromErrorText("normalized failure"))
	if !settled.IsError || settled.ModelText == nil || *settled.ModelText != "external settlement" || provider.finalized != 1 {
		t.Fatalf("settlement: %+v count=%d", settled, provider.finalized)
	}
	provider.fail = true
	if settled := executor.FinalizeToolResult(providers.ToolCall{ID: "failed-finalizer", Name: "external_only"}, toolresult.FromText("must not fall back")); !settled.IsError || settled.TextProjection() == "must not fall back" {
		t.Fatalf("finalization failure fell back: %+v", settled)
	}
	if _, err := executor.DefinitionsContext(ctx); err == nil {
		t.Fatal("registry failure hidden")
	}
	if _, err := executor.ExecuteResult(ctx, providers.ToolCall{Name: "external_only", Arguments: "{}"}); err == nil {
		t.Fatal("execution fell back")
	}
	if initialized {
		t.Fatal("external service initialized native backend")
	}
}

func TestSelectedNativeRebindPreservesContributionsPermissionsAndHooks(t *testing.T) {
	makeHost := func(client *pluginToolTestClient) *pluginhost.Host {
		provider := toolservice.NewService()
		consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}}}
		registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer, client)
		host := pluginhost.New(provider, consumer, client)
		host.AttachServiceRegistry(registry, conflicts)
		if err := host.Activate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = host.Close(context.Background()) })
		return host
	}
	first, second := &pluginToolTestClient{}, &pluginToolTestClient{}
	oldHost, newHost := makeHost(first), makeHost(second)
	inner := &recordingToolExecutor{}
	original := newPluginAwareToolExecutor(inner, oldHost, nil, "thread", "thread", "/workspace")
	rebound := replacePluginToolBindings(original, newHost, nil, "thread", "/workspace")
	name := newHost.ToolDefinitions()[0].Name
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "thread", ExecutionID: "turn"})
	if _, err := rebound.Execute(ctx, providers.ToolCall{ID: "new", Name: name, Arguments: "{}"}); err != nil {
		t.Fatal(err)
	}
	if !second.executed || first.executed || len(inner.authorized) != 1 {
		t.Fatalf("rebound dispatch: old=%v new=%v auth=%d", first.executed, second.executed, len(inner.authorized))
	}
	if _, err := original.Execute(ctx, providers.ToolCall{ID: "old", Name: name, Arguments: "{}"}); err != nil {
		t.Fatal(err)
	}
	if !first.executed || len(inner.authorized) != 2 {
		t.Fatal("retained generation was redirected")
	}
	dispatcher := hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{hooks.PreToolUse: {{Matcher: name, Command: "exit 2"}}}))
	blocked := replacePluginToolBindings(rebound, newHost, dispatcher, "thread", "/workspace")
	if _, err := blocked.Execute(ctx, providers.ToolCall{ID: "blocked", Name: name, Arguments: "{}"}); !hooks.IsBlocked(err) {
		t.Fatalf("rebind lost pre-hook: %v", err)
	}
	if len(inner.authorized) != 2 {
		t.Fatal("blocked call reached permission gate")
	}
	if _, ok := blocked.(agent.ToolResultFinalizer); !ok {
		t.Fatal("hook wrapper lost finalizer")
	}
}

// Native composition must preserve the same invocation-owned envelope as an
// external agent.tools provider, including metadata left behind by a Go error.
type nativeFinalizationTools struct {
	mu              sync.Mutex
	context         map[string]string
	finalized       map[string]int
	legacyFinalized int
}

func (*nativeFinalizationTools) Definitions() []providers.ToolDefinition { return nil }
func (*nativeFinalizationTools) ToolMetadata(providers.ToolCall) (agent.ToolMetadata, bool) {
	return agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}
func (e *nativeFinalizationTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	e.mu.Lock()
	e.context[call.ID] = "execute-context[" + call.ID + "]"
	e.mu.Unlock()
	if call.Name == "fail" {
		return "", errors.New("native execution failed")
	}
	return call.ID, nil
}
func (e *nativeFinalizationTools) TakeAdditionalContext(call providers.ToolCall) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	value := e.context[call.ID]
	delete(e.context, call.ID)
	return value
}
func (*nativeFinalizationTools) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	return []providers.LoadableToolDefinition{{Name: "owned_" + call.ID, Description: "execute", InputSchema: map[string]any{"type": "object"}}}
}
func (e *nativeFinalizationTools) FinalizeToolResult(_ providers.ToolCall, result toolresult.Result) toolresult.Result {
	e.mu.Lock()
	e.legacyFinalized++
	e.mu.Unlock()
	return result
}
func (e *nativeFinalizationTools) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) agent.ToolFinalization {
	e.mu.Lock()
	e.finalized[call.ID]++
	e.mu.Unlock()
	return agent.ToolFinalization{Result: result,
		AdditionalContext: fmt.Sprintf("finalize-context[%s]:error=%t", call.ID, result.IsError),
		DiscoveredTools:   []providers.LoadableToolDefinition{{Name: "owned_" + call.ID, Description: "finalize", InputSchema: map[string]any{"type": "object"}}},
	}
}

func TestSelectedNativeFinalizationMetadata(t *testing.T) {
	for _, decorated := range []bool{false, true} {
		t.Run(fmt.Sprintf("plugin-decoration-%t", decorated), func(t *testing.T) {
			provider := toolservice.NewService()
			consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}}}
			registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
			host := pluginhost.New(provider, consumer)
			host.AttachServiceRegistry(registry, conflicts)
			t.Cleanup(func() { _ = host.Close(context.Background()) })
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			inner := &nativeFinalizationTools{context: map[string]string{}, finalized: map[string]int{}}
			cwd := t.TempDir()
			var executor agent.ToolExecutor = newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				return toolservice.Backend{Executor: inner}, nil
			}), selectedToolIdentity{threadID: "native-finalization", cwd: cwd})
			if decorated {
				executor = newPluginAwareToolExecutor(inner, host, nil, "native-finalization", "native-finalization", cwd)
			}
			executor = hooks.NewHookedExecutor(executor, hooks.NewDispatcher(hooks.NewRegistry(nil)), "native-finalization", cwd)
			runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor})
			defer runtime.Close()
			ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "native-finalization", ExecutionID: "turn"})
			calls := []providers.ToolCall{{ID: "A", Name: "echo", Arguments: `{}`}, {ID: "B", Name: "fail", Arguments: `{}`}}
			messages, err := runtime.ExecuteFinalCalls(ctx, calls, nil)
			if err != nil || len(messages) != len(calls) {
				t.Fatalf("native outcomes: %+v %v", messages, err)
			}
			for i, message := range messages {
				if message.ToolResult == nil || message.ToolResult.IsError != (i == 1) {
					t.Fatalf("normalization changed: %+v", message)
				}
				if len(message.DiscoveredTools) != 1 || message.DiscoveredTools[0].Name != "owned_"+calls[i].ID || message.DiscoveredTools[0].Description != "finalize" {
					t.Errorf("native finalization discovery lost or misattributed: %+v", message.DiscoveredTools)
				}
			}
			var text strings.Builder
			for _, segment := range runtime.TakeRequestContextSegments() {
				for _, message := range segment.Messages {
					text.WriteString(message.Content)
				}
			}
			for i, call := range calls {
				executed, finalized := "execute-context["+call.ID+"]", fmt.Sprintf("finalize-context[%s]:error=%t", call.ID, i == 1)
				if strings.Count(text.String(), executed) != 1 || strings.Count(text.String(), finalized) != 1 || strings.Index(text.String(), executed) > strings.Index(text.String(), finalized) {
					t.Errorf("native metadata lost or reordered for %s: %s", call.ID, text.String())
				}
				if inner.finalized[call.ID] != 1 {
					t.Errorf("rich finalization count for %s = %d", call.ID, inner.finalized[call.ID])
				}
			}
			if inner.legacyFinalized != 0 || len(inner.context) != 0 || len(runtime.TakeRequestContextSegments()) != 0 {
				t.Errorf("native metadata not drained exactly once: legacy=%d retained=%v", inner.legacyFinalized, inner.context)
			}
		})
	}
}
