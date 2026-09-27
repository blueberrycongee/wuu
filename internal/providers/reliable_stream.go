package providers

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// StreamRetryHook is called just before each reconnect attempt.
// attempt is 1-based (first retry = 1). maxRetries is the configured cap.
// err is the transport error that triggered the retry.
// retryIn is the computed backoff delay before the next connect.
type StreamRetryHook func(attempt, maxRetries int, err error, retryIn time.Duration)

// StreamRetryContext is the evidence available immediately before a retry.
// A guard can use it to reject a replay that would duplicate external effects.
type StreamRetryContext struct {
	Operation          InferenceOperation
	Attempt            int
	ForwardedEvents    int
	FinalizedToolCalls []ToolCall
	Err                error
}

// StreamReplayGuard returns nil when replay is safe. A non-nil result blocks
// the retry and is surfaced as ReplayBlockedError.
type StreamReplayGuard func(StreamRetryContext) error

// StreamEventObserver runs synchronously before an event is forwarded to the
// consumer. It is used for durable admission that must be visible to the
// replay guard before the provider goroutine can start another attempt.
type StreamEventObserver func(context.Context, StreamEvent) error

// ReliableStreamOption configures behavior that is orthogonal to retry count.
type ReliableStreamOption func(*ReliableStreamClient)

// WithStreamReplayGuard installs a safety fence evaluated before every retry.
func WithStreamReplayGuard(guard StreamReplayGuard) ReliableStreamOption {
	return func(client *ReliableStreamClient) {
		client.replayGuard = guard
	}
}

// WithStreamEventObserver installs a synchronous pre-forward observer.
func WithStreamEventObserver(observer StreamEventObserver) ReliableStreamOption {
	return func(client *ReliableStreamClient) {
		client.eventObserver = observer
	}
}

// WithStreamRetryWait replaces only the passage of backoff time. Recovery
// limits and delays still come from the operation workload profile, so this
// seam can make tests deterministic without creating a second policy source.
func WithStreamRetryWait(wait func(context.Context, time.Duration) error) ReliableStreamOption {
	return func(client *ReliableStreamClient) {
		if wait != nil {
			client.wait = wait
		}
	}
}

// ReliableStreamClient wraps a StreamClient and transparently retries dropped
// streams. Callers receive a single output channel; reconnectable inner errors
// are consumed internally and only the final unrecoverable error is forwarded.
type ReliableStreamClient struct {
	inner         StreamClient
	onRetry       StreamRetryHook
	replayGuard   StreamReplayGuard
	eventObserver StreamEventObserver
	wait          func(context.Context, time.Duration) error
}

// ReplayBlockedError reports a retryable transport failure that Wuu chose not
// to replay because an operation-specific safety fence rejected it.
type ReplayBlockedError struct {
	Cause  error
	Reason error
}

func (e *ReplayBlockedError) Error() string {
	if e == nil {
		return "automatic replay blocked"
	}
	if e.Reason == nil {
		return fmt.Sprintf("automatic replay blocked: %v", e.Cause)
	}
	return fmt.Sprintf("automatic replay blocked: %v (original stream failure: %v)", e.Reason, e.Cause)
}

func (e *ReplayBlockedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewReliableStreamClient constructs a ReliableStreamClient. Recovery policy
// is resolved from each request operation after request normalization.
func NewReliableStreamClient(inner StreamClient, onRetry StreamRetryHook, options ...ReliableStreamOption) *ReliableStreamClient {
	client := &ReliableStreamClient{
		inner:   inner,
		onRetry: onRetry,
		wait:    waitWithContext,
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}
	return client
}

// StreamChat opens one logical stream and reconnects the underlying transport
// when the provider/client reports a retryable failure before EventDone.
func (r *ReliableStreamClient) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, error) {
	if r == nil || r.inner == nil {
		return nil, errors.New("stream client is required")
	}
	out := make(chan StreamEvent)
	go r.run(ctx, req, out)
	return out, nil
}

