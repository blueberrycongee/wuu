package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (j *documentInferenceJournal) operation(tx *persistence.Tx, id string, requireActive bool) (inferenceOperationDocument, error) {
	op, err := requireInferenceDocument[inferenceOperationDocument](tx, "inference_operations", id)
	if err != nil {
		return op, err
	}
	if op.RuntimeID != j.runtime.runtimeID || op.WorkspaceScope != j.runtime.workspaceScope || op.OwnerID != j.ownerID {
		return op, fmt.Errorf("operation %q is owned by another inference journal", id)
	}
	if requireActive && op.Status != "active" {
		return op, fmt.Errorf("operation %q is already terminal (%s)", id, op.Status)
	}
	return op, nil
}

func (j *documentInferenceJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	op := record.Operation
	op.ID, op.WorkflowID, op.ParentOperationID = strings.TrimSpace(op.ID), strings.TrimSpace(op.WorkflowID), strings.TrimSpace(op.ParentOperationID)
	record.RequestHash = strings.TrimSpace(record.RequestHash)
	if op.ID == "" || !validInferenceRequestHash(record.RequestHash) {
		return errors.New("prepare inference operation: operation id and request hash are required")
	}
	if op.PayloadVersion < 1 {
		return errors.New("prepare inference operation: payload version must be positive")
	}
	if op.AttemptLimit < 1 {
		return errors.New("prepare inference operation: attempt limit must be positive")
	}
	workflow := normalizeInferenceWorkflowRecord(record.Workflow, op, record.At)
	workflow.ID = strings.TrimSpace(workflow.ID)
	if op.WorkflowID == "" {
		op.WorkflowID = workflow.ID
	}
	if workflow.ID != op.WorkflowID {
		return fmt.Errorf("prepare inference operation: operation workflow %q does not match record workflow %q", op.WorkflowID, workflow.ID)
	}
	stamp := journalTime(record.At)
	if workflow.StartedAt.IsZero() {
		workflow.StartedAt = time.UnixMilli(stamp).UTC()
	}
	return j.write("prepare inference operation", func(tx *persistence.Tx) error {
		if err := j.prepareWorkflow(tx, workflow, stamp); err != nil {
			return err
		}
		var existing inferenceOperationDocument
		found, err := tx.Get(documentNamespace("inference_operations"), op.ID, &existing)
		if err != nil {
			return err
		}
		if found {
			if existing.InferenceOperation != op || existing.RuntimeID != j.runtime.runtimeID || existing.WorkspaceScope != j.runtime.workspaceScope || existing.OwnerID != j.ownerID || existing.RequestHash != record.RequestHash {
				return fmt.Errorf("operation %q metadata changed after preparation", op.ID)
			}
			if existing.Status != "active" {
				return fmt.Errorf("operation %q is already terminal (%s)", op.ID, existing.Status)
			}
			return nil
		}
		if op.ParentOperationID != "" {
			if op.ParentOperationID == op.ID {
				return errors.New("inference operation cannot be its own parent")
			}
			parent, err := j.operation(tx, op.ParentOperationID, false)
			if err != nil {
				return err
			}
			if parent.WorkflowID != op.WorkflowID {
				return fmt.Errorf("parent inference operation %q belongs to another workflow", parent.ID)
			}
		}
		if err := assertWorkflowDocumentCost(tx, workflow.ID); err != nil {
			return err
		}
		if err := reserveWorkflowDocument(tx, workflow.ID, providers.WorkflowBudgetOperations, 1, stamp); err != nil {
			return err
		}
		if op.ParentOperationID != "" {
			if err := reserveWorkflowDocument(tx, workflow.ID, providers.WorkflowBudgetChildOperations, 1, stamp); err != nil {
				return err
			}
		}
		storedWorkflow, err := requireInferenceDocument[inferenceWorkflowDocument](tx, "inference_workflows", workflow.ID)
		if err != nil {
			return err
		}
		storedWorkflow.OperationIDs = append(storedWorkflow.OperationIDs, op.ID)
		if err := putInferenceWorkflow(tx, storedWorkflow); err != nil {
			return err
		}
		return putInferenceOperation(tx, inferenceOperationDocument{
			InferenceOperation: op, RuntimeID: j.runtime.runtimeID, WorkspaceScope: j.runtime.workspaceScope, OwnerID: j.ownerID,
			RequestHash: record.RequestHash, Status: "active", CreatedAt: stamp, UpdatedAt: stamp, AttemptIDs: []string{},
		})
	})
}

