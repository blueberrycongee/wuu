package providers

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The tests in this file pin down how the reliable layer defends itself
// against providers that violate the StreamClient contract: returning a nil
// channel, stalling without sending or closing, ignoring cancellation, or
// emitting events after EventDone.

// nilChannelStreamClient returns (nil, nil) from StreamChat.
type nilChannelStreamClient struct{}

func (nilChannelStreamClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (nilChannelStreamClient) StreamChat(context.Context, ChatRequest) (<-chan StreamEvent, error) {
	return nil, nil
}

func TestReliableStreamClientRejectsNilChannel(t *testing.T) {
	client := newReliableTestClient(nilChannelStreamClient{}, nil)
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	done := make(chan []StreamEvent, 1)
	go func() { done <- collectReliableEvents(t, ch) }()
	select {
	case events := <-done:
		final := events[len(events)-1]
		if final.Type != EventError || final.Error == nil {
			t.Fatalf("final event = %+v, want EventError", final)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reliable layer hung on a nil provider channel")
	}
}

// stalledStreamClient returns a channel and then does nothing: no events, no
// close, no reaction to context cancellation.
type stalledStreamClient struct{}

func (stalledStreamClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (stalledStreamClient) StreamChat(context.Context, ChatRequest) (<-chan StreamEvent, error) {
	return make(chan StreamEvent), nil
}

func TestReliableStreamClientCancelUnblocksStalledProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newReliableTestClient(stalledStreamClient{}, nil)
	ch, err := client.StreamChat(ctx, reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	closed := make(chan struct{})
	go func() {
		for range ch {
		}
		close(closed)
	}()
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not end the stream of a stalled provider")
	}
}

// trailingErrorStreamClient completes the response and then reports a
// transport error before closing, like an unclean close after message_stop.
type trailingErrorStreamClient struct {
	callCount int
}

func (m *trailingErrorStreamClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (m *trailingErrorStreamClient) StreamChat(_ context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	m.callCount++
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
	}
	ch := make(chan StreamEvent, 4)
	ch <- StreamEvent{Type: EventContentDelta, Content: "answer"}
	ch <- StreamEvent{Type: EventDone}
	ch <- StreamEvent{Type: EventError, Error: NewIncompleteStreamError("connection reset after done")}
	close(ch)
	return ch, nil
}

func TestReliableStreamClientTreatsDoneAsTerminal(t *testing.T) {
	inner := &trailingErrorStreamClient{}
	client := newReliableTestClient(inner, nil)
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := collectReliableEvents(t, ch)
	var doneCount, errorCount int
	for _, ev := range nonLifecycleEvents(events) {
		switch ev.Type {
		case EventDone:
			doneCount++
		case EventError:
			errorCount++
		}
	}
	if doneCount != 1 || errorCount != 0 {
		t.Fatalf("done=%d errors=%d, want exactly one done and no error after it", doneCount, errorCount)
	}
	if inner.callCount != 1 {
		t.Fatalf("attempts = %d; a completed response must not be replayed over a trailing error", inner.callCount)
	}
}

// floodingStreamClient writes far more events than the channel buffer with
// bare sends after signalling readiness, then closes. It simulates a fast
// producer that is mid-send when the consumer abandons the attempt.
type floodingStreamClient struct {
	producerDone chan struct{}
}

func (m *floodingStreamClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (m *floodingStreamClient) StreamChat(_ context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
	}
	ch := make(chan StreamEvent, 4)
	go func() {
		defer close(ch)
		defer close(m.producerDone)
		for i := 0; i < 500; i++ {
			ch <- StreamEvent{Type: EventContentDelta, Content: "x"}
		}
		ch <- StreamEvent{Type: EventDone}
	}()
	return ch, nil
}

func TestReliableStreamClientDrainsAbandonedAttempt(t *testing.T) {
	inner := &floodingStreamClient{producerDone: make(chan struct{})}
	observerErr := errors.New("durable admission failed")
	fired := false
	client := newReliableTestClient(inner, nil, WithStreamEventObserver(func(context.Context, StreamEvent) error {
		if fired {
			return observerErr
		}
		fired = true
		return observerErr
	}))
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := collectReliableEvents(t, ch)
	final := events[len(events)-1]
	if final.Type != EventError {
		t.Fatalf("final event = %+v, want EventError from the observer", final)
	}
	// The producer uses bare sends and a small buffer; without draining it
	// would block forever on `ch <-` and leak (with the lease it holds).
	select {
	case <-inner.producerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("producer still blocked after the attempt was abandoned; abandoned channel was not drained")
	}
}
