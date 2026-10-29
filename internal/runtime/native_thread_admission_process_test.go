package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

// A cold external provider may delegate its thread surface to the native
// backend during thread admission. It must bind a complete thread-owned kit.
func TestNativeToolkitAdmittedAfterExternalWorkspaceStartup(t *testing.T) {
	for _, mode := range []string{"default-model", "alternate-model"} {
		t.Run(mode, func(t *testing.T) { testNativeToolkitAdmittedAfterExternalWorkspaceStartup(t, mode) })
	}
}

func testNativeToolkitAdmittedAfterExternalWorkspaceStartup(t *testing.T, mode string) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("original"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	requests := make(chan []providers.ChatMessage, 4)
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "bad input", 400)
			return
		}
		requests <- input.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		if modelCalls.Add(1) == 1 {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"worker-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"sample.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"worker done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
		}
	}))
	defer model.Close()
	plugin := testRuntimePlugin("lazy-native-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = owner.Cleanup() })
	require.Nil(t, owner.Toolkit, "external workspace must not provision native toolkit")
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	var thread *ThreadRuntime
	if mode == "alternate-model" {
		thread, err = owner.NewThreadRuntimeForRootModel("admitted-thread", root, ThreadModelSelection{Provider: "fixture", Model: "alternate"})
	} else {
		thread, err = owner.NewThreadRuntime("admitted-thread")
	}
	require.NoError(t, err)
	t.Cleanup(func() {
		if control := thread.NativeAgentControl(); control != nil {
			control.BeginShutdown()
			control.StopAll()
			_ = control.WaitOwnedWorkerExecutions(context.Background())
			control.Close()
		}
		_ = thread.CloseNativeToolScope(context.Background())
		_ = thread.CloseArtifacts()
		owner.ReleasePluginGeneration(thread.PluginGeneration)
	})
	kit, control := thread.NativeResources()
	require.NotNil(t, kit)
	require.NotNil(t, control)
	require.Equal(t, "admitted-thread", kit.SessionID())
	require.Equal(t, "admitted-thread", control.SessionID())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "admitted-thread", ExecutionID: "turn"})
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "read_only"}, false, nil)
	read, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.NoError(t, err)
	require.Contains(t, read, "original")
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "denied", Name: "write_file", Arguments: `{"path":"sample.txt","content":"denied"}`})
	require.Error(t, err)
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "allowed", Name: "write_file", Arguments: `{"path":"sample.txt","content":"changed"}`})
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
	require.NoError(t, err)
	require.Equal(t, "changed", string(data))
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "read_only"}, false, nil)
	worker, err := control.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "admission", Description: "verify worker", Prompt: "read sample", Synchronous: true})
	require.NoError(t, err)
	require.Empty(t, worker.Error)
	for i := 0; i < 2; i++ {
		select {
		case messages := <-requests:
			if i == 1 {
				var text strings.Builder
				for _, message := range messages {
					text.WriteString(message.Content)
				}
				require.Contains(t, text.String(), "changed")
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	side, err := owner.NewSideThreadRunner("admission-side", root, ThreadModelSelection{}, "side prompt", "admitted-thread")
	require.NoError(t, err)
	defer CloseSideThreadTools(context.Background(), side)
	sideCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "admission-side", ExecutionID: "side-turn"})
	sideRead, err := side.Tools.Execute(sideCtx, providers.ToolCall{ID: "side-read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.NoError(t, err)
	require.Contains(t, sideRead, "changed")
	_, err = side.Tools.Execute(sideCtx, providers.ToolCall{ID: "side-write", Name: "write_file", Arguments: `{"path":"sample.txt","content":"forbidden"}`})
	require.Error(t, err)

	// The state-bound side runner keeps the parent archive root while the
	// parent's own lease retires, then releases it after side execution.
	settled := kit.FinalizeToolResult(providers.ToolCall{ID: "archive", Name: "synthetic"}, toolresult.FromText(strings.Repeat("side-evidence", 10000)))
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(settled.TextProjection()), &envelope))
	next, err := json.Marshal(envelope["continuation"].(map[string]any)["next"])
	require.NoError(t, err)
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	leasedSide, release, err := owner.NewSideThreadRunnerForState(state, "leased-side", root, ThreadModelSelection{}, "side prompt", "admitted-thread")
	require.NoError(t, err)
	require.NotNil(t, release)
	defer CloseSideThreadTools(context.Background(), leasedSide)
	leasedCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "leased-side", ExecutionID: "leased-turn"})
	call := providers.ToolCall{ID: "recover", Name: "recover_tool_result", Arguments: string(next)}
	require.NoError(t, thread.CloseArtifacts())
	page, err := leasedSide.Tools.Execute(leasedCtx, call)
	require.NoError(t, err)
	require.Contains(t, page, "side-evidence")
	require.NoError(t, CloseSideThreadTools(context.Background(), leasedSide))
	require.NoError(t, release())

	// A different side workspace discovers and refreshes its own local skills.
	sideRoot := t.TempDir()
	writeSkill := func(name, body string) {
		dir := filepath.Join(sideRoot, ".wuu", "skills", name)
		require.NoError(t, os.MkdirAll(dir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: local fixture\n---\n"+body+"\n"), 0600))
	}
	writeSkill("side-first", "FIRST_SIDE_SKILL")
	localSide, err := owner.NewSideThreadRunner("local-side", sideRoot, ThreadModelSelection{}, "side prompt", "admitted-thread")
	require.NoError(t, err)
	defer CloseSideThreadTools(context.Background(), localSide)
	localCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "local-side", ExecutionID: "local-turn"})
	loaded, err := localSide.Tools.Execute(localCtx, providers.ToolCall{ID: "load-first", Name: "load_skill", Arguments: `{"name":"side-first"}`})
	require.NoError(t, err)
	require.Contains(t, loaded, "FIRST_SIDE_SKILL")
	writeSkill("side-later", "LATER_SIDE_SKILL")
	preparer, ok := localSide.Tools.(agent.ModelStepPreparer)
	require.True(t, ok)
	_, err = preparer.PrepareModelStep(localCtx, nil)
	require.NoError(t, err)
	refreshed, err := localSide.Tools.Execute(localCtx, providers.ToolCall{ID: "load-later", Name: "load_skill", Arguments: `{"name":"side-later"}`})
	require.NoError(t, err)
	require.Contains(t, refreshed, "LATER_SIDE_SKILL")
}

