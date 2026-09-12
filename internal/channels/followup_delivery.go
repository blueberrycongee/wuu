package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FireDueFollowups commits the occurrence and its delivery in one transaction.
// Retrying after a crash cannot lose a claimed occurrence or duplicate a post.
func (s *Service) FireDueFollowups(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now()
	rows, err := tx.QueryContext(ctx, `SELECT spec FROM collaboration_followups WHERE state='active' AND next_at<=? ORDER BY next_at,id`, toMillis(now))
	if err != nil {
		return nil, err
	}
	var due []Followup
	for rows.Next() {
		f, e := scanFollowup(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		due = append(due, f)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var wakes []string
	for _, f := range due {
		blocked, e := followupBlockReasonTx(ctx, tx, f)
		if e != nil {
			return nil, e
		}
		if blocked != "" {
			f.State, f.Reason = "blocked", blocked
			f.Revision++
			if err = saveFollowupTx(ctx, tx, f); err != nil {
				return nil, err
			}
			continue
		}
		if f.WhenSession != "" {
			watched, e := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref=?`, f.WhenSession))
			if errors.Is(e, ErrNotFound) {
				f.State, f.Reason = "blocked", "The watched session no longer exists"
				f.Revision++
				if err = saveFollowupTx(ctx, tx, f); err != nil {
					return nil, err
				}
				continue
			}
			if e != nil {
				return nil, e
			}
			switch watched.State {
			case CollaborationSessionStarting, CollaborationSessionRunning, CollaborationSessionQueued, CollaborationSessionWaiting:
				continue
			}
		}
		occurrence := fmt.Sprintf("%s:%d:%d", f.ID, f.Revision, toMillis(f.NextAt))
		if f.Mode == "message" {
			// Scheduled user reminders are public facts, not new work for every member.
			var seq int64
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0)+1 FROM room_messages WHERE room_id=?`, f.RoomID).Scan(&seq); err != nil {
				return nil, err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO room_messages(id,room_id,seq,author_type,author_id,kind,body,mentions_json,created_at) VALUES(?,?,?,'agent',?,'text',?,'[]',?)`, ConversationReplyID(f.ID, occurrence), f.RoomID, seq, f.OwnerID, f.Note, toMillis(now))
			if err != nil {
				return nil, err
			}
		} else {
			targetKind, targetID := CollaborationTargetNamedAgent, f.OwnerID
			if f.SessionRef != "" {
				targetKind, targetID = CollaborationTargetSession, f.SessionRef
			}
			body := fmt.Sprintf("Scheduled continuation: %s\nReason: %s\nRead current state before acting; this reminder is not new human authorization. References: %v", f.ID, f.Note, f.Refs)
			if f.WhenSession != "" {
				body += fmt.Sprintf("\nSession %s reached a terminal or idle state; inspect its result.", f.WhenSession)
			}
			_, err = enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: f.RoomID, FromType: MemberAgent, FromID: f.OwnerID, FromSessionRef: f.SourceSessionRef, ToAgentID: f.OwnerID, TargetSessionRef: f.SessionRef, Kind: CollaborationControl, Body: body, WorkID: f.WorkID, GoalRevision: f.GoalRevision, ArtifactRefs: f.Refs, TargetKind: targetKind, TargetID: targetID, Visibility: CollaborationVisibilityPrivate, CorrelationID: f.ID, RequestID: occurrence, CreatedAt: now})
			if err != nil {
				return nil, err
			}
			if _, err = requestWakeTx(ctx, tx, f.OwnerID, toMillis(now)); err != nil {
				return nil, err
			}
			wakes = appendUniqueStrings(wakes, f.OwnerID)
		}
		f.LastAt = &now
		f.Revision++
		f.State = "done"
		if f.Cron != "" {
			f.NextAt, err = nextFollowupTime(f.Cron, f.Timezone, now)
			if err != nil {
				return nil, err
			}
			f.State = "active"
		}
		if err = saveFollowupTx(ctx, tx, f); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if s.wake != nil {
		for _, id := range wakes {
			s.wake.Deliver(id)
		}
	}
	return wakes, nil
}

func followupBlockReasonTx(ctx context.Context, tx *sql.Tx, f Followup) (string, error) {
	if err := requireRoomPrincipalAccessTx(ctx, tx, f.RoomID, f.OwnerID); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrNotFound) {
			return "The owner no longer has access to this room", nil
		}
		return "", err
	}
	if f.SessionRef != "" {
		b, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref=?`, f.SessionRef))
		if errors.Is(err, ErrNotFound) {
			return "The target session no longer exists", nil
		}
		if err != nil {
			return "", err
		}
		if b.PrincipalID != f.OwnerID || b.RoomID != f.RoomID {
			return "The session ownership changed", nil
		}
		switch b.State {
		case CollaborationSessionCancelled, CollaborationSessionInterrupted, CollaborationSessionMissing, CollaborationSessionFailed:
			return "The session stopped; explicitly resume it before arranging another continuation", nil
		}
	}
	if f.WorkID != "" {
		w, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id=?`, f.WorkID))
		if errors.Is(err, ErrNotFound) {
			return "The task no longer exists", nil
		}
		if err != nil {
			return "", err
		}
		if w.RoomID != f.RoomID || terminalWorkState(w.State) || w.GoalRevision != f.GoalRevision {
			return "The task ended or its goal changed", nil
		}
	}
	return "", nil
}

func cancelSessionFollowupsTx(ctx context.Context, tx *sql.Tx, ref string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT spec FROM collaboration_followups WHERE session_ref=? AND state IN ('active','paused','done')`, ref)
	if err != nil {
		return err
	}
	var entries []Followup
	for rows.Next() {
		f, e := scanFollowup(rows)
		if e != nil {
			rows.Close()
			return e
		}
		entries = append(entries, f)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range entries {
		f.State = "cancelled"
		f.Revision++
		if err = saveFollowupTx(ctx, tx, f); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=? WHERE correlation_id=? AND consumed_at IS NULL`, toMillis(now), f.ID); err != nil {
			return err
		}
	}
	return nil
}
