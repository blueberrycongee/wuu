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
