package channels

import (
	"context"
	"fmt"
)

// SetCandidateDisposition records a human decision without deleting execution
// history or a workspace that may contain subsequent work.
func (s *Service) SetCandidateDisposition(ctx context.Context, workID, artifactID, disposition string, expectedRevision int, apply func() error) error {
	if disposition != "applied" && disposition != "discarded" {
		return fmt.Errorf("invalid candidate disposition")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id=?`, workID))
	if err != nil {
		return err
	}
	if expectedRevision != work.Revision {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE work_artifacts SET disposition=? WHERE id=? AND work_id=? AND kind='candidate' AND disposition IN ('',?)`, disposition, artifactID, workID, disposition)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	if apply != nil {
		if err := apply(); err != nil {
			return err
		}
	}
	var wake bool
	var interrupted []workSessionInterruptTarget
	if disposition == "discarded" && work.CandidateArtifactRef == artifactID {
		now := toMillis(s.now())
		rows, err := tx.QueryContext(ctx, `SELECT COALESCE(named_agent_id,''),COALESCE(session_ref,'') FROM work_runs WHERE work_id=? AND kind='verifier' AND state IN ('queued','running')`, workID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var target workSessionInterruptTarget
			if err := rows.Scan(&target.agentID, &target.sessionRef); err != nil {
				rows.Close()
				return err
			}
			interrupted = append(interrupted, target)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state='interrupted', outcome='candidate discarded', ended_at=?, updated_at=? WHERE work_id=? AND kind='verifier' AND state IN ('queued','running')`, now, now, workID); err != nil {
			return err
		}
		if err := refreshWorkCurrentRunRefTx(ctx, tx, workID, s.now()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE works SET candidate_artifact_ref=NULL,candidate_workspace_revision=NULL,candidate_revision=candidate_revision+1,state='needs_human',verification_state='unknown',updated_at=? WHERE id=?`, now, workID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE room_messages SET task_state='needs_human',task_candidate_revision=task_candidate_revision+1 WHERE id=?`, workID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM task_verifications WHERE task_id=?`, workID); err != nil {
			return err
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: work.RoomID, ToAgentID: work.OwnerNamedAgentID, WorkID: work.ID, Kind: CollaborationControl, Visibility: CollaborationVisibilitySystem, Body: "The user discarded the current candidate. Do not deliver it as completed work. Read the current Work and ask what should change if no new instructions are available.", GoalRevision: work.GoalRevision, CandidateRevision: work.CandidateRevision + 1, CreatedAt: s.now()}); err != nil {
			return err
		}
		wake, err = requestWakeTx(ctx, tx, work.OwnerNamedAgentID, now)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.interruptWorkSessions(interrupted)
	if wake && s.wake != nil {
		s.wake.Deliver(work.OwnerNamedAgentID)
	}
	return nil
}
