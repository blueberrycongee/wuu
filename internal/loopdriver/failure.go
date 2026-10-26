package loopdriver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// EmptyAnswerError records a completed turn without text or tool calls. An empty
// StopReason means the provider did not report a normal completion signal.
type EmptyAnswerError struct {
	StopReason string
}

func (e *EmptyAnswerError) Error() string {
	if e.StopReason != "" {
		return fmt.Sprintf("model returned empty answer (stop_reason=%s)", e.StopReason)
	}
	return "model returned empty answer"
}

func IsEmptyAnswer(err error) bool {
	var target *EmptyAnswerError
	return errors.As(err, &target)
}

// FailureError carries operation and terminal failures through native and
// process services. Partial output belongs to the accompanying operation reply.
type FailureError struct {
	Failure pluginprotocol.DriverFailure
}

func (e *FailureError) Error() string { return e.Failure.Message }

func (e *FailureError) FailureEvidence() providers.NormalizedFailure {
	f := e.Failure
	return providers.NormalizedFailure{Origin: providers.FailureOrigin(f.Origin), Category: providers.FailureCategory(f.Category), ProviderCode: f.ProviderCode, HTTPStatus: f.HTTPStatus, RetryAfter: time.Duration(f.RetryAfterMS) * time.Millisecond, ContextOverflow: f.ContextOverflow, Cause: e}
}

func (e *FailureError) Unwrap() error {
	var cause error = errors.New(e.Failure.Message)
	switch e.Failure.Code {
	case "empty_answer":
		cause = &EmptyAnswerError{StopReason: e.Failure.StopReason}
	case "summary_output_limit":
		cause = compact.ErrSummaryOutputLimit
	default:
		switch providers.FailureCategory(e.Failure.Category) {
		case providers.FailureCanceled:
			cause = context.Canceled
		case providers.FailureDeadline:
			cause = context.DeadlineExceeded
		}
	}
	if recovery := e.Failure.Recovery; recovery != nil {
		return &providers.StreamRecoveryError{Cause: cause, Recovery: providers.StreamRecoveryInfo{OperationID: recovery.OperationID, AttemptCount: recovery.AttemptCount, RetryCount: recovery.RetryCount, MaxAttempts: recovery.MaxAttempts, SubmissionCount: recovery.SubmissionCount, StopReason: recovery.StopReason, BudgetDimension: recovery.BudgetDimension, FailureCategory: providers.FailureCategory(e.Failure.Category)}}
	}
	return cause
}

// FailureToWire preserves the classified cause and the recovery budget already
// spent. Receiving a retryable cause never grants a fresh retry budget.
func FailureToWire(err error) *pluginprotocol.DriverFailure {
	if err == nil {
		return nil
	}
	// A service can forward another service's receipt. Preserve its explicit
	// terminal code and recovery budget rather than classifying it a second time.
	var forwarded *FailureError
	if errors.As(err, &forwarded) {
		result := forwarded.Failure
		result.Message = err.Error()
		if result.Recovery != nil {
			recovery := *result.Recovery
			result.Recovery = &recovery
		}
		return &result
	}
	failure := providers.NormalizeFailure(err)
	result := &pluginprotocol.DriverFailure{
		Message: err.Error(), Origin: string(failure.Origin), Category: string(failure.Category),
		ProviderCode: failure.ProviderCode, HTTPStatus: failure.HTTPStatus,
		RetryAfterMS: failure.RetryAfter.Milliseconds(), ContextOverflow: failure.ContextOverflow,
	}
	var empty *EmptyAnswerError
	switch {
	case errors.As(err, &empty):
		result.Code, result.StopReason = "empty_answer", empty.StopReason
	case compact.IsSummaryOutputLimit(err):
		result.Code = "summary_output_limit"
	}
	var recovery *providers.StreamRecoveryError
	if errors.As(err, &recovery) {
		info := recovery.Recovery
		result.Recovery = &pluginprotocol.DriverModelRecovery{
			OperationID: info.OperationID, AttemptCount: info.AttemptCount, RetryCount: info.RetryCount,
			MaxAttempts: info.MaxAttempts, SubmissionCount: info.SubmissionCount,
			StopReason: info.StopReason, BudgetDimension: info.BudgetDimension,
		}
	}
	return result
}
