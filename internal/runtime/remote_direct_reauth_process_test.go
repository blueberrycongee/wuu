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
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

// A direct replacement must recheck authority after the selected provider has
// finished transforming the already-authorized worker read.
func TestRemoteDirectReadRechecksAuthorizationAfterContinuation(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	kit, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "after-pause.txt"), []byte("sensitive content\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "error-after-pause.txt"), []byte("sensitive content\n"), 0600))
	entered, release := make(chan struct{}, 1), make(chan struct{})
	barrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte("ok"))
	}))
	defer barrier.Close()
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(barrier.URL), 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("direct-reauth", ""))
	thread, err := owner.NewThreadRuntime("direct-reauth")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("read_only")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "direct-reauth", ExecutionID: "turn"})
	var revoked atomic.Bool
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, _ providers.ToolCall) error {
		if info.Name == "read_file" && revoked.Load() {
			return fmt.Errorf("permission revoked before direct result")
		}
		return nil
	})
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	type outcome struct {
		result toolresult.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"after-pause.txt"}`})
		done <- outcome{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("plugin did not complete native read")
	}
	revoked.Store(true)
	close(release)
	select {
	case got := <-done:
		require.ErrorContains(t, got.err, "permission revoked before direct result")
		require.NotContains(t, got.result.TextProjection(), "sensitive content")
	case <-time.After(10 * time.Second):
		t.Fatal("direct read did not finish")
	}
	// A provider can return source content inside its error result too.
	revoked.Store(false)
	errorEntered, errorRelease := make(chan struct{}, 1), make(chan struct{})
	errorBarrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		errorEntered <- struct{}{}
		<-errorRelease
		_, _ = w.Write([]byte("ok"))
	}))
	defer errorBarrier.Close()
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(errorBarrier.URL), 0600))
	go func() {
		result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "error", Name: "read_file", Arguments: `{"path":"error-after-pause.txt"}`})
		done <- outcome{result, err}
	}()
	select {
	case <-errorEntered:
	case <-time.After(10 * time.Second):
		close(errorRelease)
		t.Fatal("plugin did not prepare error result")
	}
	revoked.Store(true)
	close(errorRelease)
	select {
	case got := <-done:
		require.ErrorContains(t, got.err, "permission revoked before direct result")
		require.NotContains(t, got.result.TextProjection(), "sensitive content")
	case <-time.After(10 * time.Second):
		t.Fatal("direct error did not finish")
	}
}
