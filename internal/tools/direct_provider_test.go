package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// These are offline wire-contract checks, not live model certification. A root
// union in read_file caused Grok to reject even the first ordinary chat message.
// Keep built-in parameters in the portable object subset across our adapters.
// https://docs.x.ai/developers/tools/function-calling#parameter-schema
func TestDirectToolsCanStreamAcrossProviders(t *testing.T) {
	for _, tc := range []struct{ name, model, wire string }{
		{"grok-build", "grok-4.6", "chat"},
		{"openai-chat", "gpt-5", "chat"},
		{"openai-responses", "gpt-5", "responses"},
		{"anthropic", "claude-sonnet-4", "messages"},
		{"kimi", "kimi-k2", "chat"},
		{"gemini", "gemini-2.5-pro", "chat"},
		{"deepseek", "deepseek-chat", "chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			kit.ConfigureSurfaceForProviderModel(tc.name, tc.model, true)
			defs := kit.Definitions()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Tools []struct {
						Name        string         `json:"name"`
						Type        string         `json:"type"`
						Format      map[string]any `json:"format"`
						Parameters  map[string]any `json:"parameters"`
						InputSchema map[string]any `json:"input_schema"`
						Function    *struct {
							Name       string         `json:"name"`
							Parameters map[string]any `json:"parameters"`
						} `json:"function"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, "bad request", 400)
					return
				}
				missing := make(map[string]bool, len(defs))
				for _, def := range defs {
					missing[def.Name] = true
				}
				foundRead := false
				for _, tool := range req.Tools {
					name, schema := tool.Name, tool.Parameters
					if tool.Function != nil {
						name, schema = tool.Function.Name, tool.Function.Parameters
					} else if tc.wire == "messages" {
						schema = tool.InputSchema
					}
					if !missing[name] {
						continue // Adapters may also expose provider-native tools.
					}
					delete(missing, name)
					if schema["type"] != "object" || schema["anyOf"] != nil || schema["oneOf"] != nil || schema["allOf"] != nil {
						t.Errorf("%s: tool parameters must have a portable object root", name)
						http.Error(w, "tool parameter root must be an object type", 400)
						return
					}
					compiler := jsonschema.NewCompiler()
					if err := compiler.AddResource("schema.json", schema); err != nil {
						t.Error(err)
						return
					}
					compiled, err := compiler.Compile("schema.json")
					if err != nil {
						t.Errorf("%s: invalid wire schema: %v", name, err)
						return
					}
					if name == "read_file" {
						foundRead = true
						for _, args := range []map[string]any{{"path": float64(1)}, {"continuation": true}} {
							if err := compiled.Validate(args); err == nil {
								t.Errorf("read_file lost selector type constraints: %v", args)
							}
						}
						for _, args := range []map[string]any{
							{"path": "notes.txt", "offset": float64(2), "limit": float64(10)},
							{"continuation": "opaque-next-page"},
						} {
							if err := compiled.Validate(args); err != nil {
								t.Errorf("read_file rejects supported arguments %v: %v", args, err)
							}
						}
					}
				}
				for name := range missing {
					t.Errorf("adapter omitted tool %s", name)
				}
				if !foundRead {
					t.Error("direct request omitted read_file")
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
			switch {
			case tc.name == "grok-build":
				client, err = grokbuild.New(grokbuild.ClientConfig{BaseURL: server.URL, APIKey: "test"})
			case tc.wire == "messages":
				client, err = anthropic.New(anthropic.ClientConfig{BaseURL: server.URL, APIKey: "test"})
			default:
				client, err = openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: tc.wire})
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events, err := client.StreamChat(ctx, providers.ChatRequest{Model: tc.model, Messages: []providers.ChatMessage{{Role: "user", Content: "Reply ok"}}, Tools: defs})
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
