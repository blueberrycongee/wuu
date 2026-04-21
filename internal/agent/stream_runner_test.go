package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type mockStreamAttempt struct {
	events []providers.StreamEvent
	err    error
}

type mockStreamClient struct {
	events        []providers.StreamEvent
	attempts      []mockStreamAttempt
	chatResponses []providers.ChatResponse
	chatErrs      []error
	requests      []providers.ChatRequest
	callCount     int
	chatCallCount int
}

func (m *mockStreamClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	m.requests = append(m.requests, req)
	idx := m.chatCallCount
	m.chatCallCount++
	if idx < len(m.chatErrs) && m.chatErrs[idx] != nil {
		return providers.ChatResponse{}, m.chatErrs[idx]
	}
	if idx < len(m.chatResponses) {
		return m.chatResponses[idx], nil
	}
	return providers.ChatResponse{}, nil
}

func (m *mockStreamClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	m.requests = append(m.requests, req)
	if len(m.attempts) > 0 {
		idx := m.callCount
		m.callCount++
		if idx >= len(m.attempts) {
			return nil, errors.New("unexpected extra stream attempt")
		}
		attempt := m.attempts[idx]
		if attempt.err != nil {
			return nil, attempt.err
		}
		ch := make(chan providers.StreamEvent, len(attempt.events))
		for _, e := range attempt.events {
			ch <- e
		}
		close(ch)
		return ch, nil
	}
	ch := make(chan providers.StreamEvent, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func TestStreamRunner_DefaultReconnectConfigMatchesCC(t *testing.T) {
	cfg := (&StreamRunner{}).streamReconnectCfg()
	if cfg.Budget != 10*time.Minute {
		t.Fatalf("expected 10m budget, got %s", cfg.Budget)
	}
	if cfg.InitialDelay != 1*time.Second {
		t.Fatalf("expected 1s initial delay, got %s", cfg.InitialDelay)
	}
	if cfg.MaxDelay != 30*time.Second {
		t.Fatalf("expected 30s max delay, got %s", cfg.MaxDelay)
	}
}

func TestStreamRunner_SimpleContent(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "Hello "},
			{Type: providers.EventContentDelta, Content: "world"},
			{Type: providers.EventDone},
		},
	}

	var received []providers.StreamEvent
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		OnEvent: func(ev providers.StreamEvent) {
			received = append(received, ev)
		},
	}

	result, err := runner.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "Hello world" {
		t.Fatalf("unexpected result: %q", result)
	}

	if len(received) != 6 {
		t.Fatalf("expected 6 events including lifecycle/message, got %d", len(received))
	}
	if received[0].Type != providers.EventLifecycle || received[0].Lifecycle == nil || received[0].Lifecycle.Phase != providers.StreamPhaseConnecting {
		t.Fatalf("unexpected first event: %+v", received[0])
	}
	if received[1].Type != providers.EventLifecycle || received[1].Lifecycle == nil || received[1].Lifecycle.Phase != providers.StreamPhaseConnected {
		t.Fatalf("unexpected second event: %+v", received[1])
	}
	if received[2].Type != providers.EventContentDelta || received[2].Content != "Hello " {
		t.Fatalf("unexpected first content event: %+v", received[2])
	}
	if received[4].Type != providers.EventDone {
		t.Fatalf("expected done event before committed message, got %s", received[4].Type)
	}
	if received[5].Type != providers.EventMessage || received[5].Message == nil || received[5].Message.Role != "assistant" {
		t.Fatalf("expected committed assistant message event, got %+v", received[5])
	}
}

func TestStreamRunner_AllowsNaturalEmptyCompletionWithoutPersistingAssistantMessage(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventDone, StopReason: "end_turn"},
		},
	}

	var received []providers.StreamEvent
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		OnEvent: func(ev providers.StreamEvent) {
			received = append(received, ev)
		},
	}

	result, err := runner.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty result, got %q", result)
	}
	for _, ev := range received {
		if ev.Type == providers.EventMessage {
			t.Fatalf("did not expect persisted assistant message event, got %+v", ev)
		}
	}
}