func (j *documentInferenceJournal) prepareWorkflow(tx *persistence.Tx, record providers.InferenceWorkflowJournalRecord, stamp int64) error {
	if record.ID == "" || record.Profile == "" {
		return errors.New("prepare inference workflow: id and profile are required")
	}
	if _, err := inferenceWorkflowLimits(record.Budget); err != nil {
		return err
	}
	var existing inferenceWorkflowDocument
	found, err := tx.Get(documentNamespace("inference_workflows"), record.ID, &existing)
	if err != nil {
		return err
	}
	if found {
		if existing.RuntimeID != j.runtime.runtimeID || existing.WorkspaceScope != j.runtime.workspaceScope || existing.OwnerID != j.ownerID || existing.Profile != record.Profile || existing.Budget != record.Budget {
			return fmt.Errorf("workflow %q metadata changed after preparation", record.ID)
		}
		if existing.Status != "active" {
			return fmt.Errorf("workflow %q is already terminal (%s)", record.ID, existing.Status)
		}
		return nil
	}
	return putInferenceWorkflow(tx, inferenceWorkflowDocument{
		InferenceWorkflowJournalRecord: record, RuntimeID: j.runtime.runtimeID, WorkspaceScope: j.runtime.workspaceScope, OwnerID: j.ownerID,
		Status: "active", CreatedAt: record.StartedAt.UnixMilli(), UpdatedAt: stamp, Used: make(map[providers.WorkflowBudgetDimension]int64), OperationIDs: []string{},
	})
}

func (j *documentInferenceJournal) PrepareAttempt(record providers.InferenceAttemptJournalRecord) error {
	if err := normalizeInferenceAttemptJournalRecord(&record); err != nil {
		return err
	}
	stamp := journalTime(record.At)
	return j.write("prepare inference attempt", func(tx *persistence.Tx) error { return j.prepareAttempt(tx, record, stamp) })
}

func (j *documentInferenceJournal) prepareAttempt(tx *persistence.Tx, record providers.InferenceAttemptJournalRecord, stamp int64) error {
	op, err := j.operation(tx, record.OperationID, false)
	if err != nil {
		return err
	}
	if record.WorkflowID != "" && record.WorkflowID != op.WorkflowID {
		return fmt.Errorf("attempt %q belongs to another workflow", record.AttemptID)
	}
	var existing inferenceAttemptDocument
	found, err := tx.Get(documentNamespace("inference_attempts"), record.AttemptID, &existing)
	if err != nil {
		return err
	}
	if found {
		if existing.OperationID != record.OperationID || existing.Ordinal != record.Ordinal || existing.RequestHash != record.RequestHash {
			return fmt.Errorf("attempt %q metadata changed after preparation", record.AttemptID)
		}
		return nil
	}
	if op.Status != "active" || op.RequestHash != record.RequestHash {
		return fmt.Errorf("operation %q is not the active prepared operation", record.OperationID)
	}
	if record.Ordinal > op.AttemptLimit {
		return &providers.WorkflowBudgetExceededError{WorkflowID: op.WorkflowID, Dimension: providers.WorkflowBudgetAttempts, Limit: uint64(op.AttemptLimit), Used: uint64(op.AttemptLimit), Requested: 1}
	}
	answered := true
	for _, id := range op.AttemptIDs {
		prior, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", id)
		if err != nil {
			return err
		}
		if prior.Ordinal == record.Ordinal {
			return fmt.Errorf("operation %q attempt ordinal %d is already prepared", op.ID, record.Ordinal)
		}
		if prior.Ordinal == record.Ordinal-1 {
			answered = prior.Failure.Origin != providers.FailureOriginNetwork || prior.FirstEventAt != 0
		}
	}
	if err := assertWorkflowDocumentCost(tx, op.WorkflowID); err != nil {
		return err
	}
	if err := reserveWorkflowDocument(tx, op.WorkflowID, providers.WorkflowBudgetAttempts, 1, stamp); err != nil {
		return err
	}
	if record.Ordinal > 1 && answered {
		if err := reserveWorkflowDocument(tx, op.WorkflowID, providers.WorkflowBudgetSamePayloadReplays, 1, stamp); err != nil {
			return err
		}
	}
	if err := tx.Put(documentNamespace("inference_attempts"), record.AttemptID, inferenceAttemptDocument{
		ID: record.AttemptID, OperationID: op.ID, Ordinal: record.Ordinal, RequestHash: record.RequestHash, Phase: "prepared", PreparedAt: stamp, SubmissionIDs: []string{},
	}); err != nil {
		return err
	}
	op.AttemptIDs, op.UpdatedAt = append(op.AttemptIDs, record.AttemptID), stamp
	return putInferenceOperation(tx, op)
}

