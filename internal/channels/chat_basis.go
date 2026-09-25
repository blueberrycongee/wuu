package channels

import (
	"context"
	"database/sql"
	"errors"
)

// RememberChatScopes records only evidence delivered to this context. Reading an
// older page cannot erase knowledge, and a later unread message still holds a send.
func (c *AgentClient) RememberChatScopes(ctx context.Context, scopes []ScopeSequence) error {
	ref := c.sessionRef
	if ref == "" {
		ref = c.agentID
	}
	for _, scope := range scopes {
		if _, err := c.service.db.ExecContext(ctx, `INSERT INTO chat_send_bases(context_ref,room_id,thread_id,seq) VALUES(?,?,?,?) ON CONFLICT(context_ref,room_id,thread_id) DO UPDATE SET seq=MAX(seq,excluded.seq)`, ref, scope.RoomID, scope.ThreadID, scope.Seq); err != nil {
			return err
		}
	}
	return nil
}
func (c *AgentClient) ObservedChatBasis(ctx context.Context, room, thread string) (int64, error) {
	ref := c.sessionRef
	if ref == "" {
		ref = c.agentID
	}
	var seq int64
	err := c.service.db.QueryRowContext(ctx, `SELECT seq FROM chat_send_bases WHERE context_ref=? AND room_id=? AND thread_id=?`, ref, room, thread).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}
