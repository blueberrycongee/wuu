package appserver

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestSessionToolPolicyUpdatesSDKProcessAuthority(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/service-continuation-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "policy-process-fixture", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"tools.js", "policy-process"}}}, Root: fixture, Official: true}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}}}}}}
	root := t.TempDir()
	owner, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	_, err = owner.Sessions.CreateInitialized(session.Session{ID: "policy-process", CWD: root, ToolPolicyJSON: `{"allow":["process"]}`}, nil)
	require.NoError(t, err)
	state, err := owner.RetainState()
	require.NoError(t, err)
	thread, err := owner.NewThreadRuntimeForState(state, "policy-process", root, runtime.ThreadModelSelection{})
	require.NoError(t, err)
	require.Nil(t, thread.Toolkit)
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	srv := New(owner, &lockedBuffer{})
	cleanup := sync.OnceFunc(func() {
		srv.Close()
		owner.ReleasePluginGeneration(thread.PluginGeneration)
		_ = state.Close()
		_, _ = owner.Cleanup()
	})
	defer cleanup()
	require.NoError(t, srv.configureSessionToolPolicy(state, "policy-process", thread))
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "read_only"}, false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "policy-process", ExecutionID: "turn"})
	call := func(id string) (string, error) {
		return thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: id, Name: "process", Arguments: `{}`})
	}
	_, err = call("denied")
	require.Error(t, err)
	processes, err := thread.ProcessManager.List()
	require.NoError(t, err)
	require.Empty(t, processes)
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	output, err := call("allowed")
	require.NoError(t, err)
	require.Contains(t, output, "policy-process")
	processes, err = thread.ProcessManager.List()
	require.NoError(t, err)
	require.Len(t, processes, 1)
	// The process-session watcher retains the generation until its real process
	// settles; wait for its durable retirement before removing the test home.
	cleanup()
	require.Eventually(t, func() bool {
		reports, err := owner.PluginGenerationRevocations(0)
		return err == nil && len(reports) > 0
	}, 5*time.Second, 10*time.Millisecond)
}
