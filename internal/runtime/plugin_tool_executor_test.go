package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
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

func TestPluginToolExecutorPreservesArgumentsAndRichResult(t *testing.T) {
	inner := &recordingToolExecutor{}
	executor := newPluginToolExecutor(inner, pluginhost.New(), "thread-1", "/workspace")
	rich := executor.(interface {
		ExecuteResult(context.Context, providers.ToolCall) (toolresult.Result, error)
	})
	result, err := rich.ExecuteResult(toolctx.WithStepIndex(context.Background(), 4), providers.ToolCall{ID: "call-1", Name: "demo", Arguments: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if result.TextProjection() != `{}` {
		t.Fatalf("result = %q", result.TextProjection())
	}
	if len(inner.calls) != 1 || inner.calls[0].Arguments != `{}` {
		t.Fatalf("calls = %+v", inner.calls)
	}
}

func TestPluginToolExecutorKeepsConcurrentCallsIsolated(t *testing.T) {
	inner := &recordingToolExecutor{}
	executor := newPluginToolExecutor(inner, pluginhost.New(), "thread", "/workspace")
	rich := executor.(interface {
		ExecuteResult(context.Context, providers.ToolCall) (toolresult.Result, error)
	})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := rich.ExecuteResult(context.Background(), providers.ToolCall{ID: id, Name: "demo", Arguments: `{}`})
			if err != nil {
				errs <- err
				return
			}
			want := `{}`
			if result.TextProjection() != want {
				errs <- fmt.Errorf("call %s result = %q, want %q", id, result.TextProjection(), want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
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
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	executable := os.Getenv("WUU_CODE_MODE_HOST")
	realHost := executable != ""
	if !realHost {
		executable = filepath.Join(root, "host")
	}
	service, err := codemode.NewService(codemode.ServiceConfig{Executable: executable, SessionID: "plugin-code-mode"})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	kit.SetCodeModeService(service)
	kit.SetCodeModeOnly(true)
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
	if realHost {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
		defer runtime.Cancel()
		run := func(call providers.ToolCall) string {
			messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
			if err != nil || len(messages) != 1 {
				t.Fatalf("tool call failed: %+v, %v", messages, err)
			}
			return messages[0].Content
		}
		args, _ := json.Marshal(map[string]any{"source": `const tool = ALL_TOOLS.find(t => t.description.includes("change state")); if (!tool || !tool.description.includes('"type":"object"')) throw new Error("tool schema missing"); text(await tools[tool.name]({}));`, "yield_time_ms": 1})
		result := run(providers.ToolCall{ID: "plugin-exec", Name: "exec", Arguments: string(args)})
		var output strings.Builder
		for step := 0; ; step++ {
			output.WriteString(result)
			var response struct {
				State  string `json:"state"`
				CellID string `json:"cell_id"`
			}
			if err := json.Unmarshal([]byte(result), &response); err != nil {
				t.Fatal(err)
			}
			if response.State != "Yielded" {
				break
			}
			args, _ = json.Marshal(map[string]any{"cell_id": response.CellID, "yield_time_ms": 1000})
			result = run(providers.ToolCall{ID: fmt.Sprintf("plugin-wait-%d", step), Name: "wait", Arguments: string(args)})
		}
		if !client.executed || !strings.Contains(output.String(), "changed") {
			t.Fatalf("nested plugin did not execute: %s", output.String())
		}
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

func TestCollaborationPluginToolsRequireExplicitOptIn(t *testing.T) {
	for _, scopes := range [][]string{nil, {"root"}, {"collaboration"}, {"root", "collaboration"}} {
		client := &pluginToolTestClient{scopes: scopes}
		host := pluginhost.New(client)
		name := host.ToolDefinitions()[0].Name
		executor := &pluginToolExecutor{inner: &recordingToolExecutor{}, host: host, threadID: "identity", scope: "collaboration"}
		allowed := false
		for _, scope := range scopes {
			if scope == "collaboration" {
				allowed = true
			}
		}
		found := false
		for _, def := range executor.Definitions() {
			if def.Name == name {
				found = true
			}
		}
		if found != allowed {
			t.Fatalf("tool discovery scopes %v: found=%v", scopes, found)
		}
		_, err := executor.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`})
		if (err == nil) != allowed || client.executed != allowed {
			t.Fatalf("tool dispatch scopes %v: %v, executed=%v", scopes, err, client.executed)
		}
	}
}

func TestCollaborationPluginReplacementRemovesRetiredTools(t *testing.T) {
	s, _ := collaborationTestSession(t)
	thread := collaborationTestThread(t, s, "plugin-generation", ThreadModelSelection{})
	client := &pluginToolTestClient{scopes: []string{"collaboration"}}
	s.PluginHost = pluginhost.New(client)
	name := s.PluginHost.ToolDefinitions()[0].Name
	s.ConfigureCollaborationTools(thread, "plugin-generation")
	if !s.HasCollaborationTools() {
		t.Fatal("opt-in plugin was not available")
	}
	s.PluginHost = pluginhost.New()
	s.ConfigureCollaborationTools(thread, "plugin-generation")
	for _, def := range thread.StreamRunner.Tools.Definitions() {
		if def.Name == name {
			t.Fatal("retired plugin stayed in conversation tools")
		}
	}
	if _, err := thread.StreamRunner.Tools.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`}); err == nil || client.executed {
		t.Fatal("retired plugin remained callable")
	}
}
