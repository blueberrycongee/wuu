package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestExplicitResumeDoesNotUseAssistantPrefill(t *testing.T) {
	for _, thinking := range []bool{false, true} {
		history := []providers.ChatMessage{{Role: "user", Content: "original"}, {Role: "assistant", Content: "completed progress"}, {Role: "assistant", Content: "unsafe partial", Cause: providers.IncompleteStreamFragment}}
		options := map[string]any{}
		if thinking {
			options["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 1024}
		}
		req := providers.ChatRequest{Model: "claude-test", Messages: history, ResumeSampling: true, ProviderOptions: options}
		payload, err := buildAnthropicRequest(req, 4096, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(payload.Messages) != 3 || payload.Messages[1].Role != "assistant" || payload.Messages[2].Role != "user" {
			t.Fatalf("resume used assistant prefill: %+v", payload.Messages)
		}
		encoded, _ := json.Marshal(payload)
		if strings.Contains(string(encoded), "unsafe partial") {
			t.Fatal("partial leaked to model")
		}
		if len(history) != 3 || history[2].Content != "unsafe partial" {
			t.Fatal("adapter mutated durable input")
		}
	}
}

func TestExplicitResumeSurvivesContextOverflowWithThinking(t *testing.T) {
	for _, thinking := range []bool{false, true} {
		t.Run(fmt.Sprintf("thinking=%v", thinking), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body struct {
					Messages []struct {
						Role string `json:"role"`
					} `json:"messages"`
					Thinking json.RawMessage `json:"thinking"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body.Messages) == 0 || body.Messages[len(body.Messages)-1].Role != "user" {
					t.Error("explicit resume used assistant prefill")
				}
				if thinking && len(body.Thinking) == 0 {
					t.Error("thinking mode lost")
				}
				w.Header().Set("Content-Type", "application/json")
				if requests == 1 {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long"}}`)
					return
				}
				fmt.Fprint(w, `{"content":[{"type":"text","text":"continued"}],"stop_reason":"end_turn"}`)
			}))
			defer server.Close()
			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := agent.LoopConfig{Model: "claude-test", ResumeSampling: true, Compact: func(_ context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
				return messages[len(messages)-1:], nil
			}}
			if thinking {
				cfg.ProviderOptions = map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 1024}}
			}
			history := []providers.ChatMessage{{Role: "user", Content: "original"}, {Role: "assistant", Content: "completed progress"}}
			res, err := agent.RunToolLoop(context.Background(), history, cfg, agent.NewStreamStep(continuationChatOnly{client}))
			if err != nil || requests != 2 || res.Content != "continued" {
				t.Fatalf("requests=%d result=%+v err=%v", requests, res, err)
			}
		})
	}
}
