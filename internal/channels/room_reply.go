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
		Body: body, Mentions: mentionIDs, CreatedAt: fromMillis(now),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_messages (id, room_id, seq, author_type, author_id, kind, body, mentions_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, message.ID, message.RoomID, message.Seq,
		message.AuthorType, message.AuthorID, message.Kind, message.Body, mentionsJSON, now); err != nil {
		return nil, fmt.Errorf("insert conversation reply: %w", err)
	}
	return evaluateTriggersTx(ctx, tx, message, mentions, nil, now)
}
