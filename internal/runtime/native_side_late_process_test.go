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
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestSideThreadLateNativeDelegationProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("side evidence"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	plugin := testRuntimePlugin("late-side-tools")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.Nil(t, owner.Toolkit)
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	// The convenience constructor has no retained state lease, so it cannot
	// safely initialize the native backend after returning.
	unbound, err := owner.NewSideThreadRunner("unbound-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer CloseSideThreadTools(context.Background(), unbound)
	runner, release, err := owner.NewSideThreadRunnerForState(state, "late-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() {
		_ = CloseSideThreadTools(context.Background(), runner)
		if release != nil {
			_ = release()
		}
	}()
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	parent, err := owner.NewThreadRuntime("parent")
	require.NoError(t, err)
	defer func() {
		if control := parent.NativeAgentControl(); control != nil {
			control.BeginShutdown()
			control.StopAll()
			_ = control.WaitOwnedWorkerExecutions(context.Background())
			control.Close()
		}
		_ = parent.CloseNativeToolScope(context.Background())
		_ = parent.CloseArtifacts()
		owner.ReleasePluginGeneration(parent.PluginGeneration)
	}()
	archived := parent.NativeToolkit().FinalizeToolResult(providers.ToolCall{ID: "archive", Name: "synthetic"}, toolresult.FromText(strings.Repeat("late-side-evidence", 10000)))
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(archived.TextProjection()), &envelope))
	cursor, err := json.Marshal(envelope["continuation"].(map[string]any)["next"])
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "late-side", ExecutionID: "side-turn"})
	unboundCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "unbound-side", ExecutionID: "side-turn"})
	_, err = unbound.Tools.Execute(unboundCtx, providers.ToolCall{ID: "unbound-read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.ErrorContains(t, err, "native tool backend was not initialized during side thread admission")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			output, err := runner.Tools.Execute(ctx, providers.ToolCall{ID: fmt.Sprintf("read-%d", i), Name: "read_file", Arguments: `{"path":"sample.txt"}`})
			if err != nil || !containsSideEvidence(output) {
				t.Errorf("read %d: %q, %v", i, output, err)
			}
		}(i)
	}
	wg.Wait()
	require.NoError(t, parent.CloseArtifacts())
	page, err := runner.Tools.Execute(ctx, providers.ToolCall{ID: "recover", Name: "recover_tool_result", Arguments: string(cursor)})
	require.NoError(t, err)
	require.Contains(t, page, "late-side-evidence")
	_, err = runner.Tools.Execute(ctx, providers.ToolCall{ID: "write", Name: "write_file", Arguments: `{"path":"sample.txt","content":"forbidden"}`})
	require.Error(t, err)
	data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
	require.NoError(t, err)
	require.Equal(t, "side evidence", string(data))
	require.NoError(t, CloseSideThreadTools(context.Background(), runner))
	if release != nil {
		require.NoError(t, release())
	}
	_, err = runner.Tools.Execute(ctx, providers.ToolCall{ID: "after-close", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.Error(t, err)

}

func containsSideEvidence(s string) bool { return strings.Contains(s, "side evidence") }

func TestSideThreadLateNativeInitializationDrainsOnClose(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("during close"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	barrier := t.TempDir()
	log := filepath.Join(root, "mcp.log")
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
	plugin := testRuntimePlugin("late-side-tools")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugin.MCPServers = map[string]config.MCPServerConfig{"svc": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), log, barrier}}}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	owner.pluginGeneration.requiredPlugins[plugin.ID] = true
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	runner, release, err := owner.NewSideThreadRunnerForState(state, "late-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() {
		_ = CloseSideThreadTools(context.Background(), runner)
		if release != nil {
			_ = release()
		}
	}()
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	defer os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "late-side", ExecutionID: "side-turn"})
	done := make(chan error, 1)
	go func() {
		_, err := runner.Tools.Execute(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		done <- err
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(barrier, "entered")); return err == nil }, 5*time.Second, 10*time.Millisecond)
	closeCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	err = CloseSideThreadTools(closeCtx, runner)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case err := <-done:
		t.Fatalf("in-flight call settled before initialization resumed: %v", err)
	default:
	}
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("in-flight call did not settle")
	}
	require.NoError(t, CloseSideThreadTools(context.Background(), runner))
	require.NoError(t, release())
}

func TestSideThreadLateNativeDuringModelTurn(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("model side evidence"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	requests := make(chan []providers.ChatMessage, 2)
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		requests <- input.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
				http.Error(w, "marker", 500)
				return
			}
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"side-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"sample.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"side done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
		}
	}))
	defer model.Close()
	plugin := testRuntimePlugin("late-side-tools")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	runner, release, err := owner.NewSideThreadRunnerForState(state, "model-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() {
		_ = CloseSideThreadTools(context.Background(), runner)
		_ = release()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "model-side", ExecutionID: "model-turn"})
	_, err = runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "read the file"}}, nil)
	require.NoError(t, err)
	<-requests
	select {
	case messages := <-requests:
		var content strings.Builder
		for _, message := range messages {
			content.WriteString(message.Content)
		}
		require.Contains(t, content.String(), "model side evidence")
	case <-ctx.Done():
		t.Fatal("second model request missing")
	}
}

