package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestLazyNativeBackendFailsClosedProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/service-continuation-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("restricted"), 0600))
	plugin := testRuntimePlugin("lazy-native-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("lazy-check")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	require.Nil(t, thread.Toolkit)
	ctx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "lazy-check", ExecutionID: "turn"})
	custom, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "custom", Name: "fixture_custom", Arguments: `{}`})
	require.NoError(t, err)
	require.Contains(t, custom, "external provider works")
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "read_only"}, false, nil)
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.ErrorContains(t, err, "native tool backend was not initialized during thread admission")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "write", Name: "write_file", Arguments: `{"path":"sample.txt","content":"modified"}`})
			if err == nil || !strings.Contains(err.Error(), "native tool backend was not initialized during thread admission") {
				t.Errorf("late native call: %v", err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
	require.NoError(t, err)
	require.Equal(t, "restricted", string(data))
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "unconfined", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.ErrorContains(t, err, "native tool backend was not initialized during thread admission")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = thread.StreamRunner.Tools.Execute(canceled, providers.ToolCall{ID: "canceled", Name: "read_file", Arguments: `{"path":"sample.txt"}`})
	require.Error(t, err)
}
