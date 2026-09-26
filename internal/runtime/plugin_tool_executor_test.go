package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type recordingToolExecutor struct {
	mu               sync.Mutex
	calls            []providers.ToolCall
	authorized       []providers.ToolCall
	authorizationErr error
}

func (e *recordingToolExecutor) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "demo", InputSchema: map[string]any{"type": "object"}}}
}
func (e *recordingToolExecutor) Execute(context.Context, providers.ToolCall) (string, error) {
	panic("rich path expected")
}
func (e *recordingToolExecutor) ExecuteResult(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, call)
	e.mu.Unlock()
	return toolresult.FromText(call.Arguments), nil
}

func (e *recordingToolExecutor) AuthorizeTool(_ context.Context, call providers.ToolCall, _ agent.ToolMetadata) error {
	e.authorized = append(e.authorized, call)
	return e.authorizationErr
}

type pluginToolTestClient struct {
	executed bool
	scopes   []string
}

type denyPluginAuthorizer struct{ calls int }

func (a *denyPluginAuthorizer) Authorize(context.Context, tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	a.calls++
	return tools.AuthorizationDecision{Outcome: "deny", Reason: "plugin policy"}, nil
}

func (c *pluginToolTestClient) ID() string { return "policy-tool" }
func (c *pluginToolTestClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (c *pluginToolTestClient) Close(context.Context) error { return nil }
func (c *pluginToolTestClient) Tools() []pluginhost.ToolRegistration {
	return []pluginhost.ToolRegistration{{ID: "change", Description: "change state", ExecutionScopes: c.scopes, InputSchema: map[string]any{"type": "object"}}}
}
func (c *pluginToolTestClient) ExecuteTool(context.Context, pluginhost.ToolExecuteParams) (pluginhost.ToolExecuteResult, error) {
	c.executed = true
	return pluginhost.ToolExecuteResult{Result: toolresult.FromText("changed")}, nil
}

func TestPluginToolExecutorRejectsInvalidArgumentsBeforeHooksOrExecution(t *testing.T) {
	inner := &recordingToolExecutor{}
	executor := newPluginToolExecutor(inner, pluginhost.New(), "thread", "/workspace")

	_, err := executor.Execute(context.Background(), providers.ToolCall{
		ID: "call-write", Name: "write_file", Arguments: `{"path":"page.html","content":"truncated`,
	})
	if err == nil {
		t.Fatal("expected invalid arguments error")
	}
	if got := toolerrors.Kind(err); got != toolerrors.InvalidArguments {
		t.Fatalf("error kind = %q, want %q: %v", got, toolerrors.InvalidArguments, err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("inner executor must not be called, got %+v", inner.calls)
	}
}

func TestPluginToolExecutorAuthorizesBeforeDispatch(t *testing.T) {
	client := &pluginToolTestClient{}
	host := pluginhost.New(client)
	name := host.ToolDefinitions()[0].Name
	inner := &recordingToolExecutor{authorizationErr: fmt.Errorf("policy denied")}
	executor := newPluginToolExecutor(inner, host, "thread", "/workspace")
	_, err := executor.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`})
	if err == nil || client.executed || len(inner.authorized) != 1 {
		t.Fatalf("error = %v executed = %v authorized = %+v", err, client.executed, inner.authorized)
	}
}

func TestPluginToolExecutorUsesToolkitBoundaryAndAuthorizer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*tools.Toolkit) *denyPluginAuthorizer
		wantAuth  int
	}{
		{name: "read only boundary", configure: func(kit *tools.Toolkit) *denyPluginAuthorizer {
			kit.SetBoundary(tools.ReadOnlyBoundary())
			return nil
		}},
		{name: "custom authorizer", configure: func(kit *tools.Toolkit) *denyPluginAuthorizer {
			authorizer := &denyPluginAuthorizer{}
			kit.SetAuthorizer(authorizer)
			return authorizer
		}, wantAuth: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kit, err := tools.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			authorizer := tc.configure(kit)
			client := &pluginToolTestClient{}
			host := pluginhost.New(client)
			name := host.ToolDefinitions()[0].Name
			executor := newPluginToolExecutor(kit, host, "thread", kit.RootDir())

			_, err = executor.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`})
			if err == nil || client.executed {
				t.Fatalf("plugin tool escaped policy: error=%v executed=%v", err, client.executed)
			}
			if authorizer != nil && authorizer.calls != tc.wantAuth {
				t.Fatalf("authorizer calls = %d, want %d", authorizer.calls, tc.wantAuth)
			}
		})
	}
}

func TestPluginToolExecutorRunsInsideToolHooks(t *testing.T) {
	client := &pluginToolTestClient{}
	host := pluginhost.New(client)
	name := host.ToolDefinitions()[0].Name
	inner := &recordingToolExecutor{}
	dispatcher := hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.PreToolUse: {{Matcher: name, Command: "exit 2"}},
	}))
	executor := newPluginAwareToolExecutor(inner, host, dispatcher, "thread", "thread", "/workspace")

	_, err := executor.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`})
	if err == nil || !hooks.IsBlocked(err) {
		t.Fatalf("expected plugin tool hook denial, got %v", err)
	}
	if client.executed || len(inner.authorized) != 0 {
		t.Fatalf("hook must run before authorization and dispatch: executed=%v authorized=%+v", client.executed, inner.authorized)
	}
}

func TestReplacePluginToolHostPreservesHookLayer(t *testing.T) {
	oldHost := pluginhost.New(&pluginToolTestClient{})
	newClient := &pluginToolTestClient{}
	newHost := pluginhost.New(newClient)
	name := newHost.ToolDefinitions()[0].Name
	dispatcher := hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.PreToolUse: {{Matcher: name, Command: "exit 2"}},
	}))
	executor := newPluginAwareToolExecutor(&recordingToolExecutor{}, oldHost, dispatcher, "thread", "thread", "/workspace")
	executor = replacePluginToolHost(executor, newHost, "thread", "/workspace")

	_, err := executor.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`})
	if err == nil || !hooks.IsBlocked(err) || newClient.executed {
		t.Fatalf("replacement lost hook layer: error=%v executed=%v", err, newClient.executed)
	}
}

func TestCodeModeOnlyIncludesPluginToolsInNestedSurface(t *testing.T) {
	root := t.TempDir()
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	client := &pluginToolTestClient{}
	host := pluginhost.New(client)
	name := host.ToolDefinitions()[0].Name
	executor := newPluginToolExecutor(kit, host, "thread", root)
	for _, def := range executor.Definitions() {
		if def.Name == name || def.Name == "read_file" {
			t.Fatalf("leaf tool exposed at top level: %s", def.Name)
		}
	}
	nested, err := kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, def := range nested {
		if def.Name == name {
			found = true
		}
		if def.Name == "write_file" {
			t.Fatal("catalog advertised a tool unavailable in the active model profile")
		}
	}
	if !found {
		t.Fatal("plugin tool missing from nested execution surface")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	code := "return await tools[" + strconv.Quote(name) + "]({})"
	args, _ := json.Marshal(map[string]any{"code": code, "description": "Call plugin"})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "plugin-run", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || !client.executed || !strings.Contains(messages[0].Content, "changed") {
		t.Fatalf("plugin bridge: %+v %v", messages, err)
	}

	executor = replacePluginToolHost(executor, pluginhost.New(), "thread", root)
	nested, err = kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range nested {
		if def.Name == name {
			t.Fatal("replaced plugin host left a stale code-mode catalog")
		}
	}
}
