package channels

import (
	"context"
	"fmt"
)

// InboxSummary is a body-free snapshot of messages chat_check can pull in the
// current session scope. Sequence values distinguish new mail even when the
// unread count is unchanged between observations.
type InboxSummary struct {
	RoomID           string
	Unread           int
	DeliverySequence int64
	ItemSequence     int64
}

// PeekInbox neither claims messages nor advances cursors or wake state.
func (c *AgentClient) PeekInbox(ctx context.Context) (InboxSummary, error) {
	if c == nil || c.service == nil || c.sessionRef == "" {
		return InboxSummary{}, ErrUnauthorized
	}
	s := c.service
	actor, err := s.AuthenticatePrincipal(ctx, c.agentID, c.token)
	if err != nil {
		return InboxSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InboxSummary{}, err
	}
	defer tx.Rollback()
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, c.sessionRef))
	if err != nil {
		return InboxSummary{}, err
	}
	if binding.PrincipalID != actor.ID {
		return InboxSummary{}, ErrUnauthorized
	}
	if !availableCollaborationSessionState(binding.State) {
		return InboxSummary{}, fmt.Errorf("%w: session %q is unavailable", ErrConflict, binding.SessionRef)
	}
	if err := requireRoomPrincipalAccessTx(ctx, tx, binding.RoomID, actor.ID); err != nil {
		return InboxSummary{}, err
	}
	summary := InboxSummary{RoomID: binding.RoomID}
	scope, scopeArgs := sessionCollaborationInboxScope(binding)
	args := append([]any{actor.ID, binding.Primary, binding.RoomID, binding.SessionRef}, scopeArgs...)
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(delivery.rowid), 0)
		FROM collaboration_messages delivery
		WHERE delivery.to_agent_id = ? AND (NOT ? OR delivery.room_id = ?)
		AND delivery.pulled_at IS NULL AND delivery.invalidated_at IS NULL
		AND (delivery.target_session_ref = ? OR (delivery.target_session_ref IS NULL AND (`+scope+`)))`, args...).Scan(&summary.Unread, &summary.DeliverySequence)
	if err != nil {
		return InboxSummary{}, err
	}
	if publicScope, publicArgs := sessionPublicInboxScope(binding); publicScope != "" {
		// A routed public message also has a private delivery. Count it once,
		// including after that delivery was received through the wake input.
		var count int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(inbox.rowid), 0)
			FROM inbox_items inbox LEFT JOIN room_messages message ON message.id = inbox.message_id
			WHERE inbox.member_type = 'agent' AND inbox.member_id = ?`+publicScope+`
			AND NOT EXISTS (SELECT 1 FROM collaboration_messages delivery
				WHERE delivery.to_agent_id = inbox.member_id AND delivery.invalidated_at IS NULL
				AND (delivery.source_message_id = inbox.message_id
					OR delivery.kind = 'assignment' AND delivery.work_id = inbox.message_id))`,
			append([]any{actor.ID}, publicArgs...)...).Scan(&count, &summary.ItemSequence)
		if err != nil {
			return InboxSummary{}, err
		}
		summary.Unread += count
	}
	return summary, tx.Commit()
}
