package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

// Hide StreamChat to exercise the unary-to-stream adapter used by the runner.
type continuationChatOnly struct{ providers.Client }

func TestResponsesTurnContinuation(t *testing.T) {
	for _, transport := range []string{"unary", "sse", "websocket"} {
		eventOnlyFinish := providers.FinishReasonStop
		if transport == "unary" {
			eventOnlyFinish = ""
		}
		for _, tc := range []struct {
			name, metadata, content string
			continueTurn            bool
			finish                  providers.FinishReason
		}{
			{"explicit continuation", `"status":"completed","end_turn":false`, "working", true, providers.FinishReasonStop},
			{"empty continuation", `"status":"completed","end_turn":false`, "", true, providers.FinishReasonStop},
			{"event-only completion", `"end_turn":false`, "working", transport != "unary", eventOnlyFinish},
			{"missing flag", `"status":"completed"`, "", false, providers.FinishReasonStop},
			{"null flag", `"status":"completed","end_turn":null`, "", false, providers.FinishReasonStop},
			{"true flag", `"status":"completed","end_turn":true`, "", false, providers.FinishReasonStop},
			{"commentary alone", `"status":"completed"`, "working", false, providers.FinishReasonStop},
			{"output limit", `"status":"incomplete","end_turn":false,"incomplete_details":{"reason":"max_output_tokens"}`, "partial", false, providers.FinishReasonLength},
			{"content filter", `"status":"incomplete","end_turn":false,"incomplete_details":{"reason":"content_filter"}`, "blocked", false, providers.FinishReasonContentFilter},
			{"unknown incomplete", `"status":"incomplete","end_turn":false,"incomplete_details":{"reason":"future_limit"}`, "partial", false, providers.FinishReasonUnknown},
			{"incomplete without details", `"status":"incomplete","end_turn":false`, "partial", false, providers.FinishReasonUnknown},
			{"conflicting details", `"status":"completed","end_turn":false,"incomplete_details":{}`, "partial", false, providers.FinishReasonStop},
			{"unknown status", `"status":"future_status","end_turn":false`, "partial", false, providers.FinishReasonUnknown},
			{"foreign stop reason", `"status":"pause_turn","end_turn":false`, "partial", false, providers.FinishReasonUnknown},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var conn *websocket.Conn
					var request []byte
					var err error
					if transport == "websocket" {
						conn, err = websocket.Accept(w, r, nil)
						if err != nil {
							t.Error(err)
							return
						}
						defer conn.CloseNow()
					}
					// Continuation reuses the WebSocket. Closing after the first response
					// races the next request and exercises transport fallback instead.
					for {
						if conn != nil {
							_, request, err = conn.Read(ctx)
						} else {
							request, err = io.ReadAll(r.Body)
						}
						if err != nil {
							t.Error(err)
							return
						}
						n := requests.Add(1)
						text, metadata := tc.content, tc.metadata
						if n > 1 {
							if n != 2 || !tc.continueTurn {
								t.Errorf("unexpected billable request %d", n)
							}
							if tc.content != "" && !strings.Contains(string(request), tc.content) {
								t.Errorf("continuation lost prior assistant content: %s", request)
							}
							text, metadata = "final answer", `"status":"completed","end_turn":true`
						}
						encodedText, _ := json.Marshal(text)
						item := fmt.Sprintf(`{"id":"msg_%d","type":"message","role":"assistant","phase":"commentary","status":"completed","content":[{"type":"output_text","text":%s}]}`, n, encodedText)
						response := fmt.Sprintf(`{"id":"resp_%d",%s,"output":[%s],"usage":{"input_tokens":7,"output_tokens":3}}`, n, metadata, item)
						if transport == "unary" {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, response)
							return
						}
						eventType := "response.completed"
						if strings.Contains(metadata, `"status":"incomplete"`) {
							eventType = "response.incomplete"
						}
						events := []string{
							fmt.Sprintf(`{"type":"response.output_item.added","output_index":0,"item":%s}`, item),
							fmt.Sprintf(`{"type":"response.output_text.delta","delta":%s}`, encodedText),
							fmt.Sprintf(`{"type":"%s","response":%s}`, eventType, response),
						}
						w.Header().Set("Content-Type", "text/event-stream")
						for _, event := range events {
							if conn != nil {
								if err := conn.Write(ctx, websocket.MessageText, []byte(event)); err != nil {
									t.Error(err)
									return
								}
							} else {
								fmt.Fprintf(w, "data: %s\n\n", event)
							}
						}
						if conn == nil || !tc.continueTurn || n >= 2 {
							return
						}
					}
				}))
				defer server.Close()
				mode := providers.StreamTransportSSE
				if transport == "websocket" {
					mode = providers.StreamTransportWebSocket
				}
				client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesTransport: mode})
				if err != nil {
					t.Fatal(err)
				}
				var api providers.Client = client
				if transport == "unary" {
					api = continuationChatOnly{client}
				}
				runner := &agent.Runner{Client: api, Model: "test", MaxSteps: 3}
				result, err := runner.RunWithUsage(ctx, "hello", nil)
				if err != nil {
					t.Fatal(err)
				}
				wantRequests, wantContent := int32(1), tc.content
				if tc.continueTurn {
					wantRequests, wantContent = 2, "final answer"
				}
				if requests.Load() != wantRequests || result.Content != wantContent || result.FinishReason != tc.finish || result.OutputTokens != int(wantRequests)*3 {
					t.Fatalf("requests=%d, result=%+v", requests.Load(), result)
				}
			})
		}
	}
}

