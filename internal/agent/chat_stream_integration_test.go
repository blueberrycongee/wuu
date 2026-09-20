package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

const chatPartial = "data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\n"
const chatFailure = "data: {\"error\":{\"code\":\"server_error\",\"message\":\"Synthetic upstream failure\"},\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"},\"finish_reason\":\"error\"}]}\n\n"
const chatDraft = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"draft\",\"function\":{\"name\":\"count_effect\",\"arguments\":\"\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"amount\\\":1}\"}}]}}]}\n\n"
const chatToolFinish = "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"
const chatComplete = "data: {\"choices\":[{\"delta\":{\"content\":\"Complete answer\"},\"finish_reason\":\"stop\"}]}\n\n"

func TestChatStreamFactory_RecoveryAndToolSafety(t *testing.T) {
	for _, tc := range []struct {
		name, body        string
		requests, effects int32
		reconnects        int
	}{
		{"error_first_frame", chatFailure, 2, 0, 1},
		{"partial_error_eof", chatPartial + chatFailure, 2, 0, 1},
		{"partial_error_done", chatPartial + chatFailure + "data: [DONE]\n\n", 2, 0, 1},
		{"draft_error_eof", chatDraft + chatFailure, 2, 0, 1},
		{"draft_error_done", chatDraft + chatFailure + "data: [DONE]\n\n", 2, 0, 1},
		{"stop_eof", chatComplete, 1, 0, 0},
		{"stop_done", chatComplete + "data: [DONE]\n\n", 1, 0, 0},
		{"length_eof", strings.Replace(chatComplete, "stop", "length", 1), 1, 0, 0},
		{"tool_calls_eof", chatDraft + chatToolFinish, 2, 1, 0},
		{"tool_calls_done", chatDraft + chatToolFinish + "data: [DONE]\n\n", 2, 1, 0},
		{"incomplete_text", chatPartial, 2, 0, 1},
		{"incomplete_draft", chatDraft, 2, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := runChatStream(t, tc.body, false)
			if o.err != nil || o.result.DriverStatus != "succeeded" || o.result.Content != "Complete answer" {
				t.Fatalf("response did not complete cleanly: status=%s content=%q err=%v", o.result.DriverStatus, o.result.Content, o.err)
			}
			if o.requests != tc.requests || o.effects != tc.effects || o.reconnects != tc.reconnects || o.toolResults != tc.effects {
				t.Fatalf("unexpected recovery/tool history: %+v", o)
			}
			if tc.effects == 0 {
				for _, message := range o.result.NewMessages {
					if message.Role == "tool" || len(message.ToolCalls) != 0 {
						t.Errorf("failed draft entered durable history: %+v", message)
					}
				}
			}
		})
	}
}

func TestChatStreamFactory_NonRetryableError(t *testing.T) {
	o := runChatStream(t, chatDraft+strings.Replace(chatFailure, "server_error", "insufficient_quota", 1), false)
	var streamErr *providers.StreamError
	if !errors.As(o.err, &streamErr) || streamErr.Code != "insufficient_quota" || o.result.DriverStatus != "failed" || o.requests != 1 || o.reconnects != 0 || o.effects != 0 || o.toolResults != 0 {
		t.Fatalf("non-retryable error lost or retried: %+v", o)
	}
}

func TestChatStreamFactory_BlocksReplayAfterExecutedTool(t *testing.T) {
	// Wait for a legitimately finalized tool to execute before sending the error.
	// A later provider failure must not replay already admitted work.
	o := runChatStream(t, chatDraft+chatToolFinish, true)
	var blocked *providers.ReplayBlockedError
	if !errors.As(o.err, &blocked) || o.result.DriverStatus != "failed" || o.requests != 1 || o.reconnects != 0 || o.effects != 1 {
		t.Fatalf("unsafe replay was not blocked: %+v", o)
	}
}

type chatStreamObservation struct {
	result                         agent.LoopResult
	err                            error
	requests, effects, toolResults int32
	reconnects                     int
}

func runChatStream(t *testing.T, body string, failAfterExecution bool) chatStreamObservation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tool := &chatMemoryTool{executed: make(chan struct{})}
	var requests, toolResults atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Stream || r.Method != "POST" || r.URL.Path != "/chat/completions" {
			t.Errorf("invalid factory request: %s %s stream=%v err=%v", r.Method, r.URL.Path, req.Stream, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := requests.Add(1)
		response := body
		if n > 1 {
			response = chatComplete
			for _, message := range req.Messages {
				if message.Role == "tool" {
					toolResults.Add(1)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range strings.SplitAfter(response, "\n\n") {
			fmt.Fprint(w, frame)
			w.(http.Flusher).Flush()
			if n == 1 && frame == chatToolFinish {
				// Exercise early execution deterministically before EOF/DONE or
				// a later error, rather than racing final-call registration.
				select {
				case <-tool.executed:
				case <-ctx.Done():
					t.Error("finalized tool did not execute during the stream")
					return
				}
			}
		}
		if n == 1 && failAfterExecution {
			fmt.Fprint(w, chatFailure)
		}
	}))
	defer server.Close()
	// Explicit synthetic credentials avoid credential-store fallback. Leave type
	// and wire unset to exercise the production OpenRouter factory mapping.
	client, err := providerfactory.BuildRuntimeStreamClient(config.ProviderConfig{
		NPM: "@openrouter/ai-sdk-provider", BaseURL: server.URL,
		APIKey: "synthetic-key", AuthToken: "synthetic-unused-token",
	}, "synthetic-openrouter")
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := toolledger.New(t.TempDir(), "synthetic-chat-stream")
	if err != nil {
		t.Fatal(err)
	}
	runner := &agent.StreamRunner{
		Client: client, ProviderName: "synthetic-openrouter", Model: "openai/gpt-4o", MaxSteps: 3,
		Tools: tool, ToolLedger: ledger, StreamingToolExecution: true,
	}
	var o chatStreamObservation
	o.result, o.err = runner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Synthetic local test"}}, func(event providers.StreamEvent) {
		if event.Type == providers.EventReconnect {
			o.reconnects++
		}
	})
	o.requests, o.effects, o.toolResults = requests.Load(), tool.effects.Load(), toolResults.Load()
	t.Logf("requests=%d reconnects=%d effects=%d tool_results=%d status=%s content=%q err=%v", o.requests, o.reconnects, o.effects, o.toolResults, o.result.DriverStatus, o.result.Content, o.err)
	return o
}

type chatMemoryTool struct {
	effects  atomic.Int32
	executed chan struct{}
}

func (*chatMemoryTool) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "count_effect", Description: "Synthetic memory counter", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"amount": map[string]any{"type": "integer"}}}}}
}

func (*chatMemoryTool) ToolMetadata(providers.ToolCall) (agent.ToolMetadata, bool) {
	return agent.ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}

func (m *chatMemoryTool) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	var args struct {
		Amount int `json:"amount"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return "", err
	}
	if call.Name != "count_effect" || args.Amount != 1 {
		return "", errors.New("invalid synthetic tool call")
	}
	if m.effects.Add(1) == 1 {
		close(m.executed)
	}
	return `{"counted":true}`, nil
}
