package channels

import (
	"context"
	"database/sql"
)

// Commit usage, evidence and the initiating party's notification with the turn.
// A persistent identity does not need a parent conversation to return work.
func (s *Service) settleIdentityWorkTx(ctx context.Context, tx *sql.Tx, binding CollaborationSessionBinding, scope CollaborationTurnScope, params CollaborationSessionSettleParams, state CollaborationSessionState) ([]string, error) {
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id=?`, scope.WorkID))
	if err != nil {
		return nil, err
	}
	now := s.now()
	if params.AdmissionFailure && !terminalWorkState(work.State) {
		if _, err := tx.ExecContext(ctx, `UPDATE works SET state='needs_human',failure_reason=?,updated_at=? WHERE id=?`, params.FailureReason, toMillis(now), work.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE room_messages SET task_state='needs_human' WHERE id=?`, work.ID); err != nil {
			return nil, err
		}
		if err := insertWorkEventTx(ctx, tx, WorkEvent{WorkID: work.ID, Kind: "state", State: string(WorkNeedsHuman), Summary: params.FailureReason, GoalRevision: scope.GoalRevision, CandidateRevision: work.CandidateRevision, CreatedAt: now}); err != nil {
			return nil, err
		}
	}
	runState := params.RunState
	if runState == "" {
		runState = WorkRunCompleted
		if params.State == CollaborationSessionFailed {
			runState = WorkRunFailed
		} else if params.State == CollaborationSessionInterrupted {
			runState = WorkRunInterrupted
		}
	}
	var run WorkRun
	if scope.RunID != "" {
		run, err = scanWorkRun(tx.QueryRowContext(ctx, workRunSelect+` WHERE run.id=?`, scope.RunID))
		if err != nil {
			return nil, err
		}
		if run.State == WorkRunRunning || run.State == WorkRunQueued || run.State == WorkRunCompleted || run.State == WorkRunFailed {
			qualified := false
			currentCandidate := run.CandidateRevision == work.CandidateRevision || work.PromotionRunRef == run.ID && run.CandidateRevision+1 == work.CandidateRevision
			if runState == WorkRunCompleted && run.Kind == WorkRunProducer && currentCandidate {
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_artifacts WHERE run_id=? AND kind='candidate')`, run.ID).Scan(&qualified); err != nil {
					return nil, err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state=?,outcome=?,provider=?,model=?,input_tokens=?,output_tokens=?,qualified=?,ended_at=?,updated_at=? WHERE id=?`, runState, params.Result, params.Provider, params.Model, params.InputTokens, params.OutputTokens, qualified, toMillis(now), toMillis(now), run.ID); err != nil {
				return nil, err
			}
			if qualified != run.Qualified {
				if _, err := tx.ExecContext(ctx, `UPDATE works SET qualified_candidates=MAX(0,qualified_candidates+?) WHERE id=?`, boolInt(qualified)-boolInt(run.Qualified), work.ID); err != nil {
					return nil, err
				}
			}
			run.State, run.Qualified = runState, qualified
			if err := refreshWorkCurrentRunRefTx(ctx, tx, work.ID, now); err != nil {
				return nil, err
			}
		}
	}
	wakeIDs := []string{}
	if state != CollaborationSessionWaiting {
		recipient, target, err := workResultRecipientTx(ctx, tx, work, binding.SessionRef)
		if err != nil {
			return nil, err
		}
		if recipient != binding.PrincipalID {
			if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
				RoomID: work.RoomID, WorkID: work.ID, FromType: MemberAgent, FromID: binding.PrincipalID,
				FromSessionRef: binding.SessionRef, ToAgentID: recipient, TargetSessionRef: target,
				Kind: CollaborationCompletion, Visibility: CollaborationVisibilityPrivate, Body: params.Result,
				GoalRevision: scope.GoalRevision, CandidateRevision: work.CandidateRevision,
				CorrelationID: params.TurnID, RequestID: "work-turn:" + params.TurnID,
				TerminalState: collaborationTerminalStateForRun(runState), CreatedAt: now,
			}); err != nil {
				return nil, err
			}
			if _, err := requestWakeTx(ctx, tx, recipient, toMillis(now)); err != nil {
				return nil, err
			}
			wakeIDs = append(wakeIDs, recipient)
		}
	}
	admitted, err := s.admitQueuedWorkRunsTx(ctx, tx, now)
	return appendUniqueStrings(wakeIDs, admitted...), err
}