func TestStreamRunner_NoToolCallsWhenNoneRequested(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "answer"},
			{Type: providers.EventDone},
		},
	}

	tools := &fakeTools{}
	runner := StreamRunner{
		Client: client,
		Tools:  tools,
		Model:  "test-model",
	}

	result, err := runner.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "answer" {
		t.Fatalf("unexpected result: %q", result)
	}
	// Tools should not have been called.
	if len(tools.calls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(tools.calls))
	}
}

func TestStreamRunner_ValidationErrors(t *testing.T) {
	// Run validates blank prompt.
	runner := StreamRunner{Model: "m"}
	_, err := runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error for nil client")
	}

	client := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventDone}}}
	runner = StreamRunner{Client: client}
	_, err = runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error for empty model")
	}

	runner = StreamRunner{Client: client, Model: "m"}
	_, err = runner.Run(context.Background(), "  ")
	if err == nil {
		t.Fatal("expected error for blank prompt")
	}

	// RunWithCallback validates client and model but not prompt.
	runner = StreamRunner{Model: "m"}
	_, err = runner.RunWithCallback(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil client in RunWithCallback")
	}

	runner = StreamRunner{Client: client}
	_, err = runner.RunWithCallback(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty model in RunWithCallback")
	}
}

func TestStreamRunner_StreamError(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "partial"},
			{Type: providers.EventError, Error: context.DeadlineExceeded},
		},
	}

	runner := StreamRunner{Client: client, Model: "m"}
	_, err := runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error from stream error event")
	}
}

func TestStreamRunner_RetryOnInitialConnectError(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{err: errors.New("Post https://example.com/v1/chat/completions: EOF")},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "recovered"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	var reconnectMsgs []string
	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventReconnect {
				reconnectMsgs = append(reconnectMsgs, ev.Content)
			}
		},
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
	if len(reconnectMsgs) != 1 {
		t.Fatalf("expected 1 reconnect event, got %d", len(reconnectMsgs))
	}
}

func TestStreamRunner_RetriesOnIncompleteStreamBeforeOutput(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: nil},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "recovered"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	var reconnectMsgs []string
	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventReconnect {
				reconnectMsgs = append(reconnectMsgs, ev.Content)
			}
		},
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
	if len(reconnectMsgs) != 1 {
		t.Fatalf("expected 1 reconnect event, got %d", len(reconnectMsgs))
	}
}

func TestStreamRunner_EmitsStructuredLifecycleEvents(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{err: errors.New("EOF")},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "ok"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	var lifecycle []*providers.StreamLifecycle
	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
		OnEvent: func(ev providers.StreamEvent) {
			if ev.Type == providers.EventLifecycle && ev.Lifecycle != nil {
				lifecycle = append(lifecycle, ev.Lifecycle)
			}
		},
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
	if len(lifecycle) < 4 {
		t.Fatalf("expected >= 4 lifecycle events, got %d", len(lifecycle))
	}
	if lifecycle[0].Phase != providers.StreamPhaseConnecting || lifecycle[0].Attempt != 1 {
		t.Fatalf("unexpected first lifecycle event: %+v", lifecycle[0])
	}
	if lifecycle[1].Phase != providers.StreamPhaseReconnecting || lifecycle[1].RetryCount != 1 {
		t.Fatalf("unexpected reconnect lifecycle event: %+v", lifecycle[1])
	}
	if lifecycle[1].Budget <= 0 {
		t.Fatalf("expected positive budget in reconnect event, got %s", lifecycle[1].Budget)
	}
	if lifecycle[2].Phase != providers.StreamPhaseConnecting {
		t.Fatalf("unexpected second connecting event: %+v", lifecycle[2])
	}
	if lifecycle[3].Phase != providers.StreamPhaseConnected {
		t.Fatalf("unexpected connected lifecycle event: %+v", lifecycle[3])
	}
}

func TestStreamRunner_RetryOnInitialConnectHTTP500(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{err: &providers.HTTPError{StatusCode: 500, Body: "upstream error"}},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "recovered"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunner_RetryOnEarlyStreamErrorEvent(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{
				events: []providers.StreamEvent{
					{Type: providers.EventError, Error: errors.New("connection reset by peer")},
				},
			},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "ok"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunner_DoesNotRetryAfterPartialOutput(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "partial"},
					{Type: providers.EventError, Error: errors.New("EOF")},
				},
			},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "second"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
	}

	_, err := runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected stream error")
	}
	if client.callCount != 1 {
		t.Fatalf("expected no reconnect after partial output, got %d attempts", client.callCount)
	}
}

