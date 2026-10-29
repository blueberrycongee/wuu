package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestThreadNativeScopeRetirementProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("owned session"), 0600))
	plugin := testRuntimePlugin("scope-native-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "delegate-all"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("scope-retirement")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "scope-retirement", ExecutionID: "turn"})
	call := providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`}
	output, err := thread.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, output, "owned session")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
		if call.ID == "held" {
			close(entered)
			<-release
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "held", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("native call did not enter permission hook")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	require.ErrorIs(t, thread.CloseNativeToolScope(canceled), context.Canceled)
	lateDone := make(chan error, 1)
	go func() { _, err := thread.StreamRunner.Tools.Execute(ctx, call); lateDone <- err }()
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, <-done)
	require.Error(t, <-lateDone)
	require.NoError(t, thread.CloseNativeToolScope(context.Background()))
	_, err = thread.StreamRunner.Tools.Execute(ctx, call)
	require.Error(t, err)
	require.NoError(t, thread.CloseNativeToolScope(context.Background()))
}

func TestSideNativeScopeRetirementProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("side session"), 0600))
	plugin := testRuntimePlugin("scope-native-side-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "delegate-all"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	side, err := owner.NewSideThreadRunner("scope-side", root, ThreadModelSelection{}, "side prompt", "parent")
	require.NoError(t, err)
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "scope-side", ExecutionID: "turn"})
	call := providers.ToolCall{ID: "read-side", Name: "read_file", Arguments: `{"path":"sample.txt"}`}
	output, err := side.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, output, "side session")
	require.NoError(t, CloseSideThreadTools(context.Background(), side))
	_, err = side.Tools.Execute(ctx, call)
	require.Error(t, err)
	require.NoError(t, CloseSideThreadTools(context.Background(), side))
}

func TestComposedNativeScopeRetirementProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/native-scope-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("composed session"), 0600))
	plugin := testRuntimePlugin("scope-composition-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "delegate-all"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	scopes := kernel.compositions
	defer scopes.close()
	invoke := func(method, input string) json.RawMessage {
		t.Helper()
		output, err := scopes.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: method, Params: json.RawMessage(input)})
		require.NoError(t, err)
		return output
	}
	var compositionView pluginhost.CompositionView
	require.NoError(t, json.Unmarshal(invoke("create", `{"inherit":true,"state":"parent"}`), &compositionView))
	_, err = owner.Sessions.Create("scope-composed")
	require.NoError(t, err)
	invoke("bind", fmt.Sprintf(`{"session":"scope-composed","id":%q,"expected":""}`, compositionView.ID))
	binding, err := owner.RetainState()
	require.NoError(t, err)
	defer binding.Close()
	thread, err := owner.NewThreadRuntimeForState(binding, "scope-composed", root, ThreadModelSelection{})
	require.NoError(t, err)
	require.NotEmpty(t, thread.CompositionID)
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "scope-composed", ExecutionID: "turn"})
	call := providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`}
	output, err := thread.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, output, "composed session")
	require.NoError(t, thread.CloseComposition())
	_, err = thread.StreamRunner.Tools.Execute(ctx, call)
	require.Error(t, err)
}
