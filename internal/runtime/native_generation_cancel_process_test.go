package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

// The dependency factory is shared by thread scopes in one generation. A
// caller leaving the wait cannot cancel a second owner or leak generation work.
func TestNativeGenerationInitializationCancellationProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, mode := range []string{"other-owner", "generation-close"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(t.TempDir(), "admit")
			barrier := t.TempDir()
			log := filepath.Join(root, "mcp.log")
			require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
			plugin := testRuntimePlugin("cancel-init-tools")
			plugin.Root = fixture
			plugin.Runtime.Command = node
			plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
			plugin.MCPServers = map[string]config.MCPServerConfig{"svc": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), log, barrier}}}
			plugins := []pluginpkg.Plugin{plugin}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			generation := owner.pluginGeneration
			generation.requiredPlugins[plugin.ID] = true
			defer os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := make(chan error, 1)
			go func() {
				_, cleanup, err := owner.newThreadNativeToolkit(ctx, generation, root)
				if cleanup != nil {
					_ = cleanup(context.Background())
				}
				first <- err
			}()
			require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(barrier, "entered")); return err == nil }, 5*time.Second, 10*time.Millisecond)
			second := make(chan error, 1)
			if mode == "other-owner" {
				go func() {
					_, cleanup, err := owner.newThreadNativeToolkit(context.Background(), generation, root)
					if cleanup != nil {
						_ = cleanup(context.Background())
					}
					second <- err
				}()
			}
			cancel()
			select {
			case err := <-first:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(2 * time.Second):
				t.Fatal("caller did not cancel")
			}
			if mode == "other-owner" {
				require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
				select {
				case err := <-second:
					require.NoError(t, err)
				case <-time.After(10 * time.Second):
					t.Fatal("other owner did not finish")
				}
				data, err := os.ReadFile(log)
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(string(data), "start "))
			} else {
				done := make(chan error, 1)
				go func() { _, err := owner.Cleanup(); done <- err }()
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-time.After(8 * time.Second):
					t.Fatal("generation did not cancel and retire its initialization")
				}
			}
		})
	}
}
