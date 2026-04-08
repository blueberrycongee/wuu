package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type mockStreamAttempt struct {
	events []providers.StreamEvent
	err    error
}

type mockStreamClient struct {
	events    []providers.StreamEvent
	attempts  []mockStreamAttempt
	requests  []providers.ChatRequest
	callCount int
}

func (m *mockStreamClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	m.requests = append(m.requests, req)
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

	// OnEvent should receive all events.
	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].Type != providers.EventContentDelta || received[0].Content != "Hello " {
		t.Fatalf("unexpected first event: %+v", received[0])
	}
	if received[2].Type != providers.EventDone {
		t.Fatalf("expected last event to be done, got %s", received[2].Type)
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
	_, _, err = runner.RunWithCallback(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil client in RunWithCallback")
	}

	runner = StreamRunner{Client: client}
	_, _, err = runner.RunWithCallback(context.Background(), nil, nil)
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

	runner := StreamRunner{
		Client:                  client,
		Model:                   "m",
		StreamMaxRetries:        2,
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
		StreamMaxRetries:        2,
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
		StreamMaxRetries:        2,
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
	result, newMsgs, err := runner.RunWithCallback(context.Background(), history, nil)
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
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
