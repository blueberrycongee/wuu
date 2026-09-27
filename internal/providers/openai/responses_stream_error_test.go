package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/coder/websocket"
)

func TestResponsesStreamErrorsRecoverOrStop(t *testing.T) {
	for _, transport := range []providers.StreamTransportMode{providers.StreamTransportSSE, providers.StreamTransportWebSocket} {
		for _, tc := range []struct {
			name, payload, code, message string
			category                     providers.FailureCategory
			retry                        bool
		}{
			{"top-level server", `{"type":"error","code":"server_error","message":"Temporary upstream failure","sequence_number":1}`, "server_error", "Temporary upstream failure", providers.FailureServer, true},
			{"nested server", `{"type":"error","error":{"code":"server_error","message":"Temporary upstream failure"}}`, "server_error", "Temporary upstream failure", providers.FailureServer, true},
			{"empty nested", `{"type":"error","error":{},"code":"server_error","message":"Temporary upstream failure"}`, "server_error", "Temporary upstream failure", providers.FailureServer, true},
			{"failed response", `{"type":"response.failed","response":{"error":{"code":"server_error","message":"Temporary upstream failure"}}}`, "server_error", "Temporary upstream failure", providers.FailureServer, true},
			{"request timeout", `{"type":"error","code":"request_timeout","message":"stream error: stream disconnected before completion: stream closed before response.completed"}`, "request_timeout", "stream disconnected before completion", providers.FailureServer, true},
			{"nested request timeout", `{"type":"error","error":{"code":"request_timeout","message":"Request expired"}}`, "request_timeout", "Request expired", providers.FailureServer, true},
			{"failed request timeout", `{"type":"response.failed","response":{"error":{"code":"request_timeout","message":"stream error: stream disconnected before completion: stream closed before response.completed"}}}`, "request_timeout", "stream disconnected before completion", providers.FailureServer, true},
			{"request timeout status", `{"type":"error","code":"408","message":"Request expired"}`, "408", "Request expired", providers.FailureServer, true},
			{"gateway timeout status", `{"type":"error","code":"504","message":"Request expired"}`, "504", "Request expired", providers.FailureServer, true},
			{"timeout with terminal quota", `{"type":"error","code":"request_timeout","message":"insufficient_quota"}`, "request_timeout", "insufficient_quota", providers.FailureQuota, false},
			{"rate limit", `{"type":"error","code":"rate_limit_exceeded","message":"Slow down"}`, "rate_limit_exceeded", "Slow down", providers.FailureRateLimit, true},
			{"authentication", `{"type":"error","code":"invalid_api_key","message":"Rejected"}`, "invalid_api_key", "Rejected", providers.FailureAuthentication, false},
			{"nested precedence", `{"type":"error","code":"server_error","error":{"code":"invalid_api_key","message":"Rejected"}}`, "invalid_api_key", "Rejected", providers.FailureAuthentication, false},
			{"quota", `{"type":"error","code":"insufficient_quota","message":"No credits"}`, "insufficient_quota", "No credits", providers.FailureQuota, false},
			{"invalid request", `{"type":"error","code":"invalid_request_error","message":"Unsupported timeout parameter"}`, "invalid_request_error", "Unsupported timeout parameter", providers.FailureInvalidRequest, false},
			{"unknown code", `{"type":"error","code":"custom_failure","message":"Diagnostic detail"}`, "custom_failure", "Diagnostic detail", providers.FailureUnknown, false},
			{"unknown shape", `{"type":"error","opaque":{"content":"private response content"}}`, "", "Responses error event", providers.FailureUnknown, false},
			{"invalid final message", `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","content":{"text":"private response content"}}}`, "", "invalid Responses message content", providers.FailureUnknown, false},
			{"invalid terminal message", `{"type":"response.completed","response":{"status":"completed","output":[{"id":"msg_1","type":"message","content":{"text":"private response content"}}]}}`, "", "invalid Responses message content", providers.FailureUnknown, false},
		} {
			t.Run(string(transport)+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var conn *websocket.Conn
					if transport == providers.StreamTransportWebSocket {
						var err error
						conn, err = websocket.Accept(w, r, nil)
						if err != nil {
							t.Error(err)
							return
						}
						defer conn.CloseNow()
						if _, _, err := conn.Read(ctx); err != nil {
							t.Error(err)
							return
						}
					}
					payload := tc.payload
					if requests.Add(1) > 1 {
						payload = `{"type":"response.completed","response":{"id":"resp_ok","status":"completed","output":[]}}`
					}
					if conn != nil {
						if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
							t.Error(err)
						}
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: %s\n\n", payload)
					}
				}))
				defer server.Close()
				inner, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: "responses", ResponsesTransport: transport})
				if err != nil {
					t.Fatal(err)
				}
				checkCause := func(err error) {
					t.Helper()
					failure := providers.NormalizeFailure(err)
					if failure.Category != tc.category || failure.ProviderCode != tc.code || !strings.Contains(err.Error(), tc.message) {
						t.Errorf("failure=%+v, error=%v", failure, err)
					}
					if strings.Contains(err.Error(), "private response content") {
						t.Error("copied arbitrary event content into error")
					}
				}
				retries := 0
				client := providers.NewReliableStreamClient(inner, func(_ int, _ int, err error, _ time.Duration) {
					retries++
					checkCause(err)
				}, providers.WithStreamRetryWait(func(context.Context, time.Duration) error { return nil }))
				ch, err := client.StreamChat(ctx, providers.ChatRequest{Model: "test", CacheHint: &providers.CacheHint{PromptCacheKey: "thread-errors"}, Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}}})
				if err != nil {
					t.Fatal(err)
				}
				done, failures := 0, 0
				for ev := range ch {
					if ev.Type == providers.EventDone {
						done++
					}
					if ev.Type == providers.EventError {
						failures++
						checkCause(ev.Error)
						var recovery *providers.StreamRecoveryError
						if !errors.As(ev.Error, &recovery) || recovery.Recovery.RetryCount != 0 || recovery.Recovery.SubmissionCount != 1 || recovery.Recovery.StopReason != "non_retryable" {
							t.Errorf("terminal diagnostics = %#v", recovery)
						}
					}
				}
				if tc.retry {
					if requests.Load() != 2 || retries != 1 || done != 1 || failures != 0 {
						t.Fatalf("requests=%d retries=%d done=%d failures=%d", requests.Load(), retries, done, failures)
					}
				} else if requests.Load() != 1 || retries != 0 || done != 0 || failures != 1 {
					t.Fatalf("requests=%d retries=%d done=%d failures=%d", requests.Load(), retries, done, failures)
				}
			})
		}
	}
}
