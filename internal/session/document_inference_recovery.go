package session

import (
	"slices"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ReconcileOrphans records safe rescheduling or ambiguous interruption; it never
// reconstructs or sends a model request. A live process retains its ownership
// even if its heartbeat was delayed by a blocked disk or a paused debugger.
func (r *InferenceJournalRuntime) ReconcileOrphans(now time.Time) ([]InferenceCrashRecovery, error) {
	if r == nil {
		return nil, nil
	}
	done, err := r.beginUse()
	if err != nil {
		return nil, err
	}
	defer done()
	stamp, cutoff := journalTime(now), journalTime(now.Add(-inferenceJournalStaleAfter))
	var recoveries []InferenceCrashRecovery
	err = r.store.transaction(func(tx *persistence.Tx) error {
		recoveries = nil
		operations, err := readDocumentRows[inferenceOperationDocument](tx, "inference_operations", "")
		if err != nil {
			return err
		}
		slices.SortFunc(operations, func(a, b inferenceOperationDocument) int {
			if a.CreatedAt < b.CreatedAt {
				return -1
			}
			if a.CreatedAt > b.CreatedAt {
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
		for _, op := range operations {
			if op.WorkspaceScope != r.workspaceScope || op.RuntimeID == r.runtimeID || op.Status != "active" {
				continue
			}
			runtime, err := ReadInferenceRuntime(tx, op.RuntimeID)
			if err != nil {
				return err
			}
			if runtime.ClosedAt == 0 && inferenceRuntimeProcessAlive(runtime.PID) {
				continue
			}
			var latest inferenceAttemptDocument
			for _, id := range op.AttemptIDs {
				attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", id)
				if err != nil {
					return err
				}
				if attempt.Ordinal > latest.Ordinal {
					latest = attempt
				}
			}
			item := InferenceCrashRecovery{OperationID: op.ID, OwnerID: op.OwnerID, Kind: op.Kind, Profile: op.WorkloadProfile,
				AttemptID: latest.ID, PriorPhase: latest.Phase, PriorOutcome: string(latest.TerminalOutcome), PriorRecovery: latest.RecoveryAction}
			switch {
			case item.Profile == providers.InferenceProfileBestEffort:
				item.Outcome, item.Action = providers.InferenceOutcomeAbandoned, providers.RecoveryStop
			case item.PriorPhase == "" || item.PriorPhase == "prepared":
				item.Outcome, item.Action = providers.InferenceOutcomeInterrupted, providers.RecoveryRescheduleSafe
			case item.PriorPhase == "terminal" && recoveryCanCreateAnotherAttempt(item.PriorRecovery):
				item.Outcome, item.Action = providers.InferenceOutcomeInterrupted, providers.RecoveryRescheduleSafe
			case item.PriorPhase == "terminal":
				item.Outcome, item.Action = providers.InferenceOutcomeInterrupted, providers.RecoveryStop
			default:
				item.Outcome, item.Action = providers.InferenceOutcomeBlocked, providers.RecoveryBlockAmbiguous
			}
			for _, id := range op.AttemptIDs {
				attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", id)
				if err != nil {
					return err
				}
				for _, submissionID := range attempt.SubmissionIDs {
					submission, err := requireInferenceDocument[providers.InferenceSubmissionJournalRecord](tx, "inference_submissions", submissionID)
					if err != nil {
						return err
					}
					if submission.Outcome != providers.InferenceSubmissionInFlight {
						continue
					}
					submission.Outcome = providers.InferenceSubmissionInterrupted
					if item.Outcome == providers.InferenceOutcomeAbandoned {
						submission.Outcome = providers.InferenceSubmissionAbandoned
					}
					if submission.CompletedAt.IsZero() {
						submission.CompletedAt = time.UnixMilli(stamp).UTC()
					}
					if err := tx.Put(documentNamespace("inference_submissions"), submission.ID, submission); err != nil {
						return err
					}
				}
			}
			if latest.ID != "" && latest.Phase != "terminal" {
				if err := completeInferenceAttemptDocument(tx, op.ID, latest.ID, item.Outcome, providers.InferenceJournalFailure{}, stamp); err != nil {
					return err
				}
				attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", latest.ID)
				if err != nil {
					return err
				}
				attempt.RecoveryAction = item.Action
				if err := tx.Put(documentNamespace("inference_attempts"), attempt.ID, attempt); err != nil {
					return err
				}
			}
			if err := completeInferenceOperationDocument(tx, op.ID, item.Outcome, item.Action, providers.InferenceJournalFailure{}, stamp); err != nil {
				return err
			}
			recoveries = append(recoveries, item)
		}
		workflows, err := readDocumentRows[inferenceWorkflowDocument](tx, "inference_workflows", "")
		if err != nil {
			return err
		}
		for _, workflow := range workflows {
			if workflow.WorkspaceScope != r.workspaceScope || workflow.RuntimeID == r.runtimeID || workflow.Status != "active" {
				continue
			}
			runtime, err := ReadInferenceRuntime(tx, workflow.RuntimeID)
			if err != nil {
				return err
			}
			if runtime.ClosedAt == 0 && (runtime.HeartbeatAt >= cutoff || inferenceRuntimeProcessAlive(runtime.PID)) {
				continue
			}
			active, blocked := false, false
			for _, id := range workflow.OperationIDs {
				op, err := requireInferenceDocument[inferenceOperationDocument](tx, "inference_operations", id)
				if err != nil {
					return err
				}
				active = active || op.Status == "active"
				blocked = blocked || op.Status == string(providers.InferenceOutcomeBlocked)
			}
			if active {
				continue
			}
			outcome := providers.InferenceOutcomeInterrupted
			if blocked {
				outcome = providers.InferenceOutcomeBlocked
			} else if workflow.Profile == providers.InferenceProfileBestEffort {
				outcome = providers.InferenceOutcomeAbandoned
			}
			if err := completeInferenceWorkflowDocument(tx, workflow, outcome, stamp); err != nil {
				return err
			}
		}
		return nil
	})
	return recoveries, err
}

func (r *InferenceJournalRuntime) Prune(now time.Time) (int64, error) {
	if r == nil {
		return 0, nil
	}
	done, err := r.beginUse()
	if err != nil {
		return 0, err
	}
	defer done()
	cutoff := journalTime(now.Add(-inferenceJournalRetention))
	var deleted int64
	err = r.store.transaction(func(tx *persistence.Tx) error {
		deleted = 0
		operations, err := readDocumentRows[inferenceOperationDocument](tx, "inference_operations", "")
		if err != nil {
			return err
		}
		for _, op := range operations {
			if op.WorkspaceScope != r.workspaceScope || op.Status == "active" || op.UpdatedAt >= cutoff {
				continue
			}
			for _, id := range op.AttemptIDs {
				attempt, err := requireInferenceDocument[inferenceAttemptDocument](tx, "inference_attempts", id)
				if err != nil {
					return err
				}
				for _, submissionID := range attempt.SubmissionIDs {
					tx.Delete(documentNamespace("inference_submissions"), submissionID)
				}
				tx.Delete(documentNamespace("inference_attempts"), id)
			}
			tx.Delete(documentNamespace("inference_operations"), op.ID)
			workflow, err := requireInferenceDocument[inferenceWorkflowDocument](tx, "inference_workflows", op.WorkflowID)
			if err != nil {
				return err
			}
			workflow.OperationIDs = slices.DeleteFunc(workflow.OperationIDs, func(id string) bool { return id == op.ID })
			if err := tx.Put(documentNamespace("inference_workflows"), workflow.ID, workflow); err != nil {
				return err
			}
			deleted++
		}
		workflows, err := readDocumentRows[inferenceWorkflowDocument](tx, "inference_workflows", "")
		if err != nil {
			return err
		}
		for _, workflow := range workflows {
			if workflow.WorkspaceScope == r.workspaceScope && workflow.Status != "active" && workflow.UpdatedAt < cutoff && len(workflow.OperationIDs) == 0 {
				tx.Delete(documentNamespace("inference_workflows"), workflow.ID)
				deleted++
			}
		}
		return nil
	})
	return deleted, err
}