func TestNativeGenerationDependenciesSharedAcrossThreads(t *testing.T) {
	for _, mode := range []string{"cold", "hot"} {
		t.Run(mode, func(t *testing.T) { testNativeGenerationDependenciesSharedAcrossThreads(t, mode) })
	}
}

func testNativeGenerationDependenciesSharedAcrossThreads(t *testing.T, mode string) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("shared"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	mcpLog := filepath.Join(root, "mcp-events.log")
	plugin := testRuntimePlugin("native-shared-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	cfg.MCPServers = map[string]config.MCPServerConfig{"shared": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), mcpLog}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	if mode == "hot" {
		candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins)
		require.NoError(t, err)
		require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
		require.Nil(t, owner.Toolkit)
	}
	generation := owner.pluginGeneration
	require.False(t, generation.nativeDependencies.initialized, "external startup must not initialize native dependencies")
	_, err = os.Stat(mcpLog)
	require.True(t, os.IsNotExist(err), "external startup must not start MCP")
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	type admission struct {
		thread *ThreadRuntime
		err    error
	}
	firstDone, secondDone := make(chan admission, 1), make(chan admission, 1)
	go func() {
		thread, err := owner.NewThreadRuntimeForRootModel("shared-first", root, ThreadModelSelection{Provider: "fixture", Model: "alternate"})
		firstDone <- admission{thread, err}
	}()
	go func() { thread, err := owner.NewThreadRuntime("shared-second"); secondDone <- admission{thread, err} }()
	firstResult, secondResult := <-firstDone, <-secondDone
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	first, second := firstResult.thread, secondResult.thread
	cleanup := func(thread *ThreadRuntime) {
		if c := thread.NativeAgentControl(); c != nil {
			c.BeginShutdown()
			c.StopAll()
			_ = c.WaitOwnedWorkerExecutions(context.Background())
			c.Close()
		}
		_ = thread.CloseNativeToolScope(context.Background())
		_ = thread.CloseArtifacts()
		owner.ReleasePluginGeneration(thread.PluginGeneration)
	}
	finish := sync.OnceFunc(func() { cleanup(second); _, _ = owner.Cleanup() })
	defer finish()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(mcpLog)
		return err == nil && strings.Count(string(data), "start ") == 1
	}, 5*time.Second, 10*time.Millisecond, "MCP should start once")
	require.NotSame(t, first.NativeToolkit(), second.NativeToolkit())
	code := first.NativeToolkit().CodeModeService()
	require.NotNil(t, code)
	require.Same(t, code, second.NativeToolkit().CodeModeService())
	cleanup(first)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "shared-second", ExecutionID: "turn"})
	result, err := second.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.NoError(t, err)
	require.Contains(t, result, "shared")
	_, err = owner.RefreshSystemPrompt("fixture", "fixture")
	require.ErrorContains(t, err, "native tool backend was not initialized during workspace admission")
	data, err := os.ReadFile(mcpLog)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(data), "start "), "threads must share one MCP process")
	finish()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(mcpLog)
		return err == nil && strings.Count(string(data), "stop ") == 1
	}, 5*time.Second, 10*time.Millisecond, "generation retirement must stop shared MCP")
}

