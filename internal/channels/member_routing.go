package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Results return to the task's initiating session while it still has room
// access. If it is unavailable, the visible lead or owner retains the result.
func workResultRecipientTx(ctx context.Context, tx *sql.Tx, work Work, sourceSession string) (string, string, error) {
	var coordinator string
	if err := tx.QueryRowContext(ctx, `SELECT runtime.id FROM room_runtimes runtime JOIN collaboration_messages assignment ON assignment.from_id = runtime.id AND assignment.work_id = ? AND assignment.kind = 'assignment' WHERE runtime.room_id = ? AND runtime.autostart = 1 LIMIT 1`, work.ID, work.RoomID).Scan(&coordinator); err == nil {
		return coordinator, "", nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	var principalID, sessionRef string
	err := tx.QueryRowContext(ctx, `
 SELECT binding.principal_id, binding.session_ref FROM collaboration_messages assignment
 JOIN collaboration_session_bindings binding ON binding.session_ref = assignment.from_session_ref
 JOIN room_members member ON member.room_id = binding.room_id AND member.member_type = 'agent' AND member.member_id = binding.principal_id
 WHERE assignment.work_id = ? AND assignment.kind = 'assignment' AND binding.room_id = ?
 AND binding.principal_id IN (?, ?) AND binding.session_ref != ? AND binding.state NOT IN ('cancelled', 'interrupted', 'missing', 'failed')
 ORDER BY assignment.created_at, assignment.id LIMIT 1`, work.ID, work.RoomID, work.OwnerNamedAgentID, work.LeadNamedAgentID, sourceSession).Scan(&principalID, &sessionRef)
	if err == nil {
		return principalID, sessionRef, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	for _, id := range []string{work.LeadNamedAgentID, work.OwnerNamedAgentID} {
		if id != "" && requireRoomPrincipalAccessTx(ctx, tx, work.RoomID, id) == nil {
			return id, "", nil
		}
	}
	return roomResultRecipientTx(ctx, tx, work.RoomID)
}

func roomResultRecipientTx(ctx context.Context, tx *sql.Tx, roomID string) (string, string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT member_id FROM room_members WHERE room_id = ? AND member_type = 'agent' ORDER BY joined_at, member_id LIMIT 1`, roomID).Scan(&id)
	if err != nil {
		return "", "", fmt.Errorf("resolve visible room recipient: %w", err)
	}
	return id, "", nil
}
