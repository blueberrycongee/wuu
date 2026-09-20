package providers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type reliableStreamAttempt struct {
	events []StreamEvent
	err    error
}

type reliableStreamMockClient struct {
	attempts  []reliableStreamAttempt
	callCount int
}

func (m *reliableStreamMockClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, nil
}

func (m *reliableStreamMockClient) StreamChat(_ context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	idx := m.callCount
	m.callCount++
	if idx >= len(m.attempts) {
		return nil, errors.New("unexpected extra stream attempt")
	}
	attempt := m.attempts[idx]
	if attempt.err != nil {
		return nil, attempt.err
	}
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
	}
	ch := make(chan StreamEvent, len(attempt.events))
	for _, ev := range attempt.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func reliableTestRetryWait(context.Context, time.Duration) error {
	return nil
}

func newReliableTestClient(inner StreamClient, onRetry StreamRetryHook, options ...ReliableStreamOption) *ReliableStreamClient {
	options = append(options, WithStreamRetryWait(reliableTestRetryWait))
	return NewReliableStreamClient(inner, onRetry, options...)
}

func reliableTestRequest() ChatRequest {
	return ChatRequest{Operation: NewInferenceOperation(InferenceOperationAuxiliary, InferenceProfileBestEffort)}
}

func collectReliableEvents(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

func eventTypes(events []StreamEvent) []StreamEventType {
	out := make([]StreamEventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func nonLifecycleEvents(events []StreamEvent) []StreamEvent {
	out := make([]StreamEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type != EventLifecycle {
			out = append(out, ev)
		}
	}
	return out
}

func lifecycleEvents(events []StreamEvent) []*StreamLifecycle {
	out := make([]*StreamLifecycle, 0, len(events))
	for _, ev := range events {
		if ev.Type == EventLifecycle && ev.Lifecycle != nil {
			out = append(out, ev.Lifecycle)
		}
	}
	return out
}

func TestReliableStreamClientSingleSuccess(t *testing.T) {
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{{events: []StreamEvent{
		{Type: EventContentDelta, Content: "ok"},
		{Type: EventDone},
	}}}}
	client := newReliableTestClient(inner, nil)
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := collectReliableEvents(t, ch)
	if got, want := eventTypes(nonLifecycleEvents(events)), []StreamEventType{EventContentDelta, EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	lifecycle := lifecycleEvents(events)
	if len(lifecycle) != 2 || lifecycle[0].Phase != StreamPhaseConnecting || lifecycle[1].Phase != StreamPhaseConnected {
		t.Fatalf("lifecycle = %+v, want connecting then connected", lifecycle)
	}
	if lifecycle[0].OperationID == "" || lifecycle[0].AttemptID == "" || lifecycle[0].AttemptID != lifecycle[1].AttemptID {
		t.Fatalf("lifecycle identity missing or unstable: %+v", lifecycle)
	}
	if lifecycle[0].Attempt != 1 || lifecycle[0].MaxAttempts != 4 {
		t.Fatalf("initial attempt = %+v, want 1/4", lifecycle[0])
	}
	if lifecycle[1].SubmissionCount != 1 || lifecycle[1].SubmissionID == "" {
		t.Fatalf("connected lifecycle missing submission: %+v", lifecycle[1])
	}
	if inner.callCount != 1 {
		t.Fatalf("attempts = %d, want 1", inner.callCount)
	}
}

func TestReliableStreamClientRetriesStreamErrorsThenSucceeds(t *testing.T) {
	retryErr := NewIncompleteStreamError("temporary drop")
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
		{events: []StreamEvent{{Type: EventContentDelta, Content: "ok"}, {Type: EventDone}}},
	}}
	var attempts []int
	client := newReliableTestClient(inner, func(attempt, _ int, _ error, _ time.Duration) {
		attempts = append(attempts, attempt)
	})
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := collectReliableEvents(t, ch)
	if got, want := eventTypes(nonLifecycleEvents(events)), []StreamEventType{EventContentDelta, EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(attempts, []int{1, 2}) {
		t.Fatalf("retry attempts = %v", attempts)
	}
}

func TestReliableStreamClientRetriesConnectFailure(t *testing.T) {
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{err: &HTTPError{StatusCode: 500, Body: "upstream"}},
		{events: []StreamEvent{{Type: EventContentDelta, Content: "ok"}, {Type: EventDone}}},
	}}
	var retries int
	client := newReliableTestClient(inner, func(int, int, error, time.Duration) { retries++ })
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := collectReliableEvents(t, ch)
	if got, want := eventTypes(nonLifecycleEvents(events)), []StreamEventType{EventContentDelta, EventDone}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if retries != 1 || inner.callCount != 2 {
		t.Fatalf("retries=%d attempts=%d, want 1/2", retries, inner.callCount)
	}
	lifecycle := lifecycleEvents(events)
	wantPhases := []StreamLifecyclePhase{StreamPhaseConnecting, StreamPhaseReconnecting, StreamPhaseConnecting, StreamPhaseConnected}
	if len(lifecycle) != len(wantPhases) {
		t.Fatalf("lifecycle = %+v, want phases %v", lifecycle, wantPhases)
	}
	for i, want := range wantPhases {
		if lifecycle[i].Phase != want {
			t.Fatalf("lifecycle[%d] = %+v, want phase %s", i, lifecycle[i], want)
		}
	}
	if lifecycle[1].Attempt != 2 || lifecycle[1].RetryCount != 1 || lifecycle[1].ResetPartial {
		t.Fatalf("connect-failure retry lifecycle = %+v", lifecycle[1])
	}
}

