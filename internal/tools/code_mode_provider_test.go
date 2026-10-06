package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

// Large catalogs must not inflate the first request on any supported wire API.
func TestCodeModeLargeCatalogCanStreamAcrossProviders(t *testing.T) {
	for _, tc := range []struct{ name, model, wire string }{
		{"grok", "grok-4.6", "chat"},
		{"openai", "gpt-6-sol", "responses"},
		{"anthropic", "claude-sonnet-4-5", "messages"},
		{"kimi", "kimi-k3", "chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kit := newCodeModeTestToolkit(t)
			kit.ConfigureSurfaceForProviderModel(tc.name, tc.model, true)
			kit.SetContextWindowToolsEnabled(true)
			kit.SetCodeModeAdditionalTools(func() []providers.ToolDefinition {
				var defs []providers.ToolDefinition
				for i := 0; i < 3; i++ {
					defs = append(defs, providers.ToolDefinition{
						Name: fmt.Sprintf("extension_%d", i), Description: strings.Repeat("Search indexed documents. ", 300),
						InputSchema: map[string]any{"type": "object", "properties": map[string]any{"extension_query": map[string]any{"type": "string"}}},
					})
				}
				return defs
			})
			nested, err := kit.CodeModeNestedSurface()
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := json.Marshal(nested)
			if err != nil || len(catalog) <= 16*1024 {
				t.Fatalf("fixture does not reproduce oversized catalog: %d, %v", len(catalog), err)
			}
			target := providers.ToolSurfaceValidationTarget{ProviderName: tc.name, Model: tc.model}
			if err := kit.ValidateActiveToolSurfaceForProvider(target); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Tools []json.RawMessage `json:"tools"`
				}
				payload, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					t.Error(readErr)
					return
				}
				if bytes.Contains(payload, []byte("stale_catalog_entry")) || bytes.Contains(payload, []byte(`"direct_only"`)) || bytes.Contains(payload, []byte(`"DirectOnly"`)) {
					t.Error("stale schema or host routing metadata leaked to provider")
				}
				if err := json.Unmarshal(payload, &req); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				if len(req.Tools) == 0 {
					t.Error("adapter dropped tools")
				}
				var execDescription string
				direct := map[string]bool{}
				for _, raw := range req.Tools {
					var tool struct {
						Name        string `json:"name"`
						Description string `json:"description"`
						Function    *struct {
							Name        string `json:"name"`
							Description string `json:"description"`
						} `json:"function"`
					}
					if err := json.Unmarshal(raw, &tool); err != nil {
						t.Error(err)
						continue
					}
					if tool.Function != nil {
						tool.Name, tool.Description = tool.Function.Name, tool.Function.Description
					}
					direct[tool.Name] = true
					if tool.Name != "run_code" && tool.Name != "new_context" && tool.Name != "set_session_workspace" {
						t.Errorf("ordinary tool leaked at top level: %s", tool.Name)
					}
					if tool.Name == "run_code" {
						execDescription = tool.Description
					}
				}
				if !direct["run_code"] || !direct["new_context"] || !direct["set_session_workspace"] {
					t.Errorf("direct control missing: %v", direct)
				}
				t.Logf("catalog metadata=%d bytes; initial entry=%d bytes", len(catalog), len(execDescription))
				if len(execDescription) == 0 || len(execDescription) > 24*1024 {
					t.Errorf("unbounded entry description: %d bytes", len(execDescription))
				}
				if strings.Contains(execDescription, `"extension_query"`) || strings.Contains(execDescription, strings.Repeat("Search indexed documents. ", 10)) {
					t.Error("deferred extension metadata leaked into the initial request")
				}

				w.Header().Set("Content-Type", "text/event-stream")
				switch tc.wire {
				case "chat":
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				case "responses":
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\",\"item_id\":\"msg_1\",\"output_index\":0}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n")
				case "messages":
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}
			}))
			defer server.Close()
			var client providers.StreamClient
			if tc.wire == "messages" {
				client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "test"})
			} else {
				client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: tc.wire})
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events, err := client.StreamChat(ctx, providers.ChatRequest{Model: tc.model, NativeDeferredToolDiscovery: true, Messages: []providers.ChatMessage{{Role: "system", Content: "Recovered context", DiscoveredTools: []providers.LoadableToolDefinition{{Name: "stale_catalog_entry", InputSchema: map[string]any{"type": "object"}}}}, {Role: "user", Content: "Reply ok"}}, Tools: kit.Definitions()})
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for event := range events {
				if event.Error != nil {
					t.Fatal(event.Error)
				}
				if event.Type == providers.EventContentDelta {
					output.WriteString(event.Content)
				}
			}
			if output.String() != "ok" {
				t.Fatalf("reply=%q", output.String())
			}
		})
	}
}
