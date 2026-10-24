package session

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"time"
)

func (j *remoteInferenceJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	return j.invoke(context.Background(), "PrepareOperation", record)
}
func (j *remoteInferenceJournal) PrepareAttempt(record providers.InferenceAttemptJournalRecord) error {
	return j.invoke(context.Background(), "PrepareAttempt", record)
}
func (j *remoteInferenceJournal) UpsertSubmission(ctx context.Context, record providers.InferenceSubmissionJournalRecord) error {
	return j.invoke(ctx, "UpsertSubmission", record)
}
func (j *remoteInferenceJournal) MarkAttemptFirstEvent(operationID, attemptID, submissionID string, at time.Time) error {
	return j.invoke(context.Background(), "MarkAttemptFirstEvent", operationID, attemptID, submissionID, at)
}
func (j *remoteInferenceJournal) PrepareRecoveryAttempt(ctx context.Context, record providers.InferenceRecoveryAttemptJournalRecord) error {
	return j.invoke(ctx, "PrepareRecoveryAttempt", record)
}
func (j *remoteInferenceJournal) CompleteAttempt(record providers.InferenceAttemptTerminalRecord) error {
	return j.invoke(context.Background(), "CompleteAttempt", record)
}
func (j *remoteInferenceJournal) CompleteOperation(record providers.InferenceOperationTerminalRecord) error {
	return j.invoke(context.Background(), "CompleteOperation", record)
}
func (j *remoteInferenceJournal) CompleteWorkflow(record providers.InferenceWorkflowTerminalRecord) error {
	return j.invoke(context.Background(), "CompleteWorkflow", record)
}