func (r *ReliableStreamClient) run(ctx context.Context, req ChatRequest, out chan<- StreamEvent) {
	defer close(out)
	var err error
	attemptsStarted := 0
	reportFailure := func(err error, stopReason string) {
		r.send(ctx, out, StreamEvent{Type: EventError, Error: streamRecoveryError(err, req, attemptsStarted, stopReason)})
	}
	req, err = prepareInferenceRequest(ctx, r.inner, req)
	if err != nil {
		reportFailure(err, "recovery_failed")
		return
	}
	req, err = EnsureInferenceExecutionContext(ctx, req, InferenceOperationAuxiliary, InferenceProfileInteractive)
	if err != nil {
		reportFailure(err, "recovery_failed")
		return
	}
	operation := req.Operation
	cfg := NormalizeRetryConfig(RetryConfigForProfile(operation.WorkloadProfile))
	startedAt := time.Now()
	maxAttempts := operation.AttemptLimit
	maxRetries := maxAttempts - 1
	var preparedNextAttempt InferenceAttempt
	for {
		if ctx.Err() != nil {
			if preparedNextAttempt.Valid() {
				_ = preparedNextAttempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(ctx.Err()))
			}
			return
		}
		attemptReq := req
		if preparedNextAttempt.Valid() {
			attemptReq.Attempt = preparedNextAttempt
			preparedNextAttempt = InferenceAttempt{}
		} else {
			var beginErr error
			attemptReq, beginErr = BeginInferenceAttemptContext(ctx, req, operation.Kind, operation.WorkloadProfile)
			if beginErr != nil {
				reportFailure(beginErr, "recovery_failed")
				return
			}
		}
		attempt := attemptReq.Attempt
		retriesUsed := attempt.Ordinal - 1
		if !r.sendLifecycle(ctx, out, attemptReq.Execution, operation, StreamPhaseConnecting, attempt.ID, attempt.Ordinal, maxAttempts, retriesUsed, maxRetries, nil, 0, false, startedAt) {
			_ = attempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(ctx.Err()))
			return
		}

		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		attemptsStarted++
		ch, err := r.inner.StreamChat(attemptCtx, attemptReq)
		if err == nil && ch == nil {
			err = errors.New("stream client returned a nil event channel")
		}
		forwardedEvents := 0
		var finalizedToolCalls []ToolCall
		if err == nil {
			var sawDone bool
			err, sawDone, forwardedEvents, finalizedToolCalls = r.forwardAttempt(attemptCtx, ch, out, attemptReq.Execution, operation, attempt, maxAttempts, retriesUsed, maxRetries, startedAt)
			// Cancel before draining: a producer blocked mid-send unblocks on
			// either the drain or its own ctx select, finishes, and releases
			// its lease through its defers.
			cancelAttempt()
			drainStream(ch)
			if ctx.Err() != nil {
				failure := NormalizeFailure(ctx.Err())
				_ = attempt.Complete(InferenceOutcomeCanceled, failure)
				return
			}
			if sawDone && err == nil {
				if journalErr := attempt.Complete(InferenceOutcomeSucceeded, NormalizedFailure{}); journalErr != nil {
					reportFailure(journalErr, "recovery_failed")
				}
				return
			}
			if err == nil {
				err = NewIncompleteStreamError("stream closed before done")
			}
		} else {
			cancelAttempt()
		}
		if err != nil {
			if journalErr := attempt.ObserveStreamEvent(StreamEvent{Type: EventError, Error: err}); journalErr != nil {
				err = errors.Join(err, journalErr)
			}
		}
		failure := NormalizeFailure(err)
		plan := PlanRecovery(failure)
		canRetry := ctx.Err() == nil && attempt.Ordinal < maxAttempts && streamRecoverySupported(r.inner, plan)
		if canRetry && r.replayGuard != nil {
			if guardErr := r.replayGuard(StreamRetryContext{
				Operation:          operation,
				Attempt:            attempt.Ordinal,
				ForwardedEvents:    forwardedEvents,
				FinalizedToolCalls: append([]ToolCall(nil), finalizedToolCalls...),
				Err:                err,
			}); guardErr != nil {
				err = &ReplayBlockedError{Cause: err, Reason: guardErr}
				canRetry = false
			}
		}
		if !canRetry {
			stopReason := "non_retryable"
			if plan.Retryable() {
				stopReason = "recovery_unavailable"
				if attempt.Ordinal >= maxAttempts {
					stopReason = "retry_limit"
				}
			}
			failure := NormalizeFailure(err)
			outcome := InferenceOutcomeFailed
			if failure.Category == FailureCanceled || failure.Category == FailureDeadline {
				outcome = InferenceOutcomeCanceled
			}
			if journalErr := attempt.Complete(outcome, failure); journalErr != nil {
				err = errors.Join(err, journalErr)
				failure = NormalizeFailure(journalErr)
				outcome = InferenceOutcomeFailed
				stopReason = "recovery_failed"
			}
			if !r.sendLifecycle(ctx, out, attemptReq.Execution, operation, StreamPhaseFailed, attempt.ID, attempt.Ordinal, maxAttempts, retriesUsed, maxRetries, err, 0, false, startedAt) {
				return
			}
			reportFailure(err, stopReason)
			return
		}

		delay := time.Duration(0)
		if plan.Action != RecoveryRefreshAuth {
			delay = backoffDelay(retriesUsed, cfg.InitialDelay, cfg.MaxDelay, err)
		}
		if journalErr := attempt.Complete(InferenceOutcomeFailed, failure); journalErr != nil {
			reportFailure(errors.Join(journalErr, err), "recovery_failed")
			return
		}
		nextAttempt, journalErr := attempt.PrepareRecoveryAttempt(ctx, plan, time.Now().Add(delay))
		if journalErr != nil {
			r.sendLifecycle(ctx, out, attemptReq.Execution, operation, StreamPhaseFailed, attempt.ID, attempt.Ordinal, maxAttempts, retriesUsed, maxRetries, journalErr, 0, false, startedAt)
			reportFailure(errors.Join(journalErr, err), "recovery_failed")
			return
		}
		if ctx.Err() != nil {
			_ = nextAttempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(ctx.Err()))
			return
		}
		if plan.Action == RecoveryRefreshAuth {
			applier := r.inner.(InferenceRecoveryApplier)
			if applyErr := applier.ApplyInferenceRecovery(ctx, plan); applyErr != nil {
				_ = nextAttempt.Complete(InferenceOutcomeFailed, NormalizeFailure(applyErr))
				reportFailure(errors.Join(applyErr, err), "recovery_failed")
				return
			}
		}
		retriesUsed = attempt.Ordinal
		nextAttemptOrdinal := attempt.Ordinal + 1
		if !r.sendLifecycle(ctx, out, attemptReq.Execution, operation, StreamPhaseReconnecting, operation.AttemptID(nextAttemptOrdinal), nextAttemptOrdinal, maxAttempts, retriesUsed, maxRetries, err, delay, forwardedEvents > 0, startedAt) {
			_ = nextAttempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(ctx.Err()))
			return
		}
		if r.onRetry != nil {
			r.onRetry(retriesUsed, maxRetries, err, delay)
		}
		if r.wait(ctx, delay) != nil {
			_ = nextAttempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(ctx.Err()))
			return
		}
		preparedNextAttempt = nextAttempt
	}
}