func TestSideThreadLateNativeProviderRevocation(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("side evidence"), 0600))
			marker := filepath.Join(t.TempDir(), "admit")
			plugin := testRuntimePlugin("late-side-tools")
			plugin.Root = fixture
			plugin.Runtime.Command = node
			plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
			plugins := []pluginpkg.Plugin{plugin}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			state, err := owner.RetainState()
			require.NoError(t, err)
			defer state.Close()
			runner, release, err := owner.NewSideThreadRunnerForState(state, "revoked-side", root, ThreadModelSelection{}, "side prompt", "parent")
			require.NoError(t, err)
			defer func() { _ = CloseSideThreadTools(context.Background(), runner); _ = release() }()
			require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "revoked-side", ExecutionID: "side-turn"})
			if mode == "after" {
				output, err := runner.Tools.Execute(ctx, providers.ToolCall{ID: "before", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
				require.NoError(t, err)
				require.Contains(t, output, "side evidence")
			}
			generation := owner.pluginGeneration
			active := make(map[string]bool)
			for _, id := range generation.moduleIDs() {
				active[id] = true
			}
			delete(active, plugin.ID)
			generation.revokeMissingPlugins(active, owner.StreamRunner.Client, owner.Model)
			_, err = runner.Tools.Execute(ctx, providers.ToolCall{ID: "after", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
			require.Error(t, err)
			if mode == "before" {
				generation.nativeDependencies.mu.Lock()
				initialized := generation.nativeDependencies.initialized
				generation.nativeDependencies.mu.Unlock()
				require.False(t, initialized, "revoked provider must not initialize native dependencies")
			}
		})
	}
}

func TestSideThreadLateNativeInitializationFailsClosed(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("unchanged"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	plugin := testRuntimePlugin("late-side-tools")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugin.MCPServers = map[string]config.MCPServerConfig{"broken": {Command: filepath.Join(root, "missing-mcp")}}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	owner.pluginGeneration.requiredPlugins[plugin.ID] = true
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	runner, release, err := owner.NewSideThreadRunnerForState(state, "failure-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() { _ = CloseSideThreadTools(context.Background(), runner); _ = release() }()
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "failure-side", ExecutionID: "side-turn"})
	for i := 0; i < 2; i++ {
		_, err := runner.Tools.Execute(ctx, providers.ToolCall{ID: fmt.Sprintf("read-%d", i), Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		require.Error(t, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
	require.NoError(t, err)
	require.Equal(t, "unchanged", string(data))
}

func TestSideThreadLateNativeInitializationHonorsCancellation(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("during close"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	barrier := t.TempDir()
	log := filepath.Join(root, "mcp.log")
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
	plugin := testRuntimePlugin("late-side-tools")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugin.MCPServers = map[string]config.MCPServerConfig{"svc": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), log, barrier}}}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	owner.pluginGeneration.requiredPlugins[plugin.ID] = true
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	runner, release, err := owner.NewSideThreadRunnerForState(state, "late-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() {
		_ = CloseSideThreadTools(context.Background(), runner)
		if release != nil {
			_ = release()
		}
	}()
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	defer os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "late-side", ExecutionID: "side-turn"})
	done := make(chan error, 1)
	go func() {
		_, err := runner.Tools.Execute(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		done <- err
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(barrier, "entered")); return err == nil }, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled side call did not finish while native initialization was blocked")
	}
	closeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	require.NoError(t, CloseSideThreadTools(closeCtx, runner), "cancellation must drain the native scope before the MCP server resumes")

	require.NoError(t, CloseSideThreadTools(context.Background(), runner))
	require.NoError(t, release())
	require.NoError(t, state.Close())
	cleaned := make(chan error, 1)
	go func() { _, err := owner.Cleanup(); cleaned <- err }()
	select {
	case err := <-cleaned:
		require.NoError(t, err)
	case <-time.After(8 * time.Second):
		buf := make([]byte, 1<<20)
		n := goruntime.Stack(buf, true)
		t.Fatalf("generation close did not cancel shared initialization:\n%s", buf[:n])
	}
}

func TestSideThreadLateNativeCancellationDoesNotCancelOtherOwner(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("other owner"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	barrier := t.TempDir()
	log := filepath.Join(root, "mcp.log")
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
	plugin := testRuntimePlugin("late-side-tools")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugin.MCPServers = map[string]config.MCPServerConfig{"svc": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), log, barrier}}}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	owner.pluginGeneration.requiredPlugins[plugin.ID] = true
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	one, releaseOne, err := owner.NewSideThreadRunnerForState(state, "side-one", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() { _ = CloseSideThreadTools(context.Background(), one); _ = releaseOne() }()
	two, releaseTwo, err := owner.NewSideThreadRunnerForState(state, "side-two", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	defer func() { _ = CloseSideThreadTools(context.Background(), two); _ = releaseTwo() }()
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	defer os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx1, cancel1 := context.WithCancel(ctx)
	ctx1 = loopdriver.WithExecutionContext(ctx1, loopdriver.ExecutionContext{SessionID: "side-one", ExecutionID: "turn-one"})
	ctx2 := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "side-two", ExecutionID: "turn-two"})
	first := make(chan error, 1)
	go func() {
		_, err := one.Tools.Execute(ctx1, providers.ToolCall{ID: "one", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		first <- err
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(barrier, "entered")); return err == nil }, 5*time.Second, 10*time.Millisecond)
	type outcome struct {
		output string
		err    error
	}
	second := make(chan outcome, 1)
	go func() {
		output, err := two.Tools.Execute(ctx2, providers.ToolCall{ID: "two", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		second <- outcome{output, err}
	}()
	cancel1()
	select {
	case err := <-first:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("first caller did not cancel")
	}
	closeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	require.NoError(t, CloseSideThreadTools(closeCtx, one))
	stop()
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
	select {
	case result := <-second:
		require.NoError(t, result.err)
		require.Contains(t, result.output, "other owner")
	case <-ctx.Done():
		t.Fatal("other owner did not finish")
	}
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(data), "start "), "shared initialization must start the server once")
}
