package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/stretchr/testify/require"
)

func TestReadFileImageProviderHTTP(t *testing.T) {
	for _, wire := range []string{"chat", "responses", "anthropic"} {
		for _, codeMode := range []bool{false, true} {
			mode := "direct"
			if codeMode {
				mode = "code"
			}
			t.Run(wire+"/"+mode, func(t *testing.T) {
				root := t.TempDir()
				data := readImagePNG(t, 16, 8)
				encoded := base64.StdEncoding.EncodeToString(data)
				require.NoError(t, os.WriteFile(filepath.Join(root, "screen.png"), data, 0600))
				kit, err := New(root)
				require.NoError(t, err)
				call := providers.ToolCall{ID: "call_read", Name: "read_file", Arguments: `{"path":"screen.png"}`}
				result, err := kit.ExecuteResult(context.Background(), call)
				require.NoError(t, err)
				if codeMode {
					part := result.Content[1]
					result = codeModeResponseResult(codemode.Response{State: "Result", CellID: "cell", Content: []codemode.ContentItem{
						{Type: "input_image", ImageURL: "data:" + part.MIMEType + ";base64," + part.Data},
					}})
					call.Name, call.Arguments = "exec", `{"source":"image(result.content[1])"}`
				}
				bodies := make(chan []byte, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						http.Error(w, "read body", 400)
						return
					}
					select {
					case bodies <- body:
					default:
						t.Error("unexpected retry")
					}
					w.Header().Set("Content-Type", "application/json")
					switch wire {
					case "chat":
						io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
					case "responses":
						io.WriteString(w, `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`)
					case "anthropic":
						io.WriteString(w, `{"id":"msg_1","role":"assistant","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
					}
				}))
				defer server.Close()
				var client interface {
					Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error)
				}
				model := "gpt-5"
				if wire == "anthropic" {
					client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "test", HTTPClient: server.Client()})
					model = "claude-sonnet-4"
				} else {
					client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: wire, HTTPClient: server.Client()})
				}
				require.NoError(t, err)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err = client.Chat(ctx, providers.ChatRequest{Model: model, Messages: []providers.ChatMessage{
					{Role: "user", Content: "Inspect screen.png"},
					{Role: "assistant", ToolCalls: []providers.ToolCall{call}},
					{Role: "tool", ToolCallID: call.ID, Name: call.Name, ToolResult: &result},
				}})
				require.NoError(t, err)
				var request map[string]any
				require.NoError(t, json.Unmarshal(<-bodies, &request))
				images := 0
				var inspect func(any)
				inspect = func(value any) {
					switch value := value.(type) {
					case []any:
						for _, item := range value {
							inspect(item)
						}
					case map[string]any:
						switch value["type"] {
						case "input_image":
							require.Equal(t, "data:image/png;base64,"+encoded, value["image_url"])
							images++
						case "image_url":
							require.Equal(t, "data:image/png;base64,"+encoded, value["image_url"].(map[string]any)["url"])
							images++
						case "image":
							source := value["source"].(map[string]any)
							require.Equal(t, "image/png", source["media_type"])
							require.Equal(t, encoded, source["data"])
							images++
						}
						for key, item := range value {
							if text, ok := item.(string); ok && (key == "text" || key == "output" || key == "content") {
								require.NotContains(t, text, encoded, "image must not be serialized as text")
							}
							inspect(item)
						}
					}
				}
				inspect(request)
				require.Equal(t, 1, images, "provider must receive the actual image exactly once")
			})
		}
	}
}