func TestChatCompletionsDoesNotInferContinuationFromOtherProtocols(t *testing.T) {
	for _, reason := range []string{"stop", "length", "content_filter", "insufficient_system_resource", "pause_turn"} {
		t.Run(reason, func(t *testing.T) {
			content := "partial"
			if reason == "stop" {
				content = ""
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":%q,\"native_finish_reason\":\"pause_turn\"}],\"end_turn\":false}\n\ndata: [DONE]\n\n", content, reason)
			}))
			defer server.Close()
			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test"})
			if err != nil {
				t.Fatal(err)
			}
			runner := &agent.Runner{Client: client, Model: "test", MaxSteps: 2}
			result, err := runner.Run(context.Background(), "hello")
			if err != nil || requests.Load() != 1 || result != content {
				t.Fatalf("requests=%d result=%q err=%v", requests.Load(), result, err)
			}
		})
	}
}

// Mixed output must survive the agent loop without regrouping reasoning ahead
// of commentary. The same order is also the WebSocket continuation baseline.
func TestResponsesAgentPreservesOutputOrder(t *testing.T) {
	for _, transport := range []string{"unary", "sse", "websocket"} {
		for _, terminalSnapshot := range []bool{false, true} {
			if transport == "unary" && !terminalSnapshot {
				continue
			}
			t.Run(fmt.Sprintf("%s/snapshot=%v", transport, terminalSnapshot), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				items := []string{
					`{"id":"rs_1","type":"reasoning","encrypted_content":"opaque-1","summary":[]}`,
					`{"id":"msg_1","type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Reading."}]}`,
					`{"id":"rs_2","type":"reasoning","encrypted_content":"opaque-2","summary":[]}`,
					`{"id":"msg_2","type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Checking."}]}`,
					`{"id":"fc_1","type":"function_call","name":"read_file","call_id":"call_1","arguments":"{\"path\":\"README.md\"}"}`,
				}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var conn *websocket.Conn
					if transport == "websocket" {
						var err error
						conn, err = websocket.Accept(w, r, nil)
						if err != nil {
							t.Error(err)
							return
						}
						defer conn.CloseNow()
					}
					for {
						var raw []byte
						var err error
						if conn != nil {
							_, raw, err = conn.Read(ctx)
						} else {
							raw, err = io.ReadAll(r.Body)
						}
						if err != nil {
							t.Error(err)
							return
						}
						n := requests.Add(1)
						var body struct {
							Input    []responsesInputItem `json:"input"`
							Previous string               `json:"previous_response_id"`
						}
						if err := json.Unmarshal(raw, &body); err != nil {
							t.Error(err)
							return
						}
						if n == 2 {
							if transport == "websocket" {
								if body.Previous != "resp_order" || len(body.Input) != 1 || body.Input[0].Type != "function_call_output" {
									t.Errorf("lost cached continuation: %s", raw)
								}
							} else {
								var ids []string
								for _, item := range body.Input {
									if item.ID != "" {
										ids = append(ids, item.ID)
									}
								}
								if strings.Join(ids, ",") != "rs_1,msg_1,rs_2,msg_2,fc_1" {
									t.Errorf("reordered output: %v", ids)
								}
							}
						}
						output := items
						id := "resp_order"
						if n == 2 {
							output = []string{`{"id":"msg_final","type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"done"}]}`}
							id = "resp_final"
						}
						terminalOutput := strings.Join(output, ",")
						if !terminalSnapshot {
							terminalOutput = ""
						}
						response := fmt.Sprintf(`{"id":%q,"status":"completed","output":[%s],"usage":{"input_tokens":10,"output_tokens":2}}`, id, terminalOutput)
						if transport == "unary" {
							fmt.Fprint(w, response)
							return
						}
						send := func(event string) {
							if conn != nil {
								if err := conn.Write(ctx, websocket.MessageText, []byte(event)); err != nil {
									t.Error(err)
								}
							} else {
								fmt.Fprintf(w, "data: %s\n\n", event)
							}
						}
						w.Header().Set("Content-Type", "text/event-stream")
						for i, item := range output {
							send(fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, i, item))
						}
						send(fmt.Sprintf(`{"type":"response.completed","response":%s}`, response))
						if conn == nil || n >= 2 {
							return
						}
					}
				}))
				defer server.Close()
				store := false
				mode := providers.StreamTransportSSE
				if transport == "websocket" {
					mode = providers.StreamTransportAuto
				}
				client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesTransport: mode, ResponsesStore: &store, ResponsesWebSocketCache: NewResponsesWebSocketCache()})
				if err != nil {
					t.Fatal(err)
				}
				var api providers.Client = client
				if transport == "unary" {
					api = continuationChatOnly{client}
				}
				runner := agent.StreamRunner{Client: providers.AdaptStreamClient(api), Model: "gpt-test", Tools: &webSocketAgentLoopTools{}, PromptCacheKey: "thread-output-order"}
				result, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "read README"}}, nil)
				if err != nil || result.Content != "done" || requests.Load() != 2 {
					t.Fatalf("requests=%d result=%+v err=%v", requests.Load(), result, err)
				}
			})
		}
	}
}

