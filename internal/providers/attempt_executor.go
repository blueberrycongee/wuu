package providers

import (
	"context"
	"errors"
	"time"
)

// InferenceRequestPreparer performs provider-specific, wire-affecting request
// normalization before the request hash and journal operation are committed.
// It must not acquire provider capacity, refresh credentials, or send data.
type InferenceRequestPreparer interface {
	PrepareInferenceRequest(context.Context, ChatRequest) (ChatRequest, error)
}

func prepareInferenceRequest(ctx context.Context, client Client, req ChatRequest) (ChatRequest, error) {
	preparer, ok := client.(InferenceRequestPreparer)
	if !ok {
		return req, nil
	}
	return preparer.PrepareInferenceRequest(ctx, req)
}

// PrepareInferenceAttemptContext applies provider-specific wire shaping before
// durably binding the operation and its first attempt. Specialized inference
// executors, such as provider-native compaction, use this when they submit
// through a protocol path other than Client.Chat.
func PrepareInferenceAttemptContext(
	ctx context.Context,
	client Client,
	req ChatRequest,
	kind InferenceOperationKind,
	profile InferenceWorkloadProfile,
) (ChatRequest, error) {
	prepared, err := prepareInferenceRequest(ctx, client, req)
	if err != nil {
		return prepared, err
	}
	return EnsureInferenceAttemptContext(ctx, prepared, kind, profile)
}

// ExecuteChat owns one complete unary operation attempt. Protocol clients are
// responsible only for one physical send; this executor owns durable prepare,
// terminal attempt, and terminal operation transitions.
func ExecuteChat(
	ctx context.Context,
	client Client,
	req ChatRequest,
	kind InferenceOperationKind,
	profile InferenceWorkloadProfile,
) (ChatResponse, error) {
	for {
		resp, prepared, callErr := ExecuteChatAttempt(ctx, client, req, kind, profile)
		if prepared.Execution == nil {
			return resp, callErr
		}
		outcome, failure := inferenceTerminalFromError(callErr)
		if callErr == nil {
			if journalErr := prepared.Execution.Complete(outcome, failure); journalErr != nil {
				return resp, journalErr
			}
			return resp, nil
		}

		cfg := NormalizeRetryConfig(RetryConfigForProfile(prepared.Operation.WorkloadProfile))
		plan := PlanRecovery(failure)
		canRetry := prepared.Attempt.Ordinal < prepared.Operation.AttemptLimit && unaryRecoverySupported(client, plan)
		if ctx.Err() != nil || !canRetry {
			if journalErr := prepared.Execution.Complete(outcome, failure); journalErr != nil {
				return resp, errors.Join(callErr, journalErr)
			}
			return resp, callErr
		}
		delay := time.Duration(0)
		if plan.Action != RecoveryRefreshAuth {
			delay = backoffDelay(prepared.Attempt.Ordinal-1, cfg.InitialDelay, cfg.MaxDelay, callErr)
		}
		nextAttempt, journalErr := prepared.Attempt.PrepareRecoveryAttempt(ctx, plan, time.Now().Add(delay))
		if journalErr != nil {
			recoveryOutcome, recoveryFailure := inferenceTerminalFromError(journalErr)
			completeErr := prepared.Execution.Complete(recoveryOutcome, recoveryFailure)
			return resp, errors.Join(callErr, journalErr, completeErr)
		}
		req = prepared
		req.Attempt = nextAttempt
		if cancelErr := inferenceContextError(ctx); cancelErr != nil {
			attemptErr := nextAttempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(cancelErr))
			journalErr := prepared.Execution.Complete(InferenceOutcomeCanceled, NormalizeFailure(cancelErr))
			return resp, errors.Join(cancelErr, attemptErr, journalErr)
		}
		if plan.Action == RecoveryRefreshAuth {
			applier := client.(InferenceRecoveryApplier)
			if applyErr := applier.ApplyInferenceRecovery(ctx, plan); applyErr != nil {
				attemptErr := nextAttempt.Complete(InferenceOutcomeFailed, NormalizeFailure(applyErr))
				journalErr := prepared.Execution.Complete(InferenceOutcomeFailed, NormalizeFailure(applyErr))
				return resp, errors.Join(callErr, applyErr, attemptErr, journalErr)
			}
		}
		if delay > 0 {
			if waitErr := waitWithContext(ctx, delay); waitErr != nil {
				attemptErr := nextAttempt.Complete(InferenceOutcomeCanceled, NormalizeFailure(waitErr))
				journalErr := prepared.Execution.Complete(InferenceOutcomeCanceled, NormalizeFailure(waitErr))
				return resp, errors.Join(waitErr, attemptErr, journalErr)
			}
		}
	}
}

// InferenceRecoveryApplier performs a protocol-local mutation selected by the
// shared planner, such as refreshing an OAuth credential. The executor
// durably prepares the next attempt before calling it; the applier must not
// submit the inference request itself.
type InferenceRecoveryApplier interface {
	ApplyInferenceRecovery(context.Context, RecoveryPlan) error
}

func unaryRecoverySupported(client Client, plan RecoveryPlan) bool {
	switch plan.Action {
	case RecoveryReplaySame, RecoveryWaitThenReplay:
		return true
	case RecoveryRefreshAuth:
		_, ok := client.(InferenceRecoveryApplier)
		return ok
	default:
		return false
	}
}

// ExecuteChatAttempt runs and terminalizes exactly one unary attempt while
// leaving the operation open for an operation-specific completion validator.
func ExecuteChatAttempt(
	ctx context.Context,
	client Client,
	req ChatRequest,
	kind InferenceOperationKind,
	profile InferenceWorkloadProfile,
) (ChatResponse, ChatRequest, error) {
	if client == nil {
		return ChatResponse{}, ChatRequest{}, errors.New("chat client is required")
	}
	prepared, err := PrepareInferenceAttemptContext(ctx, client, req, kind, profile)
	if err != nil {
		return ChatResponse{}, prepared, completePreparedAttemptError(prepared, err)
	}
	resp, callErr := client.Chat(ctx, prepared)
	outcome, failure := inferenceTerminalFromError(callErr)
	if journalErr := prepared.Attempt.Complete(outcome, failure); journalErr != nil {
		if callErr != nil {
			return resp, prepared, errors.Join(callErr, journalErr)
		}
		return resp, prepared, journalErr
	}
	return resp, prepared, callErr
}

func completePreparedAttemptError(req ChatRequest, cause error) error {
	if !req.Attempt.Valid() {
		return cause
	}
	outcome, failure := inferenceTerminalFromError(cause)
	if err := req.Attempt.Complete(outcome, failure); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func inferenceTerminalFromError(err error) (InferenceTerminalOutcome, NormalizedFailure) {
	if err == nil {
		return InferenceOutcomeSucceeded, NormalizedFailure{}
	}
	failure := NormalizeFailure(err)
	outcome := InferenceOutcomeFailed
	if failure.Category == FailureCanceled || failure.Category == FailureDeadline {
		outcome = InferenceOutcomeCanceled
	}
	return outcome, failure
}
