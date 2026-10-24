package session

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"time"
)

// Explicit call contexts control cancellation; the journal's operation binding
// owns identity even when a required checkpoint passes Background to finish.
type journalOperationContext struct {
	context.Context
	operation context.Context
}

func (c journalOperationContext) Value(key any) any {
	if value := c.operation.Value(key); value != nil {
		return value
	}
	return c.Context.Value(key)
}
func journalCallContext(ctx, operation context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if operation == nil {
		return ctx
	}
	return providers.WithModelReceiptAdmission(journalOperationContext{Context: ctx, operation: operation}, ctx)
}

// WithContext returns an independent operation binding for a shared resource.
func (j *remoteInferenceJournal) WithContext(ctx context.Context) providers.InferenceJournal {
	clone := *j
	clone.ctx = context.WithoutCancel(ctx)
	return &clone
}

func (j *remoteInferenceJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	return j.invoke(j.ctx, "PrepareOperation", record)
}
func (j *remoteInferenceJournal) PrepareAttempt(record providers.InferenceAttemptJournalRecord) error {
	return j.invoke(j.ctx, "PrepareAttempt", record)
}
func (j *remoteInferenceJournal) UpsertSubmission(ctx context.Context, record providers.InferenceSubmissionJournalRecord) error {
	return j.invoke(ctx, "UpsertSubmission", record)
}
func (j *remoteInferenceJournal) MarkAttemptFirstEvent(operationID, attemptID, submissionID string, at time.Time) error {
	return j.invoke(j.ctx, "MarkAttemptFirstEvent", operationID, attemptID, submissionID, at)
}
func (j *remoteInferenceJournal) PrepareRecoveryAttempt(ctx context.Context, record providers.InferenceRecoveryAttemptJournalRecord) error {
	return j.invoke(ctx, "PrepareRecoveryAttempt", record)
}
func (j *remoteInferenceJournal) CompleteAttempt(record providers.InferenceAttemptTerminalRecord) error {
	return j.invoke(j.ctx, "CompleteAttempt", record)
}
func (j *remoteInferenceJournal) CompleteOperation(record providers.InferenceOperationTerminalRecord) error {
	return j.invoke(j.ctx, "CompleteOperation", record)
}
func (j *remoteInferenceJournal) CompleteWorkflow(record providers.InferenceWorkflowTerminalRecord) error {
	return j.invoke(j.ctx, "CompleteWorkflow", record)
}
