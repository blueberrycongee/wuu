package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// CancelCollaborationSessions atomically fences a session subtree and records
// its cancellation outcome for the external parent. It does not require a
// model turn, so queued children can release a waiting parent too. The host
// interrupts every returned execution after commit, including on retries.
func (s *Service) CancelCollaborationSessions(ctx context.Context, sessionRef string) ([]CollaborationSessionBinding, error) {
	sessionRef = strings.TrimSpace(sessionRef)
	if sessionRef == "" {
		return nil, errors.New("collaboration session ref is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	bindings, err := collaborationSessionSubtreeTx(ctx, tx, sessionRef)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, ErrNotFound
	}
	now := fromMillis(toMillis(s.now()))
	cancelled := make(map[string]bool, len(bindings))
	var root CollaborationSessionBinding
	for _, binding := range bindings {
		cancelled[binding.SessionRef] = true
		if binding.SessionRef == sessionRef {
			root = binding
		}
	}
	var wakeIDs []string
	for i, binding := range bindings {
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=? WHERE id IN (SELECT delivery_id FROM room_turns WHERE session_ref=? OR delivery_id IN (SELECT id FROM collaboration_messages WHERE target_session_ref=?)) AND consumed_at IS NULL`, toMillis(now), binding.SessionRef, binding.SessionRef); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM room_turns WHERE session_ref=? OR delivery_id IN (SELECT id FROM collaboration_messages WHERE target_session_ref=?)`, binding.SessionRef, binding.SessionRef); err != nil {
			return nil, err
		}
		if err := cancelSessionFollowupsTx(ctx, tx, binding.SessionRef, now); err != nil {
			return nil, err
		}
		// Resolve active runs by their durable session address as well as the
		// binding handle. This also settles a partially recovered cancelled binding
		// whose old run has not yet received its terminal notification.
		rows, err := tx.QueryContext(ctx, workRunSelect+` WHERE (run.session_ref = ? OR run.id = ?) AND run.state IN ('queued', 'running')`, binding.SessionRef, binding.RunID)
		if err != nil {
			return nil, err
		}
		var runs []WorkRun
		for rows.Next() {
			run, err := scanWorkRun(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			runs = append(runs, run)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		for _, run := range runs {
			if run.WorkID != binding.WorkID || run.NamedAgentID != binding.NamedAgentID || run.SessionRef != "" && run.SessionRef != binding.SessionRef {
				return nil, fmt.Errorf("%w: active run has a different session scope", ErrConflict)
			}
			work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, run.WorkID))
			if err != nil {
				return nil, err
			}
			run.State, run.Outcome, run.EndedAt, run.UpdatedAt = WorkRunCancelled, "collaboration session cancelled", now, now
			if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state = 'cancelled', outcome = ?, ended_at = ?, updated_at = ? WHERE id = ? AND state IN ('queued', 'running')`, run.Outcome, toMillis(now), toMillis(now), run.ID); err != nil {
				return nil, err
			}
			if err := refreshWorkCurrentRunRefTx(ctx, tx, work.ID, now); err != nil {
				return nil, err
			}
			terminalWakeIDs, err := s.enqueueWorkRunTerminalTx(ctx, tx, work, run, now)
			if err != nil {
				return nil, err
			}
			wakeIDs = appendUniqueStrings(wakeIDs, terminalWakeIDs...)
		}
		if binding.State != CollaborationSessionCancelled || binding.RunID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = 'cancelled', run_id = NULL, failure_reason = '', updated_at = ? WHERE session_ref = ?`, toMillis(now), binding.SessionRef); err != nil {
				return nil, err
			}
			bindings[i].State, bindings[i].RunID, bindings[i].FailureReason, bindings[i].UpdatedAt = CollaborationSessionCancelled, "", "", now
		}
	}
	// Internal parents stop as part of the same transaction. Only the subtree's
	// external parent needs a result to continue making progress.
	if root.State != CollaborationSessionCancelled && root.ParentSessionRef != "" && !cancelled[root.ParentSessionRef] {
		parent, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, root.ParentSessionRef))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if err == nil && parent.RoomID == root.RoomID {
			sourceAccess := requireRoomPrincipalAccessTx(ctx, tx, root.RoomID, root.PrincipalID)
			targetAccess := requireRoomPrincipalAccessTx(ctx, tx, parent.RoomID, parent.PrincipalID)
			if sourceAccess != nil && !errors.Is(sourceAccess, ErrUnauthorized) {
				return nil, sourceAccess
			}
			if targetAccess != nil && !errors.Is(targetAccess, ErrUnauthorized) {
				return nil, targetAccess
			}
			if sourceAccess == nil && targetAccess == nil {
				requestID, err := randomID("session-cancel", 12)
				if err != nil {
					return nil, err
				}
				title := root.Title
				if title == "" {
					title = root.SessionRef
				}
				if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
					RoomID: root.RoomID, FromType: MemberAgent, FromID: root.PrincipalID, FromSessionRef: root.SessionRef,
					ToAgentID: parent.PrincipalID, TargetSessionRef: parent.SessionRef, WorkID: parent.WorkID,
					Kind: CollaborationCompletion, Body: fmt.Sprintf("Session %s (%s) was cancelled. Its pending execution will not continue unless explicitly resumed.", title, root.SessionRef),
					TargetKind: CollaborationTargetSession, TargetID: parent.SessionRef, Visibility: CollaborationVisibilityPrivate,
					CorrelationID: root.TurnID, RequestID: requestID, TerminalState: CollaborationTerminalCancelled, CreatedAt: now,
				}); err != nil {
					return nil, err
				}
				if acceptsCollaborationSessionDelivery(parent.State) {
					if _, err := requestWakeTx(ctx, tx, parent.PrincipalID, toMillis(now)); err != nil {
						return nil, err
					}
					wakeIDs = appendUniqueStrings(wakeIDs, parent.PrincipalID)
				}
			}
		}
	}
	admittedWakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	wakeIDs = appendUniqueStrings(wakeIDs, admittedWakeIDs...)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.wake != nil {
		for _, identity := range wakeIDs {
			s.wake.Deliver(identity)
		}
	}
	return bindings, nil
}

func collaborationSessionSubtreeTx(ctx context.Context, tx *sql.Tx, sessionRef string) ([]CollaborationSessionBinding, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE subtree(session_ref) AS (
  SELECT ? UNION SELECT child.session_ref FROM collaboration_session_bindings child JOIN subtree parent ON child.parent_session_ref = parent.session_ref
 ) `+collaborationSessionSelect+` JOIN subtree ON subtree.session_ref = binding.session_ref ORDER BY CASE WHEN binding.session_ref = ? THEN 0 ELSE 1 END, binding.created_at, binding.session_ref`, sessionRef, sessionRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]CollaborationSessionBinding, 0)
	for rows.Next() {
		binding, err := scanCollaborationSession(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}
