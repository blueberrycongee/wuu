package channels

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const workProgressTimeout = 30 * time.Minute

func (s *Service) migrateWorkDeadlines() error {
	_, err := s.db.Exec(fmt.Sprintf(`
 CREATE TRIGGER IF NOT EXISTS work_state_clock_insert AFTER INSERT ON works
 BEGIN UPDATE works SET state_deadline_at=NEW.created_at+%d WHERE id=NEW.id; END;
 CREATE TRIGGER IF NOT EXISTS work_state_clock_update AFTER UPDATE OF state,goal_revision ON works
 WHEN NEW.state!=OLD.state OR NEW.goal_revision!=OLD.goal_revision
 BEGIN UPDATE works SET state_deadline_at=CASE WHEN NEW.state IN ('open','working','checking','revising','integrating','interrupted') THEN NEW.updated_at+%d ELSE NULL END WHERE id=NEW.id; END;`, workProgressTimeout.Milliseconds(), workProgressTimeout.Milliseconds()))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE works SET state_deadline_at=updated_at+? WHERE state_deadline_at IS NULL AND state IN ('open','working','checking','revising','integrating','interrupted')`, workProgressTimeout.Milliseconds())
	return err
}

func (s *Service) expireWorkStatesTx(ctx context.Context, tx *sql.Tx, now time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx, workSelect+` WHERE work.state_deadline_at<=? AND work.state IN ('open','working','checking','revising','integrating','interrupted')`, toMillis(now))
	if err != nil {
		return nil, err
	}
	var works []Work
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		works = append(works, work)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var wakes []string
	for _, work := range works {
		reason := fmt.Sprintf("Work made no state transition before its %s deadline. Inspect recorded progress and explain the blocker or explicitly continue the task.", work.State)
		if _, err := tx.ExecContext(ctx, `UPDATE works SET state='needs_human',failure_reason=?,updated_at=? WHERE id=?`, reason, toMillis(now), work.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE room_messages SET task_state='needs_human' WHERE id=?`, work.ID); err != nil {
			return nil, err
		}
		if err := insertWorkEventTx(ctx, tx, WorkEvent{WorkID: work.ID, Kind: "recovery", State: string(WorkNeedsHuman), Summary: reason, GoalRevision: work.GoalRevision, CandidateRevision: work.CandidateRevision, CreatedAt: now}); err != nil {
			return nil, err
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: work.RoomID, ToAgentID: work.OwnerNamedAgentID, WorkID: work.ID, Kind: CollaborationControl, Visibility: CollaborationVisibilitySystem, Body: reason, GoalRevision: work.GoalRevision, CandidateRevision: work.CandidateRevision, CreatedAt: now}); err != nil {
			return nil, err
		}
		wake, err := requestWakeTx(ctx, tx, work.OwnerNamedAgentID, toMillis(now))
		if err != nil {
			return nil, err
		}
		if wake {
			wakes = appendUniqueStrings(wakes, work.OwnerNamedAgentID)
		}
	}
	return wakes, nil
}