func normalizeSubmissionDocument(record *providers.InferenceSubmissionJournalRecord) {
	record.ID, record.OperationID, record.AttemptID = strings.TrimSpace(record.ID), strings.TrimSpace(record.OperationID), strings.TrimSpace(record.AttemptID)
	record.Provider, record.Protocol = journalText(record.Provider, 128), journalText(record.Protocol, 128)
	record.Transport, record.Mode, record.Reason = journalText(record.Transport, 128), journalText(record.Mode, 128), journalText(record.Reason, 256)
	if record.StartedAt.IsZero() {
		record.StartedAt = time.Now().UTC()
	}
	if record.ReportedUsage != nil {
		usage := *record.ReportedUsage
		record.ReportedUsage = &usage
	}
	if record.EstimatedUsage != nil {
		usage := *record.EstimatedUsage
		record.EstimatedUsage = &usage
	}
}

func (j *documentInferenceJournal) UpsertSubmission(record providers.InferenceSubmissionJournalRecord) error {
	normalizeSubmissionDocument(&record)
	return j.write("upsert inference submission", func(tx *persistence.Tx) error { return j.upsertSubmission(tx, record) })
}

func (j *documentInferenceJournal) upsertSubmission(tx *persistence.Tx, record providers.InferenceSubmissionJournalRecord) error {
	if record.ID == "" || record.OperationID == "" || record.AttemptID == "" || record.Ordinal < 1 || record.AttemptOrdinal < 1 {
		return errors.New("upsert inference submission: ids and ordinals are required")
	}
	op, err := j.operation(tx, record.OperationID, false)
	if err != nil {
		return err
	}
	attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", record.AttemptID)
	if err != nil {
		return err
	}
	if attempt.OperationID != op.ID || attempt.Ordinal != record.AttemptOrdinal {
		return fmt.Errorf("attempt %q does not belong to operation %q", record.AttemptID, op.ID)
	}
	var existing providers.InferenceSubmissionJournalRecord
	found, err := tx.Get(documentNamespace("inference_submissions"), record.ID, &existing)
	if err != nil {
		return err
	}
	stamp := record.StartedAt.UnixMilli()
	next := record
	if !found {
		for _, attemptID := range op.AttemptIDs {
			priorAttempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", attemptID)
			if err != nil {
				return err
			}
			for _, id := range priorAttempt.SubmissionIDs {
				prior, err := requireInferenceDocument[providers.InferenceSubmissionJournalRecord](tx, "inference_submissions", id)
				if err != nil {
					return err
				}
				if prior.Ordinal == record.Ordinal {
					return fmt.Errorf("operation %q submission ordinal %d already exists", op.ID, record.Ordinal)
				}
			}
		}
		if err := assertWorkflowDocumentCost(tx, op.WorkflowID); err != nil {
			return err
		}
		if err := reserveWorkflowDocument(tx, op.WorkflowID, providers.WorkflowBudgetSubmissions, 1, stamp); err != nil {
			return err
		}
		attempt.SubmissionIDs = append(attempt.SubmissionIDs, record.ID)
	} else {
		next, err = mergeInferenceSubmissionEvidence(existing, record)
		if err != nil {
			return err
		}
	}
	if err := applyWorkflowDocumentCost(tx, op.WorkflowID, existing, next, stamp); err != nil {
		return err
	}
	if err := tx.Put(documentNamespace("inference_submissions"), next.ID, next); err != nil {
		return err
	}
	if attempt.Phase != "terminal" {
		if attempt.Phase == "prepared" || attempt.Phase == "dispatching" {
			attempt.Phase = "sent"
		}
		if attempt.SentAt == 0 {
			attempt.SentAt = stamp
		}
	}
	if err := tx.Put(documentNamespace("inference_attempts"), attempt.ID, attempt); err != nil {
		return err
	}
	if op.Status == "active" {
		op.UpdatedAt = stamp
		return putInferenceOperation(tx, op)
	}
	return nil
}

