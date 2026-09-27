package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type continuationChatOnly struct{ providers.Client }

func TestMessagesTurnContinuation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, tc := range []struct {
			reason, content string
			continueTurn    bool
		}{
			{"pause_turn", "working", true},
			{"end_turn", "", false},
			{"stop_sequence", "partial", false},
			{"max_tokens", "partial", false},
			{"model_context_window_exceeded", "partial", false},
			{"refusal", "declined", false},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", streaming, tc.reason), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body struct {
						Messages []struct {
							Role    string `json:"role"`
							Content []struct {
								Type string `json:"type"`
								Text string `json:"text"`
							} `json:"content"`
						} `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					n := requests.Add(1)
					reason, text := tc.reason, tc.content
					if n > 1 {
						if n != 2 || !tc.continueTurn {
							t.Errorf("unexpected billable request %d", n)
						}
						found := false
						for _, msg := range body.Messages {
							if msg.Role == "assistant" && len(msg.Content) > 0 && msg.Content[0].Text == tc.content {
								found = true
							}
						}
						if !found {
							t.Error("pause continuation lost prior assistant content")
						}
						reason, text = "end_turn", "final answer"
					}
					if !streaming {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"content":[{"type":"text","text":%q}],"stop_reason":%q,"usage":{"input_tokens":7,"output_tokens":3}}`, text, reason)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n")
					fmt.Fprint(w, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
					fmt.Fprintf(w, "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", text)
					fmt.Fprint(w, "event: content_block_stop\ndata: {\"index\":0}\n\n")
					fmt.Fprintf(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":3}}\n\n", reason)
					fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
				}))
				defer server.Close()
				client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test"})
				if err != nil {
					t.Fatal(err)
				}
				var api providers.Client = client
				if !streaming {
					api = continuationChatOnly{client}
				}
				runner := &agent.Runner{Client: api, Model: "test", MaxSteps: 3}
				result, err := runner.RunWithUsage(context.Background(), "hello", nil)
				wantRequests, wantContent := int32(1), tc.content
				if tc.continueTurn {
					wantRequests, wantContent = 2, "final answer"
				}
				if err != nil || requests.Load() != wantRequests || result.Content != wantContent || result.OutputTokens != int(wantRequests)*3 {
					t.Fatalf("requests=%d result=%+v err=%v", requests.Load(), result, err)
				}
			})
		}
	}
}
