package channels

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
)

// ConversationReplyID is the stable public message address for one room
// conversation turn, including while the host is displaying its live answer.
func ConversationReplyID(sessionRef, turnID string) string {
	digest := sha256.Sum256([]byte(sessionRef + "\x00" + turnID))
	return fmt.Sprintf("msg-reply-%x", digest[:16])
}

// A reply is a projection of an already generated answer. The explicit send
// tool's length and draft-basis limits do not apply: splitting or truncating
// here would corrupt Markdown and discard part of the user's answer.
func insertConversationReplyTx(ctx context.Context, tx *sql.Tx, binding CollaborationSessionBinding, turnID, body string, now int64) ([]string, error) {
	if binding.Purpose != CollaborationSessionConversation || binding.ParentSessionRef != "" || binding.WorkID != "" ||
		binding.RoomID == "" || binding.NamedAgentID == "" || binding.NamedAgentID != binding.PrincipalID {
		return nil, fmt.Errorf("%w: public replies require a named room conversation", ErrUnauthorized)
	}
	var canPublish bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM room_members member
			JOIN collaboration_principals principal ON principal.id = member.member_id AND principal.kind = 'named_agent'
			JOIN named_agents agent ON agent.id = principal.id AND agent.kind = 'named'
			WHERE member.room_id = ? AND member.member_type = 'agent' AND member.member_id = ?
		)`, binding.RoomID, binding.PrincipalID).Scan(&canPublish); err != nil {
		return nil, fmt.Errorf("check conversation reply membership: %w", err)
	}
	if !canPublish {
		// Membership may change during inference. Settle the finished turn and
		// release its capacity without publishing through a departed identity.
		return nil, nil
	}
	mentions, err := resolveMentionsTx(ctx, tx, binding.RoomID, body)
	if err != nil {
		return nil, err
	}
	mentionIDs := make([]string, len(mentions))
	for i, mention := range mentions {
		mentionIDs[i] = mention.MemberID
	}
	mentionsJSON, err := encodeMentions(mentionIDs)
	if err != nil {
		return nil, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM room_messages WHERE room_id = ?`, binding.RoomID).Scan(&seq); err != nil {
		return nil, fmt.Errorf("allocate conversation reply sequence: %w", err)
	}
	message := Message{
		ID: ConversationReplyID(binding.SessionRef, turnID), RoomID: binding.RoomID, Seq: seq,
		AuthorType: MemberAgent, AuthorID: binding.NamedAgentID, Kind: MessageText,
		SourceSessionRef: binding.SessionRef, SourceTurnID: turnID,
		Body: body, Mentions: mentionIDs, CreatedAt: fromMillis(now),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_messages (id, room_id, seq, author_type, author_id, kind, body, mentions_json, created_at, source_session_ref, source_turn_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, message.ID, message.RoomID, message.Seq,
		message.AuthorType, message.AuthorID, message.Kind, message.Body, mentionsJSON, now, message.SourceSessionRef, message.SourceTurnID); err != nil {
		return nil, fmt.Errorf("insert conversation reply: %w", err)
	}
	return evaluateTriggersTx(ctx, tx, message, mentions, nil, now)
}

// Old automatic replies have deterministic addresses. Restore their execution
// source from durable settlements without guessing from the agent's latest turn.
func (s *Service) backfillConversationReplySources() error {
	var needed bool
	if err := s.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM room_messages WHERE id LIKE 'msg-reply-%' AND source_session_ref IS NULL)`).Scan(&needed); err != nil || !needed {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT session_ref, turn_id FROM collaboration_session_settlements`)
	if err != nil {
		return err
	}
	type source struct{ session, turn string }
	var sources []source
	for rows.Next() {
		var item source
		if err := rows.Scan(&item.session, &item.turn); err != nil {
			rows.Close()
			return err
		}
		sources = append(sources, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range sources {
		if _, err := tx.Exec(`UPDATE room_messages SET source_session_ref = ?, source_turn_id = ? WHERE id = ? AND source_session_ref IS NULL`, item.session, item.turn, ConversationReplyID(item.session, item.turn)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
