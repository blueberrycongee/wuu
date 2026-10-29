package appserver

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestNativeThreadAdmissionAppserverPTCAndPermissions(t *testing.T) {
	for _, mode := range []string{"concurrent", "serial", "cancel", "migration"} {
		t.Run(mode, func(t *testing.T) { testNativeThreadAdmissionAppserverPTCAndPermissions(t, mode) })
	}
}

func testNativeThreadAdmissionAppserverPTCAndPermissions(t *testing.T, mode string) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("application-evidence"), 0600))
	marker := filepath.Join(t.TempDir(), "admit")
	requests := make(chan []providers.ChatMessage, 8)
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "bad input", 400)
			return
		}
		requests <- input.Messages
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		var response any
		if n == 1 || n == 3 {
			name, args := "run_code", `{"code":"const r=await tools.read_file({path:'sample.txt'}); console.log('PTC_READ_OK',r.content[0].text);","description":"Read through PTC"}`
			if n == 3 {
				name, args = "write_file", `{"path":"sample.txt","content":"forbidden"}`
			}
			response = map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d", n), "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}}
		} else {
			response = map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "turn complete"}, "finish_reason": "stop"}}}
		}
		encoded, _ := json.Marshal(response)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
	}))
	defer model.Close()
	plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "native-appserver-fixture", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"lazy-native.js", "thread-admission", marker, mode}}}, Root: fixture, Official: true}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "fixture"}}, Agent: config.AgentConfig{PermissionMode: "read_only"}, PTC: config.PTCConfig{Enabled: true}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	mcpLog := filepath.Join(root, "mcp-events.log")
	barrier := t.TempDir()
	mcpArgs := []string{filepath.Join(fixture, "mcp-singleton.js"), mcpLog}
	if mode == "migration" {
		mcpArgs = append(mcpArgs, barrier)
	}
	cfg.MCPServers = map[string]config.MCPServerConfig{"shared": {Command: node, Args: mcpArgs}}
	owner, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	require.Nil(t, owner.Toolkit)
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	out := &lockedBuffer{}
	srv := New(owner, out)
	defer func() { srv.Close(); _, _ = owner.Cleanup() }()
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	started := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	entered, settled := make(chan struct{}), make(chan struct{})
	if mode == "cancel" {
		thread, err := srv.ensureThreadRuntime(srv.thread(started.Thread.ID))
		require.NoError(t, err)
		require.NotNil(t, thread.NativeToolkit())
		thread.NativeToolkit().SetPermissionRequestHook(func(ctx context.Context, _ *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
			if call.Name == "read_file" {
				close(entered)
				<-ctx.Done()
				close(settled)
				return ctx.Err()
			}
			return nil
		})
	}
	dispatchPayload(t, srv, "ptc", MethodTurnStart, TurnStartParams{ThreadID: started.Thread.ID, Prompt: "read the file"})
	next := func() []providers.ChatMessage {
		t.Helper()
		select {
		case m := <-requests:
			return m
		case <-time.After(30 * time.Second):
			t.Fatalf("model request did not arrive; appserver output: %s", out.String())
			return nil
		}
	}
	next()
	if mode == "cancel" {
		select {
		case <-entered:
		case <-time.After(15 * time.Second):
			t.Fatal("nested read did not reach permission hook")
		}
		dispatchPayload(t, srv, "interrupt", MethodTurnInterrupt, TurnInterruptParams{ThreadID: started.Thread.ID})
		require.Nil(t, responseByID(t, parseOutput(t, out.String()), "interrupt")["error"])
		select {
		case <-settled:
		case <-time.After(10 * time.Second):
			t.Fatal("nested read did not observe cancellation")
		}
		data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
		require.NoError(t, err)
		require.Equal(t, "application-evidence", string(data))
		return
	}
	ptcResult := next()
	var text strings.Builder
	for _, m := range ptcResult {
		text.WriteString(m.Content)
	}
	if mode == "serial" {
		require.Contains(t, text.String(), "nested tool metadata unavailable")
		require.NotContains(t, text.String(), "PTC_READ_OK")
		waitForTurnCompletedCountForThread(t, out, started.Thread.ID, 1)
		data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
		require.NoError(t, err)
		require.Equal(t, "application-evidence", string(data))
		return
	}
	require.Contains(t, text.String(), "PTC_READ_OK")
	require.Contains(t, text.String(), "application-evidence")
	waitForTurnCompletedCountForThread(t, out, started.Thread.ID, 1)
	mgr, current, release, err := srv.mcpManager()
	require.NoError(t, err)
	require.True(t, current())
	require.Eventually(t, func() bool { return mgr.Status()["shared"].Connected }, 5*time.Second, 10*time.Millisecond)
	release()
	list, err := srv.mcpServerList(context.Background())
	require.NoError(t, err)
	found := false
	for _, server := range list.Servers {
		if server.Name == "shared" {
			found = server.Connected
		}
	}
	require.True(t, found, "MCP list must show the active generation manager")
	dispatchPayload(t, srv, "disconnect", MethodMCPDisconnect, MCPServerActionParams{Name: "shared"})
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "disconnect")["error"])
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(mcpLog)
		return err == nil && strings.Contains(string(data), "stop ")
	}, 5*time.Second, 10*time.Millisecond)
	if mode == "migration" {
		require.NoError(t, os.WriteFile(filepath.Join(barrier, "block"), []byte("wait"), 0600))
		request, err := json.Marshal(map[string]any{"id": "racing-connect", "method": MethodMCPConnect, "params": MCPServerActionParams{Name: "shared"}})
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() { done <- srv.handleLine(context.Background(), request) }()
		require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(barrier, "entered")); return err == nil }, 5*time.Second, 10*time.Millisecond)
		candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins)
		require.NoError(t, err)
		require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
		require.NoError(t, os.WriteFile(filepath.Join(barrier, "resume"), []byte("go"), 0600))
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Fatal("MCP connect did not settle")
		}
		response := responseByID(t, parseOutput(t, out.String()), "racing-connect")
		require.Contains(t, fmt.Sprint(response["error"]), "MCP configuration changed")
		// Hold a real selected configuration read after it returns its view,
		// publish a new generation, then resume the list operation.
		listEntered, listResume := make(chan struct{}), make(chan struct{})
		listDone := make(chan error, 1)
		go func() {
			_, err := srv.mcpServerListWithConfiguration(context.Background(), func(ctx context.Context) (config.SourceView, map[string]config.MCPServerConfig, error) {
				view, definitions, err := srv.mcpConfiguration(ctx)
				close(listEntered)
				<-listResume
				return view, definitions, err
			})
			listDone <- err
		}()
		select {
		case <-listEntered:
		case <-time.After(10 * time.Second):
			t.Fatal("MCP list did not read its configuration")
		}
		candidate, err = owner.PreflightDiscoveredExtensions(cfg, plugins)
		require.NoError(t, err)
		require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
		close(listResume)
		select {
		case err := <-listDone:
			require.ErrorContains(t, err, "MCP configuration changed")
		case <-time.After(10 * time.Second):
			t.Fatal("MCP list did not settle")
		}

		return
	}
	dispatchPayload(t, srv, "reconnect", MethodMCPConnect, MCPServerActionParams{Name: "shared"})
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "reconnect")["error"])
	dispatchPayload(t, srv, "write", MethodTurnStart, TurnStartParams{ThreadID: started.Thread.ID, Prompt: "try writing"})
	next()
	writeResult := next()
	text.Reset()
	for _, m := range writeResult {
		text.WriteString(m.Content)
	}
	require.Contains(t, strings.ToLower(text.String()), "read")
	waitForTurnCompletedCountForThread(t, out, started.Thread.ID, 2)
	data, err := os.ReadFile(filepath.Join(root, "sample.txt"))
	require.NoError(t, err)
	require.Equal(t, "application-evidence", string(data))
}