func TestReliableStreamClientRetriesTypedGatewayFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "gateway timeout", err: &HTTPError{ProviderFamily: "openai", StatusCode: 504, Body: "gateway timeout"}},
		{name: "openai compatible route miss", err: &HTTPError{ProviderFamily: "openai", StatusCode: 404, Body: `{"error":"temporary route miss"}`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
				{err: test.err},
				{events: []StreamEvent{{Type: EventContentDelta, Content: "recovered"}, {Type: EventDone}}},
			}}
			client := newReliableTestClient(inner, nil)
			ch, err := client.StreamChat(context.Background(), reliableTestRequest())
			if err != nil {
				t.Fatalf("StreamChat: %v", err)
			}
			events := nonLifecycleEvents(collectReliableEvents(t, ch))
			if inner.callCount != 2 || len(events) != 2 || events[0].Content != "recovered" {
				t.Fatalf("attempts/events = %d/%+v", inner.callCount, events)
			}
		})
	}
}

func TestReliableStreamClientEmitsFinalErrorAfterMaxRetries(t *testing.T) {
	retryErr := NewIncompleteStreamError("temporary drop")
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
	}}
	var attempts []int
	client := newReliableTestClient(inner, func(attempt, _ int, _ error, _ time.Duration) {
		attempts = append(attempts, attempt)
	})
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := nonLifecycleEvents(collectReliableEvents(t, ch))
	if len(events) != 1 || events[0].Type != EventError || events[0].Error == nil {
		t.Fatalf("events = %+v, want final error", events)
	}
	if !reflect.DeepEqual(attempts, []int{1, 2, 3}) {
		t.Fatalf("retry attempts = %v", attempts)
	}
	var terminal *StreamRecoveryError
	if !errors.As(events[0].Error, &terminal) || !errors.Is(events[0].Error, retryErr) {
		t.Fatalf("final error lost cause/diagnostics: %v", events[0].Error)
	}
	if got := terminal.Recovery; got.AttemptCount != inner.callCount || got.RetryCount != 3 || got.SubmissionCount != 4 || got.StopReason != "retry_limit" {
		t.Fatalf("terminal recovery = %+v", got)
	}
}

