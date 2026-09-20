package providers

import "errors"

// StreamRecoveryInfo describes the failed logical stream, not the whole turn.
// Attempts count calls actually started by the recovery executor; prepared
// retries that never run do not count. Submissions count physical requests,
// including transport fallbacks, and do not imply known billable token usage.
type StreamRecoveryInfo struct {
	OperationID     string          `json:"operation_id,omitempty"`
	AttemptCount    int             `json:"attempt_count"`
	RetryCount      int             `json:"retry_count"`
	MaxAttempts     int             `json:"max_attempts"`
	SubmissionCount int             `json:"submission_count"`
	StopReason      string          `json:"stop_reason"`
	FailureCategory FailureCategory `json:"failure_category"`
	BudgetDimension string          `json:"budget_dimension,omitempty"`
}

// StreamRecoveryError preserves both the provider cause and the executor's
// actual stopping decision. Reclassifying the cause alone cannot distinguish
// retry exhaustion from a retryable failure that is still being recovered.
type StreamRecoveryError struct {
	Cause    error
	Recovery StreamRecoveryInfo
}

func (e *StreamRecoveryError) Error() string { return e.Cause.Error() }
func (e *StreamRecoveryError) Unwrap() error { return e.Cause }

func streamRecoveryError(err error, req ChatRequest, attempts int, stopReason string) error {
	failure := NormalizeFailure(err)
	snapshot := req.Execution.Snapshot()
	info := StreamRecoveryInfo{
		OperationID: req.Operation.ID, AttemptCount: attempts,
		RetryCount: max(0, attempts-1), MaxAttempts: req.Operation.AttemptLimit,
		SubmissionCount: len(snapshot.Submissions), StopReason: stopReason,
		FailureCategory: failure.Category,
	}
	switch failure.Category {
	case FailureReplayUnsafe:
		info.StopReason = "replay_unsafe"
	case FailureBudgetExceeded:
		info.StopReason = "workflow_budget_exceeded"
		var budgetErr *WorkflowBudgetExceededError
		if errors.As(err, &budgetErr) {
			info.BudgetDimension = string(budgetErr.Dimension)
		}
	case FailureCostIndeterminate:
		info.StopReason = "workflow_cost_indeterminate"
	}
	return &StreamRecoveryError{Cause: err, Recovery: info}
}