// Progress snapshots can arrive out of order before or after a durable write.
// Both boundaries preserve terminal evidence while accepting stronger usage.
func mergeInferenceSubmissionEvidence(existing, record providers.InferenceSubmissionJournalRecord) (providers.InferenceSubmissionJournalRecord, error) {
	if existing.OperationID != record.OperationID || existing.AttemptID != record.AttemptID || existing.Ordinal != record.Ordinal || existing.AttemptOrdinal != record.AttemptOrdinal {
		return existing, fmt.Errorf("submission %q metadata changed after preparation", record.ID)
	}
	next := existing
	if existing.Outcome != providers.InferenceSubmissionInFlight {
		if record.Outcome != providers.InferenceSubmissionInFlight && record.Outcome != existing.Outcome {
			return existing, fmt.Errorf("submission %q already completed as %s", record.ID, existing.Outcome)
		}
	} else {
		next.Outcome = record.Outcome
	}
	if next.FailureCategory == "" {
		next.FailureCategory = record.FailureCategory
	}
	switch {
	case inferenceCostRank(string(existing.CostState)) > inferenceCostRank(string(record.CostState)):
	case inferenceCostRank(string(existing.CostState)) == inferenceCostRank(string(record.CostState)):
		next.CostState = record.CostState
		next.ReportedUsage = mergeInferenceDocumentUsage(existing.ReportedUsage, record.ReportedUsage)
		next.EstimatedUsage = mergeInferenceDocumentUsage(existing.EstimatedUsage, record.EstimatedUsage)
	default:
		next.CostState, next.ReportedUsage = record.CostState, record.ReportedUsage
		if record.EstimatedUsage != nil {
			next.EstimatedUsage = record.EstimatedUsage
		}
	}
	next.OutputBytes = max(existing.OutputBytes, record.OutputBytes)
	if next.CompletedAt.IsZero() {
		next.CompletedAt = record.CompletedAt
	}
	return next, nil
}

func (j *documentInferenceJournal) MarkAttemptFirstEvent(operationID, attemptID, submissionID string, at time.Time) error {
	operationID, attemptID, submissionID = strings.TrimSpace(operationID), strings.TrimSpace(attemptID), strings.TrimSpace(submissionID)
	if operationID == "" || attemptID == "" || submissionID == "" {
		return errors.New("mark inference first event: ids are required")
	}
	stamp := journalTime(at)
	return j.write("mark inference first event", func(tx *persistence.Tx) error {
		op, err := j.operation(tx, operationID, true)
		if err != nil {
			return err
		}
		submission, err := requireInferenceDocument[providers.InferenceSubmissionJournalRecord](tx, "inference_submissions", submissionID)
		if err != nil {
			return err
		}
		if submission.OperationID != operationID || submission.AttemptID != attemptID {
			return fmt.Errorf("submission %q does not belong to attempt %q", submissionID, attemptID)
		}
		attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", attemptID)
		if err != nil {
			return err
		}
		if attempt.OperationID != operationID || attempt.Phase == "terminal" {
			return fmt.Errorf("attempt %q is not active", attemptID)
		}
		if attempt.Phase == "prepared" || attempt.Phase == "dispatching" || attempt.Phase == "sent" {
			attempt.Phase = "streaming"
		}
		if attempt.FirstEventAt == 0 {
			attempt.FirstEventAt = stamp
		}
		if err := tx.Put(documentNamespace("inference_attempts"), attemptID, attempt); err != nil {
			return err
		}
		op.UpdatedAt = stamp
		return putInferenceOperation(tx, op)
	})
}