func TestResponsesOrderedReplayRespectsHistoryEdits(t *testing.T) {
	message := providers.ChatMessage{Role: "assistant", Content: "old", Phase: providers.MessagePhaseCommentary, ProviderItemID: "msg_1", ProviderItemProvider: "openai", ProviderItemModel: "model-a", ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"old"}`, ProviderItemID: "fc_1", ProviderItemModel: "model-a"}}, ProviderItems: []providers.ProviderItem{
		{Type: "message", Provider: "openai", Data: `{"type":"message","id":"msg_1","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"old"}]}`},
		{Type: "function_call", Provider: "openai", Data: `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"old\"}"}`},
	}}
	for _, tc := range []struct {
		name            string
		change          func(*providers.ChatMessage)
		provider, model string
	}{
		{"edited content", func(m *providers.ChatMessage) { m.Content = "new" }, "openai", "model-a"},
		{"repaired call", func(m *providers.ChatMessage) { m.ToolCalls[0].ID = "repaired" }, "openai", "model-a"},
		{"edited arguments", func(m *providers.ChatMessage) { m.ToolCalls[0].Arguments = `{"path":"new"}` }, "openai", "model-a"},
		{"different model", func(m *providers.ChatMessage) {}, "openai", "model-b"},
		{"different provider", func(m *providers.ChatMessage) {}, "other", "model-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := providers.CloneChatMessage(message)
			tc.change(&msg)
			msg = providers.ApplyProviderModelMessageCompatibility(tc.provider, tc.model, []providers.ChatMessage{msg})[0]
			got := appendResponsesInputItem(nil, msg, tc.provider, "", tc.model, false)
			msg.ProviderItems = nil
			want := appendResponsesInputItem(nil, msg, tc.provider, "", tc.model, false)
			if !responsesInputItemsEqual(got, want) {
				t.Fatalf("native snapshot bypassed current history: got=%+v want=%+v", got, want)
			}
		})
	}
}