func TestReliableStreamClientHonorsFrozenOperationAttemptLimit(t *testing.T) {
	retryErr := NewIncompleteStreamError("temporary drop")
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: retryErr}}},
		{events: []StreamEvent{{Type: EventDone}}},
	}}
	retries := 0
	client := newReliableTestClient(inner, func(int, int, error, time.Duration) { retries++ })
	req := reliableTestRequest()
	req.Operation.AttemptLimit = 1
	ch, err := client.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := collectReliableEvents(t, ch)
	if retries != 0 || inner.callCount != 1 {
		t.Fatalf("retries=%d attempts=%d, want frozen single attempt", retries, inner.callCount)
	}
	var failed *StreamLifecycle
	for _, lifecycle := range lifecycleEvents(events) {
		if lifecycle.Phase == StreamPhaseReconnecting {
			t.Fatalf("single-attempt operation emitted reconnecting lifecycle: %+v", lifecycle)
		}
		if lifecycle.Phase == StreamPhaseFailed {
			failed = lifecycle
		}
	}
	if failed == nil || failed.MaxAttempts != 1 || failed.MaxRetries != 0 {
		t.Fatalf("failed lifecycle = %+v, want frozen 1/0 limit", failed)
	}
}

func TestReliableStreamClientContextCancelStopsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: NewIncompleteStreamError("temporary drop")}}},
		{events: []StreamEvent{{Type: EventDone}}},
	}}
	client := newReliableTestClient(inner, func(int, int, error, time.Duration) { cancel() })
	ch, err := client.StreamChat(ctx, reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if events := nonLifecycleEvents(collectReliableEvents(t, ch)); len(events) != 0 {
		t.Fatalf("events = %+v, want closed without error", events)
	}
}

func TestReliableStreamClientTerminalizesPreparedAttemptWhenWaitCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	journal := &recordingInferenceJournal{}
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: NewIncompleteStreamError("temporary drop")}}},
		{events: []StreamEvent{{Type: EventDone}}},
	}}
	client := NewReliableStreamClient(inner, nil, WithStreamRetryWait(func(context.Context, time.Duration) error {
		cancel()
		return nil
	}))
	ch, err := client.StreamChat(WithInferenceJournal(ctx, journal), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	_ = collectReliableEvents(t, ch)
	if inner.callCount != 1 {
		t.Fatalf("stream calls = %d, want no send from prepared recovery attempt", inner.callCount)
	}
	if got := strings.Join(journal.events, "|"); !strings.Contains(got, "recovery:replay_same_payload|attempt:prepared|attempt:canceled") {
		t.Fatalf("prepared recovery attempt was not terminalized: %q", got)
	}
}

func TestReliableStreamClientDoesNotRetryNonRetryableError(t *testing.T) {
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: NewNonRetryableStreamError("terminal")}}},
		{events: []StreamEvent{{Type: EventDone}}},
	}}
	var retries int
	client := newReliableTestClient(inner, func(int, int, error, time.Duration) { retries++ })
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := nonLifecycleEvents(collectReliableEvents(t, ch))
	if len(events) != 1 || events[0].Type != EventError {
		t.Fatalf("events = %+v, want final error", events)
	}
	if retries != 0 || inner.callCount != 1 {
		t.Fatalf("retries=%d attempts=%d, want 0/1", retries, inner.callCount)
	}
}

func TestReliableStreamClientAppliesBackoffBeforeReconnect(t *testing.T) {
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{err: &HTTPError{StatusCode: 500, Body: "upstream"}},
		{events: []StreamEvent{{Type: EventDone}}},
	}}
	var delays []time.Duration
	var callsObservedAtWait []int
	client := NewReliableStreamClient(
		inner,
		nil,
		WithStreamRetryWait(func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			callsObservedAtWait = append(callsObservedAtWait, inner.callCount)
			return nil
		}),
	)
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	_ = collectReliableEvents(t, ch)

	if inner.callCount != 2 {
		t.Fatalf("stream calls = %d, want 2", inner.callCount)
	}
	if !reflect.DeepEqual(callsObservedAtWait, []int{1}) {
		t.Fatalf("calls observed when waiting = %v, want [1]", callsObservedAtWait)
	}
	if len(delays) != 1 || delays[0] < time.Second || delays[0] > 1250*time.Millisecond {
		t.Fatalf("first reconnect delay = %v, want exponential backoff in [1s, 1.25s]", delays)
	}
}