func (j *documentInferenceJournal) PrepareRecoveryAttempt(ctx context.Context, record providers.InferenceRecoveryAttemptJournalRecord) error {
	recovery := record.Recovery
	recovery.OperationID, recovery.AttemptID = strings.TrimSpace(recovery.OperationID), strings.TrimSpace(recovery.AttemptID)
	if recovery.OperationID == "" || recovery.AttemptID == "" || recovery.Action == "" {
		return errors.New("record inference recovery: ids and action are required")
	}
	if err := normalizeInferenceAttemptJournalRecord(&record.NextAttempt); err != nil {
		return err
	}
	if recovery.OperationID != record.NextAttempt.OperationID {
		return errors.New("recovery and next attempt operations do not match")
	}
	stamp, retryAt, nextAt := journalTime(recovery.At), optionalJournalTime(recovery.RetryAt), journalTime(record.NextAttempt.At)
	return j.writeContext(ctx, "prepare inference recovery attempt", func(tx *persistence.Tx) error {
		op, err := j.operation(tx, recovery.OperationID, true)
		if err != nil {
			return err
		}
		attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", recovery.AttemptID)
		if err != nil {
			return err
		}
		if attempt.OperationID != op.ID || attempt.Phase != "terminal" {
			return fmt.Errorf("attempt %q is not terminal for recovery", recovery.AttemptID)
		}
		if attempt.RecoveryAction != "" && attempt.RecoveryAction != recovery.Action {
			return fmt.Errorf("attempt %q already recorded recovery %q", attempt.ID, attempt.RecoveryAction)
		}
		if attempt.RecoveryAction == "" {
			if err := assertWorkflowDocumentCost(tx, op.WorkflowID); err != nil {
				return err
			}
			dimension := recoveryWorkflowDimension(recovery.Action)
			if dimension != "" {
				if err := reserveWorkflowDocument(tx, op.WorkflowID, dimension, 1, stamp); err != nil {
					return err
				}
			}
			if retryAt > stamp {
				if err := reserveWorkflowDocument(tx, op.WorkflowID, providers.WorkflowBudgetRecoveryWait, retryAt-stamp, stamp); err != nil {
					return err
				}
			}
			attempt.RecoveryAction, attempt.RetryAt = recovery.Action, retryAt
			if err := tx.Put(documentNamespace("inference_attempts"), attempt.ID, attempt); err != nil {
				return err
			}
			op.RecoveryAction, op.UpdatedAt = recovery.Action, stamp
			if err := putInferenceOperation(tx, op); err != nil {
				return err
			}
		}
		return j.prepareAttempt(tx, record.NextAttempt, nextAt)
	})
}

func normalizedDocumentFailure(failure providers.InferenceJournalFailure) providers.InferenceJournalFailure {
	failure.ProviderFamily, failure.ProviderCode, failure.Message = journalText(failure.ProviderFamily, 128), journalText(failure.ProviderCode, 128), journalText(failure.Message, 256)
	return failure
}

func (j *documentInferenceJournal) CompleteAttempt(record providers.InferenceAttemptTerminalRecord) error {
	record.OperationID, record.AttemptID = strings.TrimSpace(record.OperationID), strings.TrimSpace(record.AttemptID)
	if record.OperationID == "" || record.AttemptID == "" || record.Outcome == "" {
		return errors.New("complete inference attempt: ids and outcome are required")
	}
	j.runtime.flushSubmissionProgress(false)
	stamp := journalTime(record.At)
	return j.write("complete inference attempt", func(tx *persistence.Tx) error {
		if _, err := j.operation(tx, record.OperationID, false); err != nil {
			return err
		}
		return completeInferenceAttemptDocument(tx, record.OperationID, record.AttemptID, record.Outcome, record.Failure, stamp)
	})
}

