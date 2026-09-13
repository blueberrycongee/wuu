package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Retirement keeps historical principals and transcripts, but fences every
// hidden execution and preserves unconsumed deliveries for visible members.
func (s *Service) retireRoomRuntimes(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := toMillis(s.now())
	for _, query := range []string{
		`UPDATE room_runtimes SET autostart = 0`,
		`UPDATE reminders SET state = 'cancelled' WHERE agent_id IN (SELECT id FROM room_runtimes) AND state = 'pending'`,
		`UPDATE agent_wake_state SET outstanding = 0, pending = 0 WHERE agent_id IN (SELECT id FROM room_runtimes)`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("retire room runtimes: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = 'cancelled', failure_reason = 'Room runtime retired', updated_at = ? WHERE principal_id IN (SELECT id FROM room_runtimes) AND state NOT IN ('cancelled', 'completed', 'failed')`, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, collaborationMessageSelect+` WHERE delivery.to_agent_id IN (SELECT id FROM room_runtimes) AND delivery.consumed_at IS NULL AND delivery.invalidated_at IS NULL ORDER BY delivery.created_at, delivery.id`)
	if err != nil {
		return err
	}
	var pending []CollaborationMessage
	for rows.Next() {
		message, err := scanCollaborationMessage(rows)
		if err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, message)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, old := range pending {
		var visibleMembers int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_type = 'agent'`, old.RoomID).Scan(&visibleMembers); err != nil {
			return err
		}
		if old.WorkID == "" && old.SourceMessageID != "" && visibleMembers > 0 {
			source, loadErr := loadMessageTx(ctx, tx, old.SourceMessageID)
			if loadErr == nil && source.Kind != MessageTask {
				mentions, resolveErr := resolveMentionsTx(ctx, tx, source.RoomID, source.Body)
				if resolveErr != nil {
					return resolveErr
				}
				var reply *Message
				if source.ReplyTo != "" {
					original, err := loadMessageTx(ctx, tx, source.ReplyTo)
					if err != nil {
						return err
					}
					reply = &original
				}
				if _, err := evaluateTriggersTx(ctx, tx, source, mentions, reply, now); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at = ? WHERE id = ?`, now, old.ID); err != nil {
					return err
				}
				continue
			}
		}
		recipientID, recipientSession, err := roomResultRecipientTx(ctx, tx, old.RoomID)
		if old.WorkID != "" {
			work, loadErr := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id = ?`, old.WorkID))
			if loadErr == nil {
				recipientID, recipientSession, err = workResultRecipientTx(ctx, tx, work, old.FromSessionRef)
			}
		}
		// A room with no visible agents retains its original pending delivery. A
		// later bootstrap can transfer it after a human adds a member.
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		next := old
		next.ID, next.ToAgentID, next.TargetSessionRef = "", recipientID, recipientSession
		next.TargetKind, next.TargetID, next.RequestID = CollaborationTargetNamedAgent, recipientID, "retired:"+old.ID
		if recipientSession != "" {
			next.TargetKind, next.TargetID = CollaborationTargetSession, recipientSession
		}
		if _, err := enqueueCollaborationTx(ctx, tx, next); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at = ? WHERE id = ?`, now, old.ID); err != nil {
			return err
		}
		if _, err := requestWakeTx(ctx, tx, recipientID, now); err != nil {
			return err
		}
	}
	var migrated bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_metadata WHERE key='room_round_robin_version')`).Scan(&migrated); err != nil {
		return err
	}
	if !migrated {
		if err := restoreMemberInboxDeliveriesTx(ctx, tx, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_metadata(key,value) VALUES('room_round_robin_version','1')`); err != nil {
			return err
		}
	}
	// Unexecuted coordinator rewrites are replaced by the original human
	// request only when that request still has no visible member response.
	rows, err = tx.QueryContext(ctx, collaborationMessageSelect+` WHERE delivery.from_id IN (SELECT id FROM room_runtimes) AND delivery.kind='control' AND delivery.consumed_at IS NULL AND delivery.invalidated_at IS NULL AND delivery.source_message_id IS NOT NULL`)
	if err != nil {
		return err
	}
	var rewrites []CollaborationMessage
	for rows.Next() {
		message, err := scanCollaborationMessage(rows)
		if err != nil {
			rows.Close()
			return err
		}
		rewrites = append(rewrites, message)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, rewrite := range rewrites {
		source, err := loadMessageTx(ctx, tx, rewrite.SourceMessageID)
		if err != nil {
			return err
		}
		if source.AuthorType != MemberHuman {
			continue
		}
		var answered bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM room_messages WHERE room_id=? AND seq>? AND author_type='agent' AND kind='text')`, source.RoomID, source.Seq).Scan(&answered); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=? WHERE id=?`, now, rewrite.ID); err != nil {
			return err
		}
		if !answered {
			mentions, err := resolveMentionsTx(ctx, tx, source.RoomID, source.Body)
			if err != nil {
				return err
			}
			if _, err := evaluateTriggersTx(ctx, tx, source, mentions, nil, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Old direct rooms could persist only an inbox signal. Rebuild wake-eligible
// envelopes once, without turning passive room history into new work.
func restoreMemberInboxDeliveriesTx(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `
 SELECT inbox.member_id, inbox.message_id, inbox.kind FROM inbox_items inbox
 JOIN room_messages message ON message.id = inbox.message_id
 JOIN room_members member ON member.room_id = inbox.room_id AND member.member_type = 'agent' AND member.member_id = inbox.member_id
 WHERE inbox.member_type = 'agent' AND inbox.pulled_at IS NULL AND inbox.kind IN ('mention', 'reply', 'thread_update')
 AND (message.author_type = 'human' OR inbox.kind IN ('mention', 'reply'))
 AND NOT EXISTS (SELECT 1 FROM room_messages response WHERE response.room_id=message.room_id AND response.seq>message.seq AND response.author_type='agent' AND response.kind='text')
 AND NOT EXISTS (SELECT 1 FROM collaboration_messages delivery WHERE delivery.to_agent_id = inbox.member_id AND delivery.source_message_id = inbox.message_id)
 AND NOT EXISTS (SELECT 1 FROM room_turns turn WHERE turn.source_message_id = inbox.message_id)
 ORDER BY inbox.created_at, inbox.id`)
	if err != nil {
		return err
	}
	type pending struct {
		agentID, messageID string
		kind               InboxKind
	}
	var items []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.agentID, &item.messageID, &item.kind); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		message, err := loadMessageTx(ctx, tx, item.messageID)
		if err != nil {
			return err
		}
		if message.AuthorType == MemberHuman && item.kind == InboxThreadUpdate {
			mentions, err := resolveMentionsTx(ctx, tx, message.RoomID, message.Body)
			if err != nil {
				return err
			}
			explicit := false
			for _, mention := range mentions {
				if mention.MemberType == MemberAgent {
					explicit = true
				}
			}
			if explicit {
				continue
			}
		}
		suppressed, err := isThreadLoopSuppressedTx(ctx, tx, message)
		if err != nil {
			return err
		}
		if suppressed {
			continue
		}
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: message.RoomID, FromType: message.AuthorType, FromID: message.AuthorID, ToAgentID: item.agentID, SourceMessageID: message.ID, Body: message.Body, CreatedAt: fromMillis(now)}); err != nil {
			return err
		}
		if _, err := requestWakeTx(ctx, tx, item.agentID, now); err != nil {
			return err
		}
	}
	return nil
}