func TestReliableStreamClientDoesNotReplayLocalBackpressure(t *testing.T) {
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventError, Error: &LocalBackpressureError{Component: "test reader"}}}},
		{events: []StreamEvent{{Type: EventDone}}},
	}}
	client := newReliableTestClient(inner, nil)
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	events := nonLifecycleEvents(collectReliableEvents(t, ch))
	if inner.callCount != 1 || len(events) != 1 || events[0].Type != EventError {
		t.Fatalf("attempts/events = %d/%+v, want local failure without replay", inner.callCount, events)
	}
}

func TestReliableStreamClientRetriesTruncatedStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"local close", nil},
		{"provider timeout", NewProviderStreamError("request_timeout", "stream error: stream disconnected before completion: stream closed before response.completed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			partial := []StreamEvent{{Type: EventContentDelta, Content: "partial"}}
			if tc.err != nil {
				partial = append(partial, StreamEvent{Type: EventError, Error: tc.err})
			}
			inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
				{events: partial},
				{events: []StreamEvent{{Type: EventContentDelta, Content: "ok"}, {Type: EventDone}}},
			}}
			client := newReliableTestClient(inner, nil)
			ch, err := client.StreamChat(context.Background(), reliableTestRequest())
			if err != nil {
				t.Fatalf("StreamChat: %v", err)
			}
			events := collectReliableEvents(t, ch)
			if got, want := eventTypes(nonLifecycleEvents(events)), []StreamEventType{EventContentDelta, EventContentDelta, EventDone}; !reflect.DeepEqual(got, want) {
				t.Fatalf("events = %v, want %v", got, want)
			}
			businessEvents := nonLifecycleEvents(events)
			if businessEvents[0].Content != "partial" || businessEvents[1].Content != "ok" {
				t.Fatalf("unexpected content events: %+v", businessEvents)
			}
			lifecycle := lifecycleEvents(events)
			var reconnect *StreamLifecycle
			for _, event := range lifecycle {
				if event.Phase == StreamPhaseReconnecting {
					reconnect = event
					break
				}
			}
			if reconnect == nil || !reconnect.ResetPartial || reconnect.Attempt != 2 {
				t.Fatalf("partial retry lifecycle = %+v, want reset for attempt 2", reconnect)
			}
		})
	}
}

func TestReliableStreamClientReplayGuardBlocksUnsafeRetry(t *testing.T) {
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{
		{events: []StreamEvent{{Type: EventContentDelta, Content: "partial"}, {Type: EventError, Error: NewIncompleteStreamError("temporary drop")}}},
		{events: []StreamEvent{{Type: EventContentDelta, Content: "must not run"}, {Type: EventDone}}},
	}}
	guardReason := errors.New("tool already started")
	client := newReliableTestClient(
		inner,
		nil,
		WithStreamReplayGuard(func(ctx StreamRetryContext) error {
			if ctx.Attempt != 1 || ctx.ForwardedEvents != 1 || ctx.Operation.ID == "" {
				t.Fatalf("retry context = %+v", ctx)
			}
			return guardReason
		}),
	)
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	allEvents := collectReliableEvents(t, ch)
	events := nonLifecycleEvents(allEvents)
	if inner.callCount != 1 {
		t.Fatalf("attempts = %d, want replay blocked after first", inner.callCount)
	}
	if len(events) != 2 || events[0].Content != "partial" || events[1].Type != EventError {
		t.Fatalf("events = %+v", events)
	}
	var blocked *ReplayBlockedError
	if !errors.As(events[1].Error, &blocked) || !errors.Is(blocked.Reason, guardReason) {
		t.Fatalf("error = %v, want ReplayBlockedError", events[1].Error)
	}
	if IsRetryable(events[1].Error) {
		t.Fatalf("replay-blocked error must be terminal: %v", events[1].Error)
	}
	var failed *StreamLifecycle
	for _, lifecycle := range lifecycleEvents(allEvents) {
		if lifecycle.Phase == StreamPhaseFailed {
			failed = lifecycle
		}
	}
	if failed == nil || failed.FailureCategory != string(FailureReplayUnsafe) {
		t.Fatalf("typed failed lifecycle = %+v", failed)
	}
}
