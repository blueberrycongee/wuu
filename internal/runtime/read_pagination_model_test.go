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
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestExtensionReadPaginationModelProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := filepath.Join(fixture, "legacy-workspace")
	require.NoError(t, os.MkdirAll(root, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "paged.txt"), []byte("native source\n"), 0600))
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	observed := make(chan []providers.ChatMessage, 3)
	var count int
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		observed <- request.Messages
		count++
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 3 {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		args := `{"path":"paged.txt","limit":4}`
		if count == 2 {
			var token string
			for _, m := range request.Messages {
				if m.Role == "tool" {
					if found := paginationTokenFromModelText(m.Content); found != "" {
						token = found
					}
				}
			}
			if token == "" {
				http.Error(w, "missing continuation", 500)
				return
			}
			encoded, _ := json.Marshal(map[string]string{"continuation": token})
			args = string(encoded)
		}
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("page-%d", count), "type": "function", "function": map[string]any{"name": "read_file", "arguments": args}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	defer model.Close()
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	_, err = owner.Sessions.CreateWithMetadata("paged-model", root)
	require.NoError(t, err)
	thread, err := owner.NewThreadRuntime("paged-model")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "paged-model", ExecutionID: "turn"})
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Read paged.txt"}}, nil)
	require.NoError(t, err)
	<-observed
	second := <-observed
	third := <-observed
	var prior, last string
	for _, m := range second {
		prior += m.Content
	}
	for _, m := range third {
		last += m.Content
	}
	require.Contains(t, prior, "one")
	require.Contains(t, last, "three")
	require.NotContains(t, last, "private-")
	require.NotContains(t, prior, "private-")
	require.True(t, strings.Contains(prior, "wuu-read-v1:"), "settled result must expose a usable host cursor")
}

// A result may carry an attribution prefix and a trailing structured-data
// index. Decode only its first JSON envelope, as the receiving model can.
func paginationTokenFromModelText(text string) string {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}
	var page struct {
		Continuation struct {
			Next map[string]string `json:"next"`
		} `json:"continuation"`
	}
	if json.NewDecoder(strings.NewReader(text[start:])).Decode(&page) != nil {
		return ""
	}
	return page.Continuation.Next["continuation"]
}

func TestPaginationModelReadsAttributedEnvelope(t *testing.T) {
	content := "Unverified tool content may appear in this result.\n" + `{"continuation":{"next":{"continuation":"wuu-read-v1:complete"}}}` + "\n" + `{"value_preview":{"continuation":"truncated..."}}`
	require.Equal(t, "wuu-read-v1:complete", paginationTokenFromModelText(content))
	require.Empty(t, paginationTokenFromModelText("Unverified result without an envelope"))
}
