package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestNativeGenerationRevokesPluginMCPBeforeAndAfterAdmission(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, mode := range []string{"before", "after", "racing"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(t.TempDir(), "admit")
			pluginLog := filepath.Join(root, "plugin.log")
			userLog := filepath.Join(root, "user.log")
			provider := testRuntimePlugin("selected-tools")
			provider.Root = fixture
			provider.Runtime.Command = node
			provider.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
			barrier := t.TempDir()
			args := []string{filepath.Join(fixture, "mcp-singleton.js"), pluginLog}
			if mode == "racing" {
				args = append(args, barrier)
				require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
			}
			extension := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "mcp-extension", ActivityKinds: []string{"browser"}, MCPServers: map[string]config.MCPServerConfig{"svc": {Command: node, Args: args}}}, Root: fixture, Official: true}
			plugins := []pluginpkg.Plugin{provider, extension}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, MCPServers: map[string]config.MCPServerConfig{"user": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), userLog}}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{provider.ID: true, extension.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: provider.ID}, {Consumer: provider.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			require.Nil(t, owner.Toolkit)
			g := owner.pluginGeneration
			active := make(map[string]bool)
			for _, id := range g.moduleIDs() {
				active[id] = true
			}
			require.True(t, active[extension.ID])
			delete(active, extension.ID)
			revoke := func() { g.revokeMissingPlugins(active, owner.StreamRunner.Client, owner.Model) }
			if mode == "before" {
				revoke()
			}
			if mode == "racing" {
				g.requiredPlugins[extension.ID] = true
			}
			require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
			var thread *ThreadRuntime
			if mode == "racing" {
				type admission struct {
					thread *ThreadRuntime
					err    error
				}
				done := make(chan admission, 1)
				go func() { thread, err := owner.NewThreadRuntime("mcp-revoke"); done <- admission{thread, err} }()
				require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(barrier, "entered")); return err == nil }, 5*time.Second, 10*time.Millisecond)
				revoked := make(chan struct{})
				go func() { revoke(); close(revoked) }()
				require.Eventually(t, func() bool { g.revokedMu.Lock(); defer g.revokedMu.Unlock(); return g.revokedPlugins[extension.ID] }, 5*time.Second, 10*time.Millisecond)
				require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
				select {
				case result := <-done:
					require.NoError(t, result.err)
					thread = result.thread
				case <-time.After(10 * time.Second):
					t.Fatal("admission did not settle")
				}
				select {
				case <-revoked:
				case <-time.After(10 * time.Second):
					t.Fatal("revocation did not settle")
				}
			} else {
				thread, err = owner.NewThreadRuntime("mcp-revoke")
				require.NoError(t, err)
			}
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
			mgr := thread.NativeToolkit().MCPManager()
			require.NotNil(t, mgr)
			require.Eventually(t, func() bool { return mgr.Status()["user"].Connected }, 5*time.Second, 10*time.Millisecond)
			name := PluginMCPServerName(extension.ID, "svc")
			if mode == "racing" {
				require.Eventually(t, func() bool {
					data, err := os.ReadFile(pluginLog)
					return err == nil && strings.Contains(string(data), "stop ")
				}, 5*time.Second, 10*time.Millisecond)
				require.Error(t, mgr.Connect(context.Background(), name))
			} else if mode == "before" {
				_, err := os.Stat(pluginLog)
				require.True(t, os.IsNotExist(err), "disabled plugin server must not start")
				require.Error(t, mgr.Connect(context.Background(), name))
			} else {
				require.Eventually(t, func() bool { return mgr.Status()[name].Connected }, 5*time.Second, 10*time.Millisecond)
				var connectionID string
				for _, tool := range mgr.NativeTools() {
					if tool.Client.Name() == name {
						connectionID = tool.Client.ConnectionID()
					}
				}
				require.NotEmpty(t, connectionID)
				_, connectionLease, err := owner.ActivityRegistry.Start(activity.StartOptions{ThreadID: "connection-activity", Workdir: root, PluginID: extension.ID, GenerationID: g.id, ConnectionID: connectionID, Kind: activity.KindBrowser})
				require.NoError(t, err)
				connectionCtx, stopConnection, err := owner.ActivityRegistry.BindControl(context.Background(), connectionLease)
				require.NoError(t, err)
				defer stopConnection()
				require.NoError(t, mgr.Disconnect(name))
				select {
				case <-connectionCtx.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("disconnect did not revoke connection activity")
				}
				require.NoError(t, mgr.Connect(context.Background(), name))
				connectionID = ""
				for _, tool := range mgr.NativeTools() {
					if tool.Client.Name() == name {
						connectionID = tool.Client.ConnectionID()
					}
				}
				require.NotEmpty(t, connectionID)
				_, lease, err := owner.ActivityRegistry.Start(activity.StartOptions{ThreadID: "activity", Workdir: root, PluginID: extension.ID, GenerationID: g.id, ConnectionID: connectionID, Kind: activity.KindBrowser})
				require.NoError(t, err)
				activityCtx, cancel, err := owner.ActivityRegistry.BindControl(context.Background(), lease)
				require.NoError(t, err)
				defer cancel()
				revoke()
				require.Eventually(t, func() bool {
					data, err := os.ReadFile(pluginLog)
					return err == nil && strings.Count(string(data), "stop ") >= 2
				}, 5*time.Second, 10*time.Millisecond)
				require.Error(t, mgr.Connect(context.Background(), name))
				select {
				case <-activityCtx.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("revoked activity still live")
				}
			}
			require.True(t, mgr.Status()["user"].Connected, "unrelated user server must remain connected")
		})
	}
}

