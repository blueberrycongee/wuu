package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
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
