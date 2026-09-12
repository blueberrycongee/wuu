package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ReceiveCollaboration durably claims messages for a session without marking
// them consumed. Repeat calls return the same unacknowledged deliveries, so a
// crash before persisting agent input cannot lose a message. The host should
// acknowledge only after the turn input, including delivery IDs, is durable.
func (s *Service) ReceiveCollaboration(ctx context.Context, agentID, token, sessionRef string, limit int) ([]CollaborationMessage, error) {
	actor, err := s.AuthenticatePrincipal(ctx, agentID, token)
	if err != nil {
		return nil, err
	}
	sessionRef = strings.TrimSpace(sessionRef)
	if sessionRef == "" {
		return nil, errors.New("collaboration session ref is required")
	}
	if limit <= 0 || limit > checkLimit {
		limit = checkLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, sessionRef))
	if err != nil {
		return nil, err
	}
	if binding.PrincipalID != actor.ID {
		return nil, ErrUnauthorized
	}
	if !availableCollaborationSessionState(binding.State) {
		return nil, fmt.Errorf("%w: session %q is unavailable", ErrConflict, sessionRef)
	}
	if err := requireRoomPrincipalAccessTx(ctx, tx, binding.RoomID, actor.ID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, collaborationMessageSelect+`
  WHERE delivery.to_agent_id = ? AND delivery.pulled_at IS NULL AND delivery.invalidated_at IS NULL
   AND (delivery.target_session_ref = ? OR (delivery.target_session_ref IS NULL AND delivery.room_id = ? AND (COALESCE(delivery.work_id, '') = ? AND ? OR ? AND delivery.kind IN ('candidate_ready', 'peer_result', 'work_run_terminal', 'verification_feedback', 'completion'))))
  ORDER BY delivery.created_at, delivery.rowid LIMIT ?`, actor.ID, sessionRef, binding.RoomID, binding.WorkID, binding.WorkID != "" || binding.Purpose == CollaborationSessionConversation || binding.Purpose == CollaborationSessionCoordination, binding.Purpose == CollaborationSessionConversation, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]CollaborationMessage, 0)
	for rows.Next() {
		message, err := scanCollaborationMessage(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		message.TargetSessionRef = sessionRef
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, message := range messages {
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET target_session_ref = ? WHERE id = ? AND pulled_at IS NULL AND invalidated_at IS NULL`, sessionRef, message.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return messages, nil
}

// AcknowledgeCollaboration is idempotent and session-scoped. Possessing an
// identity's credentials does not allow one bound session to consume a sibling
// session's messages through this API.
func (s *Service) AcknowledgeCollaboration(ctx context.Context, agentID, token, sessionRef string, messageIDs []string) error {
	actor, err := s.AuthenticatePrincipal(ctx, agentID, token)
	if err != nil {
		return err
	}
	sessionRef = strings.TrimSpace(sessionRef)
	if sessionRef == "" {
		return errors.New("collaboration session ref is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, sessionRef))
	if err != nil {
		return err
	}
	if binding.PrincipalID != actor.ID {
		return ErrUnauthorized
	}
	now := toMillis(s.now())
	for _, id := range messageIDs {
		message, err := scanCollaborationMessage(tx.QueryRowContext(ctx, collaborationMessageSelect+` WHERE delivery.id = ?`, strings.TrimSpace(id)))
		if err != nil {
			return err
		}
		if message.ToAgentID != actor.ID || message.TargetSessionRef != sessionRef {
			return ErrUnauthorized
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET pulled_at = COALESCE(pulled_at, ?), consumed_at = COALESCE(consumed_at, ?) WHERE id = ?`, now, now, message.ID); err != nil {
			return err
		}
		// Work assignments also have a room task notification. Consume both only
		// after the assignment has been persisted in its addressed session.
		if message.Kind == CollaborationAssignment && message.WorkID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE inbox_items SET pulled_at = COALESCE(pulled_at, ?) WHERE member_type = 'agent' AND member_id = ? AND message_id = ? AND kind = 'task'`, now, actor.ID, message.WorkID); err != nil {
				return err
			}
		}
		if message.SourceMessageID != "" && message.WorkID == "" {
			if _, err := tx.ExecContext(ctx, `UPDATE inbox_items SET pulled_at = COALESCE(pulled_at, ?) WHERE member_type = 'agent' AND member_id = ? AND room_id = ? AND message_id = ? AND kind IN ('mention', 'reply', 'thread_update')`, now, actor.ID, message.RoomID, message.SourceMessageID); err != nil {
				return err
			}
		}

	}
	if err := recomputeAgentWakeTx(ctx, tx, actor.ID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *AgentClient) ReceiveCollaboration(ctx context.Context, limit int) ([]CollaborationMessage, error) {
	if c == nil || c.service == nil || c.sessionRef == "" {
		return nil, ErrUnauthorized
	}
	return c.service.ReceiveCollaboration(ctx, c.agentID, c.token, c.sessionRef, limit)
}

func (c *AgentClient) AcknowledgeCollaboration(ctx context.Context, messageIDs []string) error {
	if c == nil || c.service == nil || c.sessionRef == "" {
		return ErrUnauthorized
	}
	return c.service.AcknowledgeCollaboration(ctx, c.agentID, c.token, c.sessionRef, messageIDs)
}