func TestStreamRunner_DoesNotRetryIncompleteStreamAfterPartialOutput(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "partial"},
				},
			},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "second"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
	}

	_, err := runner.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected stream error")
	}
	if client.callCount != 1 {
		t.Fatalf("expected no reconnect after partial output, got %d attempts", client.callCount)
	}
}

func TestStreamRunner_AcceptsHistory(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "turn2 reply"},
			{Type: providers.EventDone},
		},
	}

	history := []providers.ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
		{Role: "user", Content: "follow up"},
	}

	runner := StreamRunner{Client: client, Model: "test-model"}
	res, err := runner.RunWithCallback(context.Background(), history, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	result := res.Content
	newMsgs := res.NewMessages
	if result != "turn2 reply" {
		t.Fatalf("unexpected result: %q", result)
	}

	// All history messages should have been sent to the API.
	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	sent := client.requests[0].Messages
	if len(sent) != len(history) {
		t.Fatalf("expected %d messages sent, got %d", len(history), len(sent))
	}
	for i, msg := range history {
		if sent[i].Role != msg.Role || sent[i].Content != msg.Content {
			t.Fatalf("message %d mismatch: got %+v, want %+v", i, sent[i], msg)
		}
	}

	// newMsgs should contain exactly the assistant reply.
	if len(newMsgs) != 1 {
		t.Fatalf("expected 1 new message, got %d", len(newMsgs))
	}
	if newMsgs[0].Role != "assistant" {
		t.Fatalf("expected assistant message, got %q", newMsgs[0].Role)
	}
	if newMsgs[0].Content != "turn2 reply" {
		t.Fatalf("unexpected new message content: %q", newMsgs[0].Content)
	}
}

func TestStreamRunner_FiltersSystemReminderHistoryAndEvents(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{Type: providers.EventDone, StopReason: "end_turn"},
		},
	}

	history := []providers.ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello"},
		{Role: "user", Name: wuucontext.SystemReminderMessageName, Content: "<system-reminder>\n# Environment\n- CWD: /tmp\n</system-reminder>"},
	}

	var received []providers.StreamEvent
	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
		BeforeStep: func() []providers.ChatMessage {
			return []providers.ChatMessage{{
				Role:    "user",
				Name:    wuucontext.SystemReminderMessageName,
				Content: "<system-reminder>\n# Environment\n- CWD: /tmp\n</system-reminder>",
			}}
		},
	}

	res, err := runner.RunWithCallback(context.Background(), history, func(ev providers.StreamEvent) {
		received = append(received, ev)
	})
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}

	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	sent := client.requests[0].Messages
	if len(sent) != 2 {
		t.Fatalf("expected reminder messages to be filtered from request, got %+v", sent)
	}
	for _, msg := range sent {
		if wuucontext.IsSystemReminder(msg.Name, msg.Content) {
			t.Fatalf("unexpected system reminder in request: %+v", msg)
		}
	}

	if len(res.NewMessages) != 0 {
		t.Fatalf("expected no persisted messages from reminder-only turn, got %+v", res.NewMessages)
	}
	for _, ev := range received {
		if ev.Type == providers.EventMessage && ev.Message != nil && wuucontext.IsSystemReminder(ev.Message.Name, ev.Message.Content) {
			t.Fatalf("unexpected reminder event: %+v", ev)
		}
	}
}

func TestStreamRunner_ReusesUsageAcrossTurnsForPreRequestCompact(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "turn1"},
				{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 950}},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "turn2"},
				{Type: providers.EventDone},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "summarized"},
		},
	}

	runner := StreamRunner{
		Client:                client,
		Model:                 "test-model",
		ContextWindowOverride: 1000,
	}

	firstHistory := []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}
	first, err := runner.RunWithCallback(context.Background(), firstHistory, nil)
	if err != nil {
		t.Fatalf("first RunWithCallback: %v", err)
	}

	secondHistory := append([]providers.ChatMessage{}, firstHistory...)
	secondHistory = append(secondHistory, first.NewMessages...)
	secondHistory = append(secondHistory, providers.ChatMessage{Role: "user", Content: "follow up"})

	_, err = runner.RunWithCallback(context.Background(), secondHistory, nil)
	if err != nil {
		t.Fatalf("second RunWithCallback: %v", err)
	}

	if len(client.requests) != 3 {
		t.Fatalf("expected 3 total requests (stream, compact, stream), got %d", len(client.requests))
	}
	if len(client.requests[2].Messages) >= len(secondHistory) {
		t.Fatalf("expected compacted second request, got %d messages from %d-history input",
			len(client.requests[2].Messages), len(secondHistory))
	}
	if got := client.requests[2].Messages[0].Content; got != "[Conversation summary]\nsummarized" {
		t.Fatalf("expected compacted root summary, got %q", got)
	}
}

