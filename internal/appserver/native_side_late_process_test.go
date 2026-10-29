package appserver

import (
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
	"github.com/blueberrycongee/wuu/internal/sidethread"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestSideThreadLateNativeProductTurn(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/lazy-native-admission-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample.txt"), []byte("product side evidence"), 0600))
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
	plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "late-side-tools", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: node, Args: []string{"lazy-native.js", "thread-admission", marker}}}, Root: fixture, Official: true}
	side, err := pluginpkg.LoadManifestWithOptions("../plugin/bundled/side/plugin.json", pluginpkg.LoadOptions{Source: "bundled", Official: true})
	require.NoError(t, err)
	plugins := []pluginpkg.Plugin{plugin, side}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true, side.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	out := &lockedBuffer{}
	server := New(owner, out)
	defer func() { server.Close(); _, _ = owner.Cleanup() }()
	createSideThreadMain(t, server, "side-parent")
	_, err = server.sendSideThreadMessage(testRuntimeState(t, owner), "side-parent", "read the file")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		side, err := sideStoreForTest(t, server).Load("side-parent")
		return err == nil && side.Status == sidethread.StatusCompleted
	}, 15*time.Second, 10*time.Millisecond, "output=%s", out.String())
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not close after the side turn")
	}
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("first request missing")
	}
	select {
	case messages := <-requests:
		var content strings.Builder
		for _, message := range messages {
			content.WriteString(message.Content)
		}
		require.Contains(t, content.String(), "product side evidence")
	case <-time.After(time.Second):
		t.Fatal("second request missing")
	}
}
