package channels

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
)

// NamedAgentConversationRef is stable across rooms, work items and restarts.
// It retains the identity's original conversation history on upgrade.
func NamedAgentConversationRef(agent AgentRuntime) string {
	sum := sha256.Sum256([]byte(agent.ID))
	return agent.CreatedAt.UTC().Format("20060102-150405") + fmt.Sprintf("-%x", sum[:8])
}

// PrepareIdentityConversation selects one room/work from the durable inbox.
// Room scope belongs to the admitted turn, not to a new conversation. The host
// must first ensure that no legacy session still has an execution lease.
func (s *Service) PrepareIdentityConversation(ctx context.Context, agent AgentRuntime, defaults CollaborationSessionBindParams) (CollaborationSessionBinding, bool, error) {
	if agent.IsRoomRuntime() {
		return CollaborationSessionBinding{}, false, ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaborationSessionBinding{}, false, err
	}
	defer tx.Rollback()
	ref := NamedAgentConversationRef(agent)
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO named_agent_conversations(agent_id, session_ref) VALUES (?, ?)`, agent.ID, ref); err != nil {
		return CollaborationSessionBinding{}, false, err
	}
	current, findErr := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, ref))
	if findErr != nil && !errors.Is(findErr, ErrNotFound) {
		return current, false, findErr
	}
	if findErr == nil && (current.State == CollaborationSessionStarting || current.State == CollaborationSessionRunning || current.State == CollaborationSessionQueued || current.State == CollaborationSessionCancelled) {
		if current.RunID != "" && current.State != CollaborationSessionCancelled {
			var reserved bool
			if err := tx.QueryRowContext(ctx, `SELECT state IN ('queued','running') AND COALESCE(turn_id,'')='' FROM work_runs WHERE id=?`, current.RunID).Scan(&reserved); err != nil {
				return current, false, err
			}
			if reserved {
				return current, true, tx.Commit()
			}
		}
		return current, false, tx.Commit()
	}
	// Historical work sessions remain readable, but their unconsumed deliveries
	// must enter the identity inbox instead of waking a second execution context.
	if _, err = tx.ExecContext(ctx, `UPDATE collaboration_messages SET target_session_ref = NULL
		WHERE to_agent_id = ? AND pulled_at IS NULL AND invalidated_at IS NULL`, agent.ID); err != nil {
		return current, false, err
	}
	// A task may finish before its queued assignment is admitted. Keep its
	// later control/result messages, but never restart the finished task.
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=?
        WHERE to_agent_id=? AND kind='assignment' AND pulled_at IS NULL AND invalidated_at IS NULL
        AND EXISTS(SELECT 1 FROM works WHERE works.id=collaboration_messages.work_id AND works.state IN ('completed','cancelled','failed'))`, toMillis(s.now()), agent.ID); err != nil {
		return current, false, err
	}
	var roomID, workID string
	err = tx.QueryRowContext(ctx, `SELECT delivery.room_id,
		CASE WHEN work.state NOT IN ('completed','cancelled','failed') THEN COALESCE(delivery.work_id,'') ELSE '' END
		FROM collaboration_messages delivery LEFT JOIN works work ON work.id=delivery.work_id
		WHERE delivery.to_agent_id=? AND delivery.pulled_at IS NULL AND delivery.invalidated_at IS NULL
		AND EXISTS(SELECT 1 FROM room_members member WHERE member.room_id=delivery.room_id AND member.member_type='agent' AND member.member_id=?)
		ORDER BY CASE WHEN EXISTS(SELECT 1 FROM work_runs run WHERE run.work_id=delivery.work_id AND run.named_agent_id=delivery.to_agent_id AND run.state='running') THEN 0 ELSE 1 END,
		delivery.created_at, delivery.rowid LIMIT 1`, agent.ID, agent.ID).Scan(&roomID, &workID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT inbox.room_id FROM inbox_items inbox
			WHERE inbox.member_type='agent' AND inbox.member_id=? AND inbox.pulled_at IS NULL AND inbox.kind IN ('task','reminder')
			AND EXISTS(SELECT 1 FROM room_members member WHERE member.room_id=inbox.room_id AND member.member_type='agent' AND member.member_id=?)
			ORDER BY inbox.created_at, inbox.rowid LIMIT 1`, agent.ID, agent.ID).Scan(&roomID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return current, false, tx.Commit()
	}
	if err != nil {
		return current, false, err
	}
	var runID string
	var runKind WorkRunKind
	var runState WorkRunState
	if workID != "" {
		err = tx.QueryRowContext(ctx, `SELECT id,kind,state FROM work_runs WHERE work_id=? AND named_agent_id=? AND state IN ('queued','running')
			ORDER BY CASE state WHEN 'running' THEN 0 ELSE 1 END,created_at,id LIMIT 1`, workID, agent.ID).Scan(&runID, &runKind, &runState)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return current, false, err
		}
		if runID != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET run_id=NULL WHERE run_id=? AND session_ref!=?`, runID, ref); err != nil {
				return current, false, err
			}
			// Transfer the reservation, including verifier identity and revisions,
			// into the continuing conversation before the host starts inference.
			if _, err = tx.ExecContext(ctx, `UPDATE work_runs SET session_ref=?,updated_at=? WHERE id=?`, ref, toMillis(s.now()), runID); err != nil {
				return current, false, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state='completed', failure_reason='Continued in the identity conversation', updated_at=?
		WHERE principal_id=? AND session_ref!=? AND state IN ('idle','waiting','starting','running','queued')`, toMillis(s.now()), agent.ID, ref); err != nil {
		return current, false, err
	}
	defaults.SessionRef, defaults.PrincipalID, defaults.RoomID, defaults.WorkID = ref, agent.ID, roomID, workID
	defaults.RunID, defaults.ParentSessionRef = runID, ""
	defaults.Purpose, defaults.State, defaults.Title = CollaborationSessionConversation, CollaborationSessionQueued, agent.Name
	if runID != "" {
		defaults.State = collaborationSessionStateForRun(runState)
		if runKind == WorkRunVerifier {
			defaults.Purpose = CollaborationSessionVerification
		} else if runKind != WorkRunProducer {
			defaults.Purpose = CollaborationSessionWork
		}
	}
	binding, err := bindCollaborationSessionTx(ctx, tx, agent, defaults, s.now())
	if err != nil {
		return current, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET turn_id='', parent_session_ref='', objective='' WHERE session_ref=?`, ref); err != nil {
		return current, false, err
	}
	// Only the selected work and ordinary follow-ups enter this turn. Other jobs
	// stay durable and cannot run concurrently under the same name.
	if _, err = tx.ExecContext(ctx, `UPDATE collaboration_messages SET target_session_ref=?
		WHERE to_agent_id=? AND room_id=? AND pulled_at IS NULL AND invalidated_at IS NULL
		AND (COALESCE(work_id,'')=? OR work_id IS NULL OR kind IN ('candidate_ready','peer_result','work_run_terminal','verification_feedback','completion')
		OR kind='control' AND EXISTS(SELECT 1 FROM works WHERE works.id=collaboration_messages.work_id AND works.state IN ('completed','cancelled','failed')))`, ref, agent.ID, roomID, workID); err != nil {
		return binding, false, err
	}
	binding.Primary, binding.TurnID, binding.ParentSessionRef, binding.Objective = true, "", "", ""
	return binding, true, tx.Commit()
}