func TestStreamRunner_CancelledCtxStopsRetry(t *testing.T) {
	// When the parent context is already cancelled, the stream runner
	// should bail immediately instead of retrying retryable errors.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{err: context.DeadlineExceeded},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "should not reach"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
	}

	_, err := runner.Run(ctx, "hi")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if client.callCount > 0 {
		t.Fatalf("expected 0 stream attempts on cancelled ctx, got %d", client.callCount)
	}
}

func TestStreamRunner_RetryOnDeadlineExceeded(t *testing.T) {
	// context.DeadlineExceeded should be retried when the parent ctx is alive.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{err: context.DeadlineExceeded},
			{
				events: []providers.StreamEvent{
					{Type: providers.EventContentDelta, Content: "recovered"},
					{Type: providers.EventDone},
				},
			},
		},
	}

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamReconnectBudget:   time.Second,
		StreamRetryInitialDelay: time.Millisecond,
		StreamRetryMaxDelay:     2 * time.Millisecond,
	}

	result, err := runner.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("unexpected result: %q", result)
	}
	if client.callCount != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunner_ZeroMaxStepsIsUnlimited(t *testing.T) {
	// Regression: previously MaxSteps == 0 was silently coerced to 8,
	// which broke long coordinator sessions. With the fix, 0 means
	// unlimited and a 12-round tool-call run completes successfully.
	const rounds = 12

	attempts := make([]mockStreamAttempt, 0, rounds+1)
	for i := 0; i < rounds; i++ {
		id := fmt.Sprintf("call_%d", i)
		attempts = append(attempts, mockStreamAttempt{
			events: []providers.StreamEvent{
				{
					Type:     providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{ID: id, Name: "run_shell"},
				},
				{
					Type: providers.EventToolUseEnd,
					ToolCall: &providers.ToolCall{
						ID: id, Name: "run_shell",
						Arguments: `{"command":"echo hi"}`,
					},
				},
				{Type: providers.EventDone},
			},
		})
	}
	// Final round: content only, no tool calls — runner exits cleanly.
	attempts = append(attempts, mockStreamAttempt{
		events: []providers.StreamEvent{
			{Type: providers.EventContentDelta, Content: "all done"},
			{Type: providers.EventDone},
		},
	})

	client := &mockStreamClient{attempts: attempts}
	tools := &fakeTools{}
	runner := StreamRunner{
		Client: client,
		Tools:  tools,
		Model:  "test-model",
		// MaxSteps left at zero — must mean "no cap".
	}

	out, err := runner.Run(context.Background(), "long task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "all done" {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(tools.calls) != rounds {
		t.Fatalf("expected %d tool calls, got %d", rounds, len(tools.calls))
	}
}

func TestStreamRunner_ReplaysReasoningContentAfterToolCall(t *testing.T) {
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventThinkingDelta, Content: "inspect repo before tool use"},
				{
					Type: providers.EventThinkingDone,
					ReasoningBlock: &providers.ReasoningBlock{
						Type:      "thinking",
						Thinking:  "inspect repo before tool use",
						Signature: "sig_1",
					},
				},
				{
					Type:     providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{ID: "call_1", Name: "list_files"},
				},
				{
					Type: providers.EventToolUseEnd,
					ToolCall: &providers.ToolCall{
						ID:        "call_1",
						Name:      "list_files",
						Arguments: `{}`,
					},
				},
				{Type: providers.EventDone},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "done"},
				{Type: providers.EventDone},
			}},
		},
	}

	runner := StreamRunner{
		Client: client,
		Tools:  &fakeTools{},
		Model:  "test-model",
	}

	out, err := runner.Run(context.Background(), "inspect this repo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "done" {
		t.Fatalf("unexpected output: %q", out)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(client.requests))
	}
	second := client.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("expected user + assistant + tool in second request, got %d", len(second))
	}
	assistant := second[1]
	if assistant.Role != "assistant" {
		t.Fatalf("expected assistant message, got %+v", assistant)
	}
	if assistant.ReasoningContent != "inspect repo before tool use" {
		t.Fatalf("unexpected reasoning content replay: %q", assistant.ReasoningContent)
	}
	if len(assistant.ReasoningBlocks) != 1 || assistant.ReasoningBlocks[0].Signature != "sig_1" {
		t.Fatalf("unexpected reasoning blocks replay: %+v", assistant.ReasoningBlocks)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls on assistant replay: %+v", assistant.ToolCalls)
	}
}

