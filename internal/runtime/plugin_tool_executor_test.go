package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
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
	executed   bool
	scopes     []string
	directOnly bool
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
	return []pluginhost.ToolRegistration{{ID: "change", DirectOnly: c.directOnly, Description: "change state", ExecutionScopes: c.scopes, InputSchema: map[string]any{"type": "object"}}}
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
	executor = replacePluginToolBindings(executor, newHost, dispatcher, "thread", "/workspace")

	_, err := executor.Execute(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`})
	if err == nil || !hooks.IsBlocked(err) || newClient.executed {
		t.Fatalf("replacement lost hook layer: error=%v executed=%v", err, newClient.executed)
	}
}

func TestCodeModeOnlyIncludesPluginToolsInNestedSurface(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the TypeScript plugin process")
	}
	root := t.TempDir()
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	kit.DisableTools("write_file")
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})

	// Exercise the shipped read-only helper through its real JSONL transport
	// and registry. The fixture supplies session data, not plugin behavior.
	item := serviceTestPlugin("peers", "plugin:peers", "fixture-generation")
	item.Root, err = filepath.Abs("../plugin/bundled/peers")
	if err != nil {
		t.Fatal(err)
	}
	item.Runtime = &pluginpkg.RuntimeSpec{Command: node, Args: []string{"runtime.mjs"}, Timeout: 5}
	var listCalls atomic.Int64
	router := NewPluginSessionRouter()
	router.Bind(nil, nil, func(_ context.Context, pluginID string, params pluginhost.SessionListParams) (pluginhost.SessionListResult, error) {
		listCalls.Add(1)
		if pluginID != item.ID || params.Scope != "shared" {
			return pluginhost.SessionListResult{}, fmt.Errorf("unexpected session owner/scope: %s/%s", pluginID, params.Scope)
		}
		return pluginhost.SessionListResult{Sessions: []pluginhost.SessionSummary{
			{SessionID: "thread", WorkspaceID: "workspace"},
			{SessionID: "peer", WorkspaceID: "workspace"},
			{SessionID: "foreign", WorkspaceID: "other-workspace"},
		}}, nil
	}, nil)
	home, state := t.TempDir(), t.TempDir()
	makeHost := func() *pluginhost.Host {
		host, _, err := buildPluginHost([]pluginpkg.Plugin{item}, root, "workspace", home, state, map[string]bool{item.ID: true}, startPluginClient, router, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = host.Close(context.Background()) })
		if err := activatePluginHost(context.Background(), host); err != nil {
			t.Fatal(err)
		}
		return host
	}
	host := makeHost()
	name, sendName := "", ""
	for _, definition := range host.ToolDefinitions() {
		tool, _ := host.Tool(definition.Name)
		switch tool.Registration.ID {
		case "list_peers":
			name = definition.Name
		case "send_message":
			sendName = definition.Name
		}
	}
	if name == "" || sendName == "" {
		t.Fatal("peer tools missing")
	}
	executor := newPluginToolExecutor(kit, host, "thread", root)
	foundSend := false
	for _, def := range executor.Definitions() {
		if def.Name == name {
			t.Fatalf("leaf tool exposed at top level: %s", def.Name)
		}
		foundSend = foundSend || def.Name == sendName
	}
	if !foundSend {
		t.Fatal("peer send control missing from direct surface")
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
		if def.Name == "write_file" || def.Name == sendName {
			t.Fatalf("catalog advertised unavailable nested tool %s", def.Name)
		}
	}
	if !found {
		t.Fatal("plugin tool missing from nested execution surface")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "thread", ExecutionID: "turn"})
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	code := `const catalog = await searchTools("list_peers");
const discovered = catalog.tools.find(tool => tool.name === ` + strconv.Quote(name) + `);
if (!discovered) throw Error("peer discovery missing");
const result = await tools[discovered.name]({});
return JSON.parse(result.content[0].text).peers.map(peer => peer.session_id);`
	args, _ := json.Marshal(map[string]any{"code": code, "description": "Discover and filter peers"})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "plugin-run", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || listCalls.Load() != 1 || !strings.Contains(messages[0].Content, `["peer"]`) {
		t.Fatalf("plugin bridge: %+v %v, calls=%d", messages, err, listCalls.Load())
	}

	nestedCtx := toolctx.WithNestedCall(ctx)
	call := providers.ToolCall{ID: "peer-read", Name: name, Arguments: "{}"}
	rich := executor.(agent.RichToolExecutor)
	kit.SetBoundary(tools.ReadOnlyBoundary())
	if result, err := rich.ExecuteResult(nestedCtx, call); err != nil || !strings.Contains(result.TextProjection(), `"session_id":"peer"`) {
		t.Fatalf("read-only peer discovery: %+v %v", result, err)
	}
	beforeDenied := listCalls.Load()
	if _, err := rich.ExecuteResult(nestedCtx, providers.ToolCall{Name: sendName, Arguments: `{}`}); err == nil || !strings.Contains(err.Error(), "must be called directly") {
		t.Fatalf("nested peer send was not rejected: %v", err)
	}
	authorizer := &denyPluginAuthorizer{}
	kit.SetAuthorizer(authorizer)
	if _, err := rich.ExecuteResult(nestedCtx, call); err == nil || authorizer.calls != 1 || listCalls.Load() != beforeDenied {
		t.Fatalf("peer discovery bypassed authorization: %v", err)
	}
	kit.SetAuthorizer(nil)
	kit.SetAgentIdentity("child", "/root/child")
	nested, err = kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range nested {
		if def.Name == name {
			t.Fatal("root-only peer discovery exposed to child")
		}
	}
	if _, err := rich.ExecuteResult(nestedCtx, call); err == nil || listCalls.Load() != beforeDenied {
		t.Fatalf("child escaped peer execution scope: %v", err)
	}
	kit.SetAgentIdentity("root", "/root")

	kit.DisableTools(name)
	nested, err = kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range nested {
		if definition.Name == name {
			t.Fatal("disabled plugin remained discoverable")
		}
	}
	if _, err := rich.ExecuteResult(nestedCtx, call); err == nil || listCalls.Load() != beforeDenied {
		t.Fatalf("disabled plugin bypassed live authorization: %v", err)
	}
	kit.EnableTools(name)

	// Replacement must bind the caller's session, not a process-global identity.
	replacement := makeHost()
	executor = replacePluginToolBindings(executor, replacement, nil, "missing-source", root)
	if _, err := executor.(agent.RichToolExecutor).ExecuteResult(nestedCtx, call); err == nil || !strings.Contains(err.Error(), "non-archived user-visible session") {
		t.Fatalf("unlisted source gained peer access: %v", err)
	}
	executor = replacePluginToolBindings(executor, replacement, nil, "thread", root)
	if result, err := executor.(agent.RichToolExecutor).ExecuteResult(nestedCtx, call); err != nil || !strings.Contains(result.TextProjection(), `"session_id":"peer"`) || strings.Contains(result.TextProjection(), "foreign") {
		t.Fatalf("replacement peer discovery: %+v %v", result, err)
	}
	if _, found := host.RetirePlugin(ctx, item.ID, fmt.Errorf("disabled")); !found {
		t.Fatal("peer retirement failed")
	}
	before := listCalls.Load()
	if _, err := rich.ExecuteResult(nestedCtx, call); err == nil || listCalls.Load() != before {
		t.Fatalf("retired peer binding remained executable: %v", err)
	}
	executor = replacePluginToolBindings(executor, pluginhost.New(), nil, "thread", root)
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

func TestPTCDirectOnlyPluginRouting(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	client := &pluginToolTestClient{directOnly: true}
	host := pluginhost.New(client)
	name := host.ToolDefinitions()[0].Name
	executor := newPluginToolExecutor(kit, host, "thread", kit.RootDir())
	found := false
	for _, def := range executor.Definitions() {
		if def.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatal("direct-only extension unavailable")
	}
	nested, err := kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range nested {
		if def.Name == name {
			t.Fatal("direct-only extension exposed as binding")
		}
	}
	rich := executor.(agent.RichToolExecutor)
	if _, err := rich.ExecuteResult(toolctx.WithNestedCall(context.Background()), providers.ToolCall{Name: name, Arguments: `{}`}); err == nil || client.executed {
		t.Fatal("nested control executed")
	}
	if _, err := rich.ExecuteResult(context.Background(), providers.ToolCall{Name: name, Arguments: `{}`}); err != nil || !client.executed {
		t.Fatalf("direct control: %v", err)
	}
	kit.DisableTools(name)
	for _, def := range executor.Definitions() {
		if def.Name == name {
			t.Fatal("disabled direct extension exposed")
		}
	}
}

func TestPTCProductionWrappersPinPluginSurfaceUntilRunEnds(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	client := &pluginToolTestClient{}
	host := pluginhost.New(client)
	name := host.ToolDefinitions()[0].Name
	executor := newPluginAwareToolExecutor(kit, host, hooks.NewDispatcher(hooks.NewRegistry(nil)), "thread", "thread", kit.RootDir())
	freezer, ok := executor.(agent.ToolSurfaceFreezer)
	if !ok {
		t.Fatal("production wrapper lost snapshot lifecycle")
	}
	freezer.FreezeToolSurface()
	freezer.FreezeToolSurface()
	defer freezer.UnfreezeToolSurface()
	_, found := host.RetirePlugin(context.Background(), client.ID(), fmt.Errorf("retired"))
	if !found {
		t.Fatal("plugin retirement failed")
	}
	freezer.UnfreezeToolSurface()
	nested, err := kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, def := range nested {
		if def.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatal("frozen schema changed in active run")
	}
	if _, err := executor.(agent.RichToolExecutor).ExecuteResult(toolctx.WithNestedCall(context.Background()), providers.ToolCall{Name: name, Arguments: `{}`}); err == nil || client.executed {
		t.Fatal("snapshot allowed a retired tool to execute")
	}
	freezer.UnfreezeToolSurface()
	nested, err = kit.CodeModeNestedSurface()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range nested {
		if def.Name == name {
			t.Fatal("next run kept retired schema")
		}
	}
}