func TestNativeThreadAdmissionDependencyFailureFailsClosed(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "admit")
	plugin := testRuntimePlugin("required-native-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugin.MCPServers = map[string]config.MCPServerConfig{"broken": {Command: "/definitely/not/a/wuu-mcp-server"}}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = owner.Cleanup() })
	require.Nil(t, owner.Toolkit)
	// The selected plugin is required; bind that requirement before candidate
	// initialization so the broken server is an admission failure.
	owner.pluginGeneration.requiredPlugins[plugin.ID] = true
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	thread, err := owner.NewThreadRuntime("failure-thread")
	require.Error(t, err)
	require.Nil(t, thread)
	require.Contains(t, err.Error(), "start required MCP server")
	require.Nil(t, owner.Toolkit, "failure must not publish an incomplete toolkit")
}

func TestNativeThreadAdmissionRejectsInvalidRemoteSelection(t *testing.T) {
	for _, mode := range []string{"invalid", "unavailable"} {
		t.Run(mode, func(t *testing.T) { testNativeThreadAdmissionRejectsInvalidRemoteSelection(t, mode) })
	}
}

func testNativeThreadAdmissionRejectsInvalidRemoteSelection(t *testing.T, mode string) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "admit")
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("local-only"), 0600))
	plugin := testRuntimePlugin("remote-admission-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	stateDir, err := owner.executionStateDir()
	require.NoError(t, err)
	artifactDir := statepath.SessionArtifactDir(stateDir, "remote-thread")
	require.NoError(t, os.MkdirAll(artifactDir, 0700))
	selection := `{"name":"remote"}`
	if mode == "unavailable" {
		selection = `{"name":"remote","profile":{"backend":"command","command":["/definitely/not/a/transport"],"workspace":"/workspace"}}`
	}
	require.NoError(t, os.WriteFile(filepath.Join(artifactDir, "execution-environment.json"), []byte(selection), 0600))
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	thread, err := owner.NewThreadRuntime("remote-thread")
	if mode == "invalid" {
		require.ErrorContains(t, err, "saved execution environment has no profile")
		require.Nil(t, thread)
	} else {
		require.NoError(t, err)
		defer func() {
			if c := thread.NativeAgentControl(); c != nil {
				c.BeginShutdown()
				c.StopAll()
				_ = c.WaitOwnedWorkerExecutions(context.Background())
				c.Close()
			}
			_ = thread.CloseNativeToolScope(context.Background())
			_ = thread.CloseArtifacts()
			owner.ReleasePluginGeneration(thread.PluginGeneration)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "remote-thread", ExecutionID: "turn"})
		result, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		require.Error(t, err)
		require.NotContains(t, result, "local-only")
	}
	data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
	require.NoError(t, err)
	require.Equal(t, "local-only", string(data))
}