func TestStreamRunner_TruncationRecovery(t *testing.T) {
	// Three rounds: first two are truncated content-only (no tool calls)
	// with stop_reason=length; the third returns the final answer.
	// The shared loop should concatenate them and surface a clean
	// result via RunWithCallback.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "part1 "},
				{Type: providers.EventDone, StopReason: "length", Truncated: true},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "part2 "},
				{Type: providers.EventDone, StopReason: "max_tokens", Truncated: true},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "done."},
				{Type: providers.EventDone},
			}},
		},
	}

	runner := StreamRunner{
		Client: client,
		Model:  "test-model",
	}

	out, err := runner.Run(context.Background(), "long output")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "part1 part2 done." {
		t.Fatalf("expected concatenated parts, got %q", out)
	}
	if client.callCount != 3 {
		t.Fatalf("expected 3 stream attempts, got %d", client.callCount)
	}
}

func TestStreamRunner_MaxStepsExceeded(t *testing.T) {
	client := &mockStreamClient{
		events: []providers.StreamEvent{
			{
				Type: providers.EventToolUseStart,
				ToolCall: &providers.ToolCall{
					ID:   "call_1",
					Name: "run_shell",
				},
			},
			{
				Type: providers.EventToolUseEnd,
				ToolCall: &providers.ToolCall{
					ID:        "call_1",
					Name:      "run_shell",
					Arguments: `{"command":"echo hi"}`,
				},
			},
			{Type: providers.EventDone},
		},
	}

	tools := &fakeTools{}
	runner := StreamRunner{
		Client:   client,
		Tools:    tools,
		Model:    "test-model",
		MaxSteps: 2,
	}

	_, err := runner.Run(context.Background(), "loop")
	if err == nil {
		t.Fatal("expected max steps error")
	}
	if len(tools.calls) != 2 {
		t.Fatalf("expected 2 tool executions, got %d", len(tools.calls))
	}
}

func TestStreamRunner_NonStreamingFallbackOnEmptyStream(t *testing.T) {
	// Stream returns empty content with no stop reason (proxy issue),
	// but the non-streaming Chat() fallback succeeds.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventDone, StopReason: ""},
			}},
		},
		chatResponses: []providers.ChatResponse{
			{Content: "fallback answer", StopReason: "stop"},
		},
	}
	runner := &StreamRunner{
		Client:              client,
		Model:               "test",
		StreamRetryMaxDelay: time.Millisecond,
	}
	result, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fallback answer" {
		t.Fatalf("expected fallback answer, got %q", result)
	}
	if client.chatCallCount != 1 {
		t.Fatalf("expected 1 Chat() call, got %d", client.chatCallCount)
	}
}

func TestStreamRunner_NoFallbackOnNormalStop(t *testing.T) {
	// Stream returns empty content with stop_reason=stop — this is a
	// legitimate model choice, not a proxy issue. No fallback.
	client := &mockStreamClient{
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventDone, StopReason: "stop"},
			}},
		},
	}
	runner := &StreamRunner{
		Client:              client,
		Model:               "test",
		StreamRetryMaxDelay: time.Millisecond,
	}
	_, err := runner.Run(context.Background(), "hello")
	// Should produce an EmptyAnswerError (from the loop), not trigger fallback.
	if err == nil {
		t.Fatal("expected error for empty content with stop reason")
	}
	if !IsEmptyAnswer(err) {
		t.Fatalf("expected EmptyAnswerError, got %v", err)
	}
	if client.chatCallCount != 0 {
		t.Fatalf("expected 0 Chat() calls (no fallback), got %d", client.chatCallCount)
	}
}
