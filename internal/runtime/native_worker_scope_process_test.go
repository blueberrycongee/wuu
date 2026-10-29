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
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

// A real worker reuses its executor for follow-up turns. Retiring the parent
// must also revoke the native scope carried by this existing worker.
func TestWorkerNativeScopeRetirementProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/native-worker-scope-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("worker-native-secret"), 0600))
	requests := make(chan []providers.ChatMessage, 8)
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		requests <- body.Messages
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 || n == 3 {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"worker-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"sample.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"worker done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
		}
	}))
	defer model.Close()
	plugin := testRuntimePlugin("worker-scope-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "delegate-all"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("worker-scope")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	require.NotNil(t, thread.AgentControl)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spawned, err := thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "scope", Description: "check worker scope", Prompt: "read the file", Synchronous: true})
	require.NoError(t, err)
	require.Empty(t, spawned.Error)
	nextRequest := func() []providers.ChatMessage {
		t.Helper()
		select {
		case messages := <-requests:
			return messages
		case <-ctx.Done():
			t.Fatalf("model request did not arrive: %v; spawn result=%+v", ctx.Err(), spawned)
			return nil
		}
	}
	nextRequest()
	first := nextRequest()
	var firstText string
	for _, m := range first {
		firstText += m.Content
	}
	require.Contains(t, firstText, "worker-native-secret")
	require.NoError(t, thread.CloseNativeToolScope(ctx))
	followup, err := thread.AgentControl.FollowupTask(ctx, spawned.AgentID, "read again")
	require.NoError(t, err)
	final, err := thread.AgentControl.Wait(ctx, spawned.AgentID)
	require.NoError(t, err)
	require.Equal(t, "running", string(followup.Status))
	require.Equal(t, "failed", string(final.Status))
	require.NotNil(t, final.Error)
	require.True(t, strings.Contains(final.Error.Error(), "scope is closed"), "unexpected worker error: %v", final.Error)
}
