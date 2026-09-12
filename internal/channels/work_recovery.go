package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type WorkRunRecoveryState string

const (
	WorkRunRecoveryActive      WorkRunRecoveryState = "active"
	WorkRunRecoveryCompleted   WorkRunRecoveryState = "completed"
	WorkRunRecoveryFailed      WorkRunRecoveryState = "failed"
	WorkRunRecoveryInterrupted WorkRunRecoveryState = "interrupted"
	WorkRunRecoveryMissing     WorkRunRecoveryState = "missing"
)

type WorkRunRecovery struct {
	RunID      string
	SessionRef string
	State      WorkRunRecoveryState
}

func (s *Service) ListUnsettledWorkRuns(ctx context.Context) ([]WorkRun, error) {
	rows, err := s.db.QueryContext(ctx, workRunSelect+`
		WHERE run.state IN ('queued', 'running') AND run.session_ref IS NOT NULL
		ORDER BY run.created_at, run.id`)
	if err != nil {
		return nil, fmt.Errorf("list unsettled work runs: %w", err)
	}
	defer rows.Close()
	var runs []WorkRun
	for rows.Next() {
		run, err := scanWorkRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// ReconcileWorkRuns settles persisted handles against Core Session state. It
// never marks Work completed: a completed verifier session still owes a
// revision-safe chat_verify receipt, while a missing runner becomes unknown or
// interrupted and wakes the visible owner with an honest explanation.
func (s *Service) ReconcileWorkRuns(ctx context.Context, recoveries []WorkRunRecovery) error {
	for _, recovery := range recoveries {
		if recovery.State == WorkRunRecoveryActive {
			continue
		}
		if err := s.reconcileWorkRun(ctx, recovery); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileWorkRun(ctx context.Context, recovery WorkRunRecovery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	run, err := scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+`
		WHERE run.id = ? AND run.session_ref = ?`, strings.TrimSpace(recovery.RunID), strings.TrimSpace(recovery.SessionRef)))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if run.State != WorkRunRunning && run.State != WorkRunQueued {
		return nil
	}
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, run.WorkID))
	if err != nil {
		return err
	}
	now := fromMillis(toMillis(s.now()))
	runState := WorkRunCompleted
	outcome := "session completed; result receipt pending"
	workState := work.State
	verificationState := work.VerificationState
	recipientID, recipientSession, err := workResultRecipientTx(ctx, tx, work, run.SessionRef)
	if err != nil {
		return err
	}
	deliveryBody := fmt.Sprintf("Background %s run %s completed during restart recovery. Read session %s, then submit its revision-safe result.", run.Kind, run.ID, run.SessionRef)
	bindingState := CollaborationSessionIdle
	switch recovery.State {
	case WorkRunRecoveryFailed:
		runState = WorkRunFailed
		outcome = "session turn failed during restart recovery"
		deliveryBody = fmt.Sprintf("Background %s run %s failed before restart recovery completed. Inspect session %s and decide whether to retry.", run.Kind, run.ID, run.SessionRef)

		if recipientID == "" {
		}
	case WorkRunRecoveryInterrupted:
		runState = WorkRunInterrupted
		outcome = "session turn was interrupted during restart recovery"
		bindingState = CollaborationSessionInterrupted
		deliveryBody = fmt.Sprintf("Work %s %s run was interrupted before restart recovery completed. No completion or rollback is being claimed. Review session %s and decide whether to retry.", work.ID, run.Kind, run.SessionRef)
	case WorkRunRecoveryMissing:
		runState = WorkRunInterrupted
		outcome = "session handle missing during restart recovery"
		workState = WorkInterrupted
		bindingState = CollaborationSessionMissing
		deliveryBody = fmt.Sprintf("Work %s %s run was interrupted during restart recovery because session %s could not be found. No completion or rollback is being claimed. Review existing artifacts and decide whether to retry.", work.ID, run.Kind, run.SessionRef)
		if run.Kind == WorkRunVerifier {
			workState = WorkNeedsHuman
			verificationState = WorkVerificationUnknown
		}
	default:

	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE work_runs SET state = ?, outcome = ?, ended_at = ?, updated_at = ? WHERE id = ?`,
		runState, outcome, toMillis(now), toMillis(now), run.ID); err != nil {
		return fmt.Errorf("settle recovered work run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE collaboration_session_bindings SET state = ?, run_id = NULL, updated_at = ?
		WHERE session_ref = ? AND run_id = ?`, bindingState, toMillis(now), run.SessionRef, run.ID); err != nil {
		return fmt.Errorf("settle recovered work session: %w", err)
	}
	var activeRunRef string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE((
			SELECT id FROM work_runs
			WHERE work_id = ? AND state IN ('queued', 'running')
			ORDER BY created_at DESC, id DESC LIMIT 1
		), '')`, work.ID).Scan(&activeRunRef); err != nil {
		return fmt.Errorf("find surviving work run: %w", err)
	}
	interruptRecovery := recovery.State == WorkRunRecoveryMissing || recovery.State == WorkRunRecoveryInterrupted || recovery.State == WorkRunRecoveryFailed
	interruptWork := interruptRecovery && activeRunRef == ""
	if interruptWork {
		workState = WorkInterrupted
		if run.Kind == WorkRunVerifier {
			workState = WorkNeedsHuman
			verificationState = WorkVerificationUnknown
		}
	} else if interruptRecovery {
		workState = work.State
		verificationState = work.VerificationState
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE works SET state = ?, verification_state = ?, current_run_ref = ?,
			failure_reason = CASE WHEN ? THEN ? ELSE failure_reason END, updated_at = ?
		WHERE id = ?`, workState, verificationState, nullableString(activeRunRef), boolInt(interruptWork), outcome, toMillis(now), work.ID); err != nil {
		return fmt.Errorf("settle recovered work: %w", err)
	}
	if err := insertWorkEventTx(ctx, tx, WorkEvent{WorkID: work.ID, Kind: "recovery", State: string(workState), Summary: outcome, GoalRevision: run.GoalRevision, CandidateRevision: run.CandidateRevision, CreatedAt: now}); err != nil {
		return err
	}
	shouldDeliver := false
	if recipientID != "" {
		kind := CollaborationCompletion
		if interruptRecovery {
			kind = CollaborationVerificationFeedback
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: work.RoomID, ToAgentID: recipientID, TargetSessionRef: recipientSession, WorkID: work.ID, Kind: kind,
			Body: deliveryBody, GoalRevision: run.GoalRevision, CandidateRevision: run.CandidateRevision,
			CreatedAt: now,
		}); err != nil {
			return err
		}
		shouldDeliver, err = requestWakeTx(ctx, tx, recipientID, toMillis(now))
		if err != nil {
			return err
		}
	}
	run.State, run.Outcome, run.EndedAt = runState, outcome, now
	terminalWakeIDs, err := s.enqueueWorkRunTerminalTx(ctx, tx, work, run, now)
	if err != nil {
		return err
	}
	admittedWakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, now)
	if err != nil {
		return err
	}
	terminalWakeIDs = appendUniqueStrings(terminalWakeIDs, admittedWakeIDs...)
	if err := tx.Commit(); err != nil {
		return err
	}
	if shouldDeliver && s.wake != nil {
		s.wake.Deliver(recipientID)
	}
	if s.wake != nil {
		for _, id := range terminalWakeIDs {
			if !shouldDeliver || id != recipientID {
				s.wake.Deliver(id)
			}
		}
	}
	return nil
}