func (r *ReliableStreamClient) forwardAttempt(
	ctx context.Context,
	ch <-chan StreamEvent,
	out chan<- StreamEvent,
	execution *InferenceExecution,
	operation InferenceOperation,
	attempt InferenceAttempt,
	maxAttempts int,
	retryCount int,
	maxRetries int,
	startedAt time.Time,
) (error, bool, int, []ToolCall) {
	var streamErr error
	var sawDone bool
	var forwardedEvents int
	var finalizedToolCalls []ToolCall
	connected := false
	for {
		var ev StreamEvent
		var ok bool
		// Never block on a provider that stalls without sending, closing, or
		// honoring cancellation: the consumer's context must always be able to
		// end this loop.
		select {
		case <-ctx.Done():
			return streamErr, sawDone, forwardedEvents, finalizedToolCalls
		case ev, ok = <-ch:
		}
		if !ok {
			return streamErr, sawDone, forwardedEvents, finalizedToolCalls
		}
		if err := attempt.ObserveStreamEvent(ev); err != nil {
			return err, sawDone, forwardedEvents, finalizedToolCalls
		}
		if !connected {
			if !r.sendLifecycle(ctx, out, execution, operation, StreamPhaseConnected, attempt.ID, attempt.Ordinal, maxAttempts, retryCount, maxRetries, nil, 0, false, startedAt) {
				return streamErr, sawDone, forwardedEvents, finalizedToolCalls
			}
			connected = true
		}
		if ev.Type == EventError {
			if ev.Error != nil {
				streamErr = ev.Error
			} else {
				streamErr = errors.New("stream error")
			}
			continue
		}
		if ev.Type == EventDone {
			sawDone = true
		}
		if ev.Type == EventToolUseEnd && ev.ToolCall != nil {
			finalizedToolCalls = append(finalizedToolCalls, *ev.ToolCall)
		}
		if r.eventObserver != nil {
			if err := r.eventObserver(ctx, ev); err != nil {
				return err, sawDone, forwardedEvents, finalizedToolCalls
			}
		}
		if !r.send(ctx, out, ev) {
			return streamErr, sawDone, forwardedEvents, finalizedToolCalls
		}
		forwardedEvents++
		// EventDone is terminal for the logical response. Anything a provider
		// emits afterwards — a trailing transport error from an unclean close,
		// a stray frame — must not retroactively fail and replay a response
		// the consumer has already accepted.
		if ev.Type == EventDone {
			return nil, sawDone, forwardedEvents, finalizedToolCalls
		}
	}
}