func TestBackgroundNativePreflightRetiresAfterFactoryFinishes(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "admit")
	barrier := t.TempDir()
	log := filepath.Join(root, "preflight-mcp.log")
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
	provider := testRuntimePlugin("background-tools")
	provider.Root = fixture
	provider.Runtime.Command = node
	provider.Runtime.Args = []string{"lazy-native.js", "background-admission", marker, barrier}
	provider.MCPServers = map[string]config.MCPServerConfig{"svc": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), log, barrier}}}
	plugins := []pluginpkg.Plugin{provider}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{provider.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: provider.ID}, {Consumer: provider.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.Nil(t, owner.Toolkit)
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	done := make(chan error, 1)
	go func() {
		candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins, provider.ID)
		if err == nil {
			err = owner.ActivatePluginGeneration(candidate, nil)
		}
		done <- err
	}()
	deadline := time.After(7 * time.Second)
waitEntered:
	for {
		if _, err := os.Stat(filepath.Join(barrier, "entered")); err == nil {
			break waitEntered
		}
		select {
		case err := <-done:
			t.Fatalf("preflight exited before background factory entered: %v", err)
		case <-deadline:
			t.Fatal("background factory did not enter")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case err := <-done:
		require.ErrorContains(t, err, "initialization did not finish during workspace admission")
	case <-time.After(10 * time.Second):
		t.Fatal("preflight did not fail while background initialization was held")
	}
	_, _ = owner.Cleanup()
	require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(log)
		return err == nil && strings.Contains(string(data), "stop ")
	}, 10*time.Second, 10*time.Millisecond, "preflight MCP must exit after its factory drains")
}

func TestBackgroundNativeThreadAdmissionFailsWithoutWaiting(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, kind := range []string{"thread", "side"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(t.TempDir(), "admit")
			barrier := t.TempDir()
			log := filepath.Join(root, "mcp.log")
			require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
			provider := testRuntimePlugin("background-tools")
			provider.Root = fixture
			provider.Runtime.Command = node
			provider.Runtime.Args = []string{"lazy-native.js", "background-admission", marker, barrier}
			provider.MCPServers = map[string]config.MCPServerConfig{"svc": {Command: node, Args: []string{filepath.Join(fixture, "mcp-singleton.js"), log, barrier}}}
			plugins := []pluginpkg.Plugin{provider}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{provider.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: provider.ID}, {Consumer: provider.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			require.Nil(t, owner.Toolkit)
			owner.pluginGeneration.requiredPlugins[provider.ID] = true
			require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
			done := make(chan error, 1)
			go func() {
				if kind == "thread" {
					_, err := owner.NewThreadRuntime("background-thread")
					done <- err
				} else {
					_, err := owner.NewSideThreadRunner("background-side", root, ThreadModelSelection{}, "side prompt", "background-thread")
					done <- err
				}
			}()
			defer os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600)
			select {
			case err := <-done:
				require.ErrorContains(t, err, "initialization did not finish during "+map[string]string{"thread": "thread", "side": "side thread"}[kind]+" admission")
			case <-time.After(8 * time.Second):
				t.Fatal("admission blocked on background native initialization")
			}
			require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
			_, _ = owner.Cleanup()
			require.Eventually(t, func() bool {
				data, err := os.ReadFile(log)
				return err == nil && strings.Contains(string(data), "stop ")
			}, 10*time.Second, 10*time.Millisecond)
		})
	}
}