func completeInferenceAttemptDocument(tx *persistence.Tx, operationID, id string, outcome providers.InferenceTerminalOutcome, failure providers.InferenceJournalFailure, stamp int64) error {
	attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", id)
	if err != nil {
		return err
	}
	if attempt.OperationID != operationID {
		return fmt.Errorf("attempt %q belongs to another operation", id)
	}
	if attempt.Phase == "terminal" {
		if attempt.TerminalOutcome == outcome {
			return nil
		}
		return fmt.Errorf("attempt %q already completed as %s", id, attempt.TerminalOutcome)
	}
	attempt.Phase, attempt.TerminalOutcome, attempt.TerminalAt, attempt.Failure = "terminal", outcome, stamp, normalizedDocumentFailure(failure)
	return tx.Put(documentNamespace("inference_attempts"), id, attempt)
}

func (j *documentInferenceJournal) CompleteOperation(record providers.InferenceOperationTerminalRecord) error {
	record.OperationID = strings.TrimSpace(record.OperationID)
	if record.OperationID == "" || record.Outcome == "" {
		return errors.New("complete inference operation: id and outcome are required")
	}
	j.runtime.flushSubmissionProgress(false)
	stamp := journalTime(record.At)
	return j.write("complete inference operation", func(tx *persistence.Tx) error {
		if _, err := j.operation(tx, record.OperationID, false); err != nil {
			return err
		}
		return completeInferenceOperationDocument(tx, record.OperationID, record.Outcome, "", record.Failure, stamp)
	})
}

func completeInferenceOperationDocument(tx *persistence.Tx, id string, outcome providers.InferenceTerminalOutcome, action providers.RecoveryActionKind, failure providers.InferenceJournalFailure, stamp int64) error {
	op, err := requireInferenceDocument[inferenceOperationDocument](tx, "inference_operations", id)
	if err != nil {
		return err
	}
	if op.Status != "active" {
		if op.TerminalOutcome == outcome {
			return nil
		}
		return fmt.Errorf("operation %q already completed as %s", id, op.TerminalOutcome)
	}
	op.Status, op.TerminalOutcome, op.RecoveryAction, op.Failure = string(outcome), outcome, action, normalizedDocumentFailure(failure)
	op.UpdatedAt, op.TerminalAt = stamp, stamp
	return putInferenceOperation(tx, op)
}

func (j *documentInferenceJournal) CompleteWorkflow(record providers.InferenceWorkflowTerminalRecord) error {
	record.WorkflowID = strings.TrimSpace(record.WorkflowID)
	if record.WorkflowID == "" || record.Outcome == "" {
		return errors.New("complete inference workflow: id and outcome are required")
	}
	stamp := journalTime(record.At)
	return j.write("complete inference workflow", func(tx *persistence.Tx) error {
		workflow, err := requireInferenceDocument[inferenceWorkflowDocument](tx, "inference_workflows", record.WorkflowID)
		if err != nil {
			return err
		}
		if workflow.RuntimeID != j.runtime.runtimeID || workflow.WorkspaceScope != j.runtime.workspaceScope || workflow.OwnerID != j.ownerID {
			return fmt.Errorf("workflow %q is owned by another inference journal", record.WorkflowID)
		}
		return completeInferenceWorkflowDocument(tx, workflow, record.Outcome, stamp)
	})
}

func completeInferenceWorkflowDocument(tx *persistence.Tx, workflow inferenceWorkflowDocument, outcome providers.InferenceTerminalOutcome, stamp int64) error {
	if workflow.Status != "active" {
		if workflow.Status == string(outcome) {
			return nil
		}
		return fmt.Errorf("workflow %q already completed as %s", workflow.ID, workflow.Status)
	}
	for _, id := range workflow.OperationIDs {
		op, err := requireInferenceDocument[inferenceOperationDocument](tx, "inference_operations", id)
		if err != nil {
			return err
		}
		if op.Status == "active" {
			return fmt.Errorf("workflow %q still has active operations", workflow.ID)
		}
	}
	workflow.Status, workflow.UpdatedAt, workflow.TerminalAt = string(outcome), stamp, stamp
	return putInferenceWorkflow(tx, workflow)
}
