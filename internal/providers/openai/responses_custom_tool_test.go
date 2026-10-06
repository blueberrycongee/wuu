package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// A text tool must survive delivery, execution normalization and replay without
// making the model encode source code as a JSON string.
func TestResponsesCustomToolRoundTrip(t *testing.T) {
	const source = "// @run_code: {\"max_output_tokens\":1200}\ntext('中文');\n// \\d ${literal} `quoted`\n"
	for _, mode := range []string{"unary", "sse", "websocket"} {
		t.Run(mode, func(t *testing.T) {
			stream := mode != "unary"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				var conn *websocket.Conn
				if mode == "websocket" {
					var err error
					conn, err = websocket.Accept(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.CloseNow()
					_, data, err := conn.Read(r.Context())
					if err != nil {
						t.Error(err)
						return
					}
					if err = json.Unmarshal(data, &body); err != nil {
						t.Error(err)
						return
					}
				} else if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				tool := body["tools"].([]any)[0].(map[string]any)
				if tool["type"] != "custom" || tool["parameters"] != nil {
					t.Errorf("unexpected declaration: %#v", tool)
				}
				item := map[string]any{"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1", "name": "run_code", "input": source}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					write := func(event any) {
						data, _ := json.Marshal(event)
						if conn != nil {
							if err := conn.Write(r.Context(), websocket.MessageText, data); err != nil {
								t.Error(err)
							}
						} else {
							fmt.Fprintf(w, "data: %s\n\n", data)
						}
					}
					write(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1", "name": "run_code", "input": ""}})
					for _, part := range []string{source[:19], source[19:]} {
						write(map[string]any{"type": "response.custom_tool_call_input.delta", "item_id": "ctc_1", "delta": part})
					}
					write(map[string]any{"type": "response.custom_tool_call_input.done", "item_id": "ctc_1", "input": source})
					write(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
					write(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []any{item}}})
				} else {
					json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{item}})
				}
			}))
			defer server.Close()
			transport := providers.StreamTransportSSE
			if mode == "websocket" {
				transport = providers.StreamTransportWebSocket
			}
			client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test", ResponsesTransport: transport})
			if err != nil {
				t.Fatal(err)
			}
			req := providers.ChatRequest{Model: "gpt-test", CacheHint: &providers.CacheHint{PromptCacheKey: "custom-round-trip"}, Messages: []providers.ChatMessage{{Role: "user", Content: "run"}}, Tools: []providers.ToolDefinition{{Name: "run_code", Freeform: true, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}}}}}
			var calls []providers.ToolCall
			if stream {
				ch, err := client.StreamChat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				for event := range ch {
					if event.Error != nil {
						t.Fatal(event.Error)
					}
					if event.Type == providers.EventToolUseEnd {
						calls = append(calls, *event.ToolCall)
					}
				}
			} else {
				resp, err := client.Chat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				calls = resp.ToolCalls
			}
			if len(calls) != 1 {
				t.Fatalf("calls=%+v", calls)
			}
			call := calls[0]
			var args struct {
				Input string `json:"input"`
			}
			if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
				t.Fatal(err)
			}
			if args.Input != source || call.Kind != providers.ToolCallKindCustom {
				t.Fatalf("lost source or kind: %+v", call)
			}
			req.Messages = append(req.Messages, providers.ChatMessage{Role: "assistant", ToolCalls: calls}, providers.ChatMessage{Role: "tool", ToolCallID: call.ID, ToolResultKind: call.Kind, Content: "ok"})
			payload, err := client.buildResponsesRequest(req, false)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(payload)
			var replay map[string]any
			json.Unmarshal(encoded, &replay)
			items := replay["input"].([]any)
			invoke := items[1].(map[string]any)
			result := items[2].(map[string]any)
			if invoke["type"] != "custom_tool_call" || invoke["input"] != source || invoke["id"] != "ctc_1" || result["type"] != "custom_tool_call_output" {
				t.Fatalf("invalid replay: %s", encoded)
			}
		})
	}
}
