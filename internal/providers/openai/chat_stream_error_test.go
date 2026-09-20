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
)

// The unified error shape is documented at
// https://openrouter.ai/docs/api/reference/streaming#handling-errors-during-streaming.
const chatStreamFailure = `{"id":"cmpl-abc123","object":"chat.completion.chunk","created":1234567890,"model":"openai/gpt-4o","provider":"openai","error":{"code":"server_error","message":"Provider disconnected unexpectedly"},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`

func TestStreamChat_DeclaredErrors(t *testing.T) {
	for _, tc := range []struct {
		name, payload, code, message string
		category                     providers.FailureCategory
	}{
		{"openrouter", chatStreamFailure, "server_error", "Provider disconnected unexpectedly", providers.FailureIncompleteStream},
		// Additional compatible-endpoint robustness cases, not claimed OpenRouter fixtures.
		{"top_level_only", `{"error":{"code":"server_error","message":"synthetic failure"}}`, "server_error", "synthetic failure", providers.FailureIncompleteStream},
		{"error_overrides_tool_finish", `{"error":{"code":"server_error","message":"synthetic failure"},"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, "server_error", "synthetic failure", providers.FailureIncompleteStream},
		{"numeric_code", `{"error":{"code":503,"message":"synthetic failure"}}`, "503", "synthetic failure", providers.FailureIncompleteStream},
		{"numeric_string_code", `{"error":{"code":"503","message":"synthetic failure"}}`, "503", "synthetic failure", providers.FailureIncompleteStream},
		{"auth", `{"error":{"code":401,"message":"synthetic failure"}}`, "401", "synthetic failure", providers.FailureAuthentication},
		{"quota", `{"error":{"code":"insufficient_quota","message":"synthetic failure"}}`, "insufficient_quota", "synthetic failure", providers.FailureQuota},
		{"context", `{"error":{"code":"context_length_exceeded","message":"synthetic failure"}}`, "context_length_exceeded", "synthetic failure", providers.FailureContextOverflow},
		{"type_fallback", `{"error":{"type":"rate_limit_error","message":"synthetic failure"}}`, "rate_limit_error", "synthetic failure", providers.FailureOverloaded},
		{"unknown", `{"error":{"code":"custom_failure","message":"synthetic failure"}}`, "custom_failure", "synthetic failure", providers.FailureUnknown},
		{"finish_only", `{"choices":[{"delta":{},"finish_reason":"error"}]}`, "", "", providers.FailureUnknown},
		{"empty_error", `{"error":{}}`, "", "", providers.FailureUnknown},
	} {
		for _, prefix := range []struct{ name, body string }{
			{"first_frame", ""},
			{"partial", "data: {\"choices\":[{\"delta\":{\"content\":\"Partial\"}}]}\n\n"},
			{"tool_draft", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"draft\",\"function\":{\"name\":\"count_effect\",\"arguments\":\"{}\"}}]}}]}\n\n"},
		} {
			for _, done := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/done=%v", tc.name, prefix.name, done), func(t *testing.T) {
					body := prefix.body + "data: " + tc.payload + "\n\n"
					if done {
						body += "data: [DONE]\n\n"
					}
					events, snapshot := collectChatStream(t, body, 0)
					var streamErr error
					var errorCount int
					for _, event := range events {
						switch event.Type {
						case providers.EventError:
							streamErr = event.Error
							errorCount++
						case providers.EventDone, providers.EventToolUseEnd:
							t.Errorf("failed stream must not complete the response or draft: %+v", event)
						}
					}
					var providerErr *providers.StreamError
					if errorCount != 1 || !errors.As(streamErr, &providerErr) {
						t.Fatalf("want one provider stream error, got %d: %v", errorCount, streamErr)
					}
					if providerErr.Code != tc.code || (tc.message != "" && providerErr.Message != tc.message) || providerErr.ProviderFamily != "openai" {
						t.Errorf("lost provider error detail: %+v", providerErr)
					}
					if got := providers.NormalizeFailure(streamErr).Category; got != tc.category {
						t.Errorf("failure category = %s, want %s", got, tc.category)
					}
					if len(snapshot.Submissions) != 1 || snapshot.Submissions[0].Outcome != providers.InferenceSubmissionFailed {
						t.Errorf("failed submission recorded as success: %+v", snapshot.Submissions)
					}
				})
			}
		}
	}
}

func TestStreamChat_DeclaredErrorRecoveryBudget(t *testing.T) {
	events, snapshot := collectChatStream(t, "data: "+chatStreamFailure+"\n\n", 2)
	var terminal error
	var reconnects int
	for _, event := range events {
		if event.Type == providers.EventDone {
			t.Fatal("exhausted error stream succeeded")
		}
		if event.Type == providers.EventError {
			terminal = event.Error
		}
		if event.Lifecycle != nil && event.Lifecycle.Phase == providers.StreamPhaseReconnecting {
			reconnects++
		}
	}
	var streamErr *providers.StreamError
	if !errors.As(terminal, &streamErr) || streamErr.Code != "server_error" || reconnects != 1 || len(snapshot.Submissions) != 2 {
		t.Fatalf("recovery exceeded or bypassed frozen budget: err=%v reconnects=%d submissions=%+v", terminal, reconnects, snapshot.Submissions)
	}
	for _, submission := range snapshot.Submissions {
		if submission.Outcome != providers.InferenceSubmissionFailed {
			t.Errorf("error attempt was not failed: %+v", submission)
		}
	}
}

func TestStreamChat_DeclaredErrorRetainsUsage(t *testing.T) {
	// Usage can accompany an error, even without choices. It remains billable,
	// but must not turn the failed submission into a successful one.
	for _, tc := range []struct{ name, body string }{
		{"same_frame", "data: {\"error\":{\"code\":\"server_error\",\"message\":\"synthetic failure\"},\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"},
		{"prior_frame", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\ndata: " + chatStreamFailure + "\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events, snapshot := collectChatStream(t, tc.body, 0)
			if len(events) != 2 || events[0].Type != providers.EventUsage || events[1].Type != providers.EventError {
				t.Fatalf("want usage then error, got %+v", events)
			}
			submission := snapshot.Submissions[0]
			if submission.Outcome != providers.InferenceSubmissionFailed || submission.CostState != providers.InferenceCostKnown || submission.ReportedUsage == nil || submission.ReportedUsage.InputTokens != 12 || submission.ReportedUsage.OutputTokens != 3 {
				t.Fatalf("lost failed submission usage: %+v", submission)
			}
		})
	}
}

func TestStreamChat_DeclaredErrorRecoveryCanceled(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chatStreamFailure)
	}))
	defer server.Close()
	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var waits atomic.Int32
	reliable := providers.NewReliableStreamClient(client, nil, providers.WithStreamRetryWait(func(context.Context, time.Duration) error {
		waits.Add(1)
		cancel()
		return ctx.Err()
	}))
	ch, err := reliable.StreamChat(ctx, providers.ChatRequest{
		Model: "synthetic-model", Messages: []providers.ChatMessage{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for event := range ch {
		if event.Type == providers.EventDone {
			t.Error("canceled recovery succeeded")
		}
	}
	if requests.Load() != 1 || waits.Load() != 1 || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("cancellation did not stop recovery: requests=%d waits=%d err=%v", requests.Load(), waits.Load(), ctx.Err())
	}
}

func collectChatStream(t *testing.T, body string, attemptLimit int) ([]providers.StreamEvent, providers.InferenceExecutionSnapshot) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, body)
	}))
	defer server.Close()
	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := providers.EnsureInferenceExecutionContext(ctx, providers.ChatRequest{
		Model: "synthetic-model", Messages: []providers.ChatMessage{{Role: "user", Content: "test"}},
		Operation: providers.InferenceOperation{AttemptLimit: attemptLimit},
	}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	streamChat := client.StreamChat
	if attemptLimit > 0 {
		streamChat = providers.NewReliableStreamClient(client, nil).StreamChat
	}
	ch, err := streamChat(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	var events []providers.StreamEvent
	for event := range ch {
		events = append(events, event)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	snapshot := req.Execution.Snapshot()
	if int(requests.Load()) != len(snapshot.Submissions) {
		t.Fatalf("wire requests and submissions differ: requests=%d snapshot=%+v", requests.Load(), snapshot)
	}
	t.Logf("requests=%d events=%s", requests.Load(), streamEventTypes(events))
	return events, snapshot
}

func streamEventTypes(events []providers.StreamEvent) string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, string(event.Type))
	}
	return strings.Join(types, ",")
}