func (r *ReliableStreamClient) sendLifecycle(
	ctx context.Context,
	out chan<- StreamEvent,
	execution *InferenceExecution,
	operation InferenceOperation,
	phase StreamLifecyclePhase,
	attemptID string,
	attempt int,
	maxAttempts int,
	retryCount int,
	maxRetries int,
	reason error,
	retryIn time.Duration,
	resetPartial bool,
	startedAt time.Time,
) bool {
	snapshot := execution.Snapshot()
	lastSubmissionID := ""
	if n := len(snapshot.Submissions); n > 0 {
		lastSubmissionID = snapshot.Submissions[n-1].ID
	}
	lifecycle := &StreamLifecycle{
		Phase:           phase,
		OperationID:     operation.ID,
		OperationKind:   operation.Kind,
		WorkloadProfile: operation.WorkloadProfile,
		PayloadVersion:  operation.PayloadVersion,
		AttemptID:       attemptID,
		Attempt:         attempt,
		MaxAttempts:     maxAttempts,
		SubmissionID:    lastSubmissionID,
		SubmissionCount: len(snapshot.Submissions),
		RetryCount:      retryCount,
		MaxRetries:      maxRetries,
		RetryIn:         retryIn,
		Elapsed:         time.Since(startedAt),
		ResetPartial:    resetPartial,
		Workflow:        snapshot.Workflow,
	}
	if reason != nil {
		lifecycle.Reason = StreamErrorSummary(reason)
		failure := NormalizeFailure(reason)
		lifecycle.FailureCategory = string(failure.Category)
		lifecycle.RecoveryAction = string(PlanRecovery(failure).Action)
		if phase == StreamPhaseFailed && failure.Category != FailureReplayUnsafe {
			lifecycle.RecoveryAction = string(RecoveryStop)
		}
		var budgetExceeded *WorkflowBudgetExceededError
		if errors.As(reason, &budgetExceeded) {
			lifecycle.BudgetDimension = string(budgetExceeded.Dimension)
		}
		var replayBlocked *ReplayBlockedError
		if errors.As(reason, &replayBlocked) && replayBlocked.Reason != nil {
			var coded interface{ ReplayReasonCode() string }
			if errors.As(replayBlocked.Reason, &coded) {
				lifecycle.ReplayReason = coded.ReplayReasonCode()
			}
		}
	}
	return r.send(ctx, out, StreamEvent{Type: EventLifecycle, Lifecycle: lifecycle})
}

func streamRecoverySupported(client StreamClient, plan RecoveryPlan) bool {
	switch plan.Action {
	case RecoveryReplaySame, RecoveryWaitThenReplay, RecoverySwitchTransport:
		return true
	case RecoveryRefreshAuth:
		_, ok := client.(InferenceRecoveryApplier)
		return ok
	default:
		return false
	}
}

func (r *ReliableStreamClient) send(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