// Routing affinity belongs to a turn, even when the client and caller context
// survive many turns. The server's first marker is authoritative for retries
// and continuations; later responses must not replace it.
func TestResponsesTurnRouting(t *testing.T) {
	for _, transport := range []string{"unary", "sse", "websocket-metadata", "websocket-handshake"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var conn *websocket.Conn
				if strings.HasPrefix(transport, "websocket") {
					if transport == "websocket-handshake" {
						w.Header().Set("x-codex-turn-state", "turn-0")
					}
					var err error
					conn, err = websocket.Accept(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.CloseNow()
				}
				for {
					got := r.Header.Get("x-codex-turn-state")
					if conn != nil {
						_, data, err := conn.Read(ctx)
						if err != nil {
							return
						}
						var request struct {
							Metadata map[string]string `json:"client_metadata"`
						}
						if err := json.Unmarshal(data, &request); err != nil {
							t.Error(err)
							return
						}
						got = request.Metadata["x-codex-turn-state"]
					}
					n := int(requests.Add(1))
					turn, round := (n-1)/3, (n-1)%3
					want := ""
					if round != 0 || transport == "websocket-handshake" && n == 1 {
						want = fmt.Sprintf("turn-%d", turn)
					}
					if got != want {
						t.Errorf("request %d routing = %q, want %q", n, got, want)
					}
					marker := "must-not-replace-first-marker"
					if round == 0 {
						marker = fmt.Sprintf("turn-%d", turn)
					}
					w.Header().Set("x-codex-turn-state", marker)
					response := fmt.Sprintf(`{"id":"resp_%d","status":"completed","end_turn":%t,"output":[{"id":"msg_%d","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":7,"output_tokens":1}}`, n, round == 2, n)
					if conn != nil {
						metadata := fmt.Sprintf(`{"type":"response.metadata","headers":{"X-Codex-Turn-State":%q}}`, marker)
						if err := conn.Write(ctx, websocket.MessageText, []byte(metadata)); err != nil {
							t.Error(err)
							return
						}
						if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"type":"response.completed","response":%s}`, response))); err != nil {
							t.Error(err)
							return
						}
						continue
					}
					if transport == "unary" {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, response)
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", response)
					}
					return
				}
			}))
			defer server.Close()
			transportMode := providers.StreamTransportSSE
			if strings.HasPrefix(transport, "websocket") {
				transportMode = providers.StreamTransportWebSocketCached
			}
			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesTransport: transportMode})
			if err != nil {
				t.Fatal(err)
			}
			var stream providers.StreamClient = providers.AdaptStreamClient(client)
			if transport == "unary" {
				stream = providers.AdaptStreamClient(continuationChatOnly{client})
			}
			runner := agent.StreamRunner{Client: stream, Model: "test", PromptCacheKey: "same-thread"}
			for i := 0; i < 2; i++ {
				if _, err := runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "continue"}}, nil); err != nil {
					t.Fatal(err)
				}
			}
			if got := requests.Load(); got != 6 {
				t.Fatalf("requests = %d, want 6", got)
			}
		})
	}
}

func TestResponsesTurnRoutingIsolation(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("x-codex-turn-state"))
		w.Header().Set("x-codex-turn-state", fmt.Sprintf("marker-%d", len(seen)))
		fmt.Fprint(w, `{"id":"r","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer server.Close()
	routing := &providers.TurnRouting{}
	for i, tc := range []struct{ path, key, account, session, want string }{
		{"/a", "one", "account1", "thread1", ""},
		{"/a", "one", "account1", "thread1", "marker-1"},
		{"/b", "one", "account1", "thread1", ""},
		{"/b", "two", "account1", "thread1", ""},
		{"/b", "two", "account2", "thread1", ""},
		{"/b", "two", "account2", "thread2", ""},
		{"/b", "two", "account2", "thread2", "marker-6"},
	} {
		client, err := New(ClientConfig{BaseURL: server.URL + tc.path, APIKey: tc.key, WireAPI: "responses", Headers: map[string]string{"ChatGPT-Account-ID": tc.account}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Chat(context.Background(), providers.ChatRequest{Model: "test", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}}, CacheHint: &providers.CacheHint{PromptCacheKey: tc.session}, TurnRouting: routing})
		if err != nil {
			t.Fatal(err)
		}
		if seen[i] != tc.want {
			t.Errorf("request %d routed with %q, want %q", i+1, seen[i], tc.want)
		}
	}
}
