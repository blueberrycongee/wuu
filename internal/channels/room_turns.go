package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const roomMaxRounds = 3
const roomMaxTurns = 10

// One durable cursor serializes each room discussion across hosts and restarts.
// Members retain their existing identity conversations and execution leases.
func (s *Service) migrateRoomTurns() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS room_turns (
 room_id TEXT PRIMARY KEY REFERENCES rooms(id) ON DELETE CASCADE,
 source_message_id TEXT NOT NULL REFERENCES room_messages(id) ON DELETE CASCADE,
 round INTEGER NOT NULL DEFAULT 0, next_member INTEGER NOT NULL DEFAULT 0,
 members_json TEXT NOT NULL DEFAULT '[]', round_start_seq INTEGER NOT NULL,
 turns INTEGER NOT NULL DEFAULT 0, delivery_id TEXT NOT NULL DEFAULT '',
 session_ref TEXT NOT NULL DEFAULT '', turn_id TEXT NOT NULL DEFAULT '')`)
	return err
}

type roomTurn struct {
	roomID, sourceID               string
	round, next                    int
	members                        []string
	roundStart                     int64
	turns                          int
	deliveryID, sessionRef, turnID string
}

func loadRoomTurnTx(ctx context.Context, tx *sql.Tx, roomID string) (roomTurn, error) {
	var t roomTurn
	var members string
	err := tx.QueryRowContext(ctx, `SELECT room_id,source_message_id,round,next_member,members_json,round_start_seq,turns,delivery_id,session_ref,turn_id FROM room_turns WHERE room_id=?`, roomID).Scan(&t.roomID, &t.sourceID, &t.round, &t.next, &members, &t.roundStart, &t.turns, &t.deliveryID, &t.sessionRef, &t.turnID)
	if err == nil {
		err = json.Unmarshal([]byte(members), &t.members)
	}
	return t, err
}

func saveRoomTurnTx(ctx context.Context, tx *sql.Tx, t roomTurn) error {
	members, err := json.Marshal(t.members)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE room_turns SET round=?,next_member=?,members_json=?,round_start_seq=?,turns=?,delivery_id=?,session_ref=?,turn_id=? WHERE room_id=?`, t.round, t.next, string(members), t.roundStart, t.turns, t.deliveryID, t.sessionRef, t.turnID, t.roomID)
	return err
}

// New human input supersedes unstarted discussion turns, not a member's work.
func stopRoomTurnTx(ctx context.Context, tx *sql.Tx, roomID string, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=? WHERE id IN (SELECT delivery_id FROM room_turns WHERE room_id=?) AND consumed_at IS NULL`, now, roomID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM room_turns WHERE room_id=?`, roomID)
	return err
}

func beginRoomTurnTx(ctx context.Context, tx *sql.Tx, source Message, now int64) ([]string, error) {
	if err := stopRoomTurnTx(ctx, tx, source.RoomID, now); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO room_turns(room_id,source_message_id,round_start_seq) VALUES(?,?,?)`, source.RoomID, source.ID, source.Seq); err != nil {
		return nil, err
	}
	t := roomTurn{roomID: source.RoomID, sourceID: source.ID, roundStart: source.Seq}
	var err error
	t.members, err = roomRespondersTx(ctx, tx, source, 0)
	if err != nil {
		return nil, err
	}
	return advanceRoomTurnTx(ctx, tx, t, now)
}

func roomRespondersTx(ctx context.Context, tx *sql.Tx, source Message, round int) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT member_id FROM room_members WHERE room_id=? AND member_type='agent' ORDER BY joined_at,rowid`, source.RoomID)
	if err != nil {
		return nil, err
	}
	var members []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		members = append(members, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT mentions_json FROM room_messages WHERE room_id=? AND seq>=? ORDER BY seq`, source.RoomID, source.Seq)
	if err != nil {
		return nil, err
	}
	mentioned := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			rows.Close()
			return nil, err
		}
		for _, id := range ids {
			mentioned[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	selected := make([]string, 0, len(members))
	for _, id := range members {
		if mentioned[id] {
			selected = append(selected, id)
		}
	}
	if len(selected) == 0 {
		selected = members
	}
	if len(selected) > 0 {
		offset := round % len(selected)
		selected = append(selected[offset:], selected[:offset]...)
	}
	return selected, nil
}

func advanceRoomTurnTx(ctx context.Context, tx *sql.Tx, t roomTurn, now int64) ([]string, error) {
	source, err := loadMessageTx(ctx, tx, t.sourceID)
	if err != nil {
		return nil, err
	}
	for t.round < roomMaxRounds && t.turns < roomMaxTurns {
		if t.next >= len(t.members) {
			var seq int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM room_messages WHERE room_id=? AND author_type='agent' AND kind='text'`, t.roomID).Scan(&seq); err != nil {
				return nil, err
			}
			if seq <= t.roundStart {
				break
			}
			t.round++
			t.next = 0
			t.roundStart = seq
			if t.round >= roomMaxRounds {
				break
			}
			t.members, err = roomRespondersTx(ctx, tx, source, t.round)
			if err != nil {
				return nil, err
			}
		}
		if len(t.members) == 0 {
			break
		}
		member := t.members[t.next]
		t.next++
		if err := requireRoomPrincipalAccessTx(ctx, tx, t.roomID, member); errors.Is(err, ErrUnauthorized) {
			continue
		} else if err != nil {
			return nil, err
		}
		var stopped bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM named_agent_conversations identity JOIN collaboration_session_bindings binding ON binding.session_ref=identity.session_ref WHERE identity.agent_id=? AND binding.state IN ('cancelled','interrupted','missing'))`, member).Scan(&stopped); err != nil {
			return nil, err
		}
		if stopped {
			continue
		}
		delivery, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: t.roomID, FromType: MemberHuman, FromID: source.AuthorID, ToAgentID: member, SourceMessageID: source.ID, Body: source.Body, RequestID: fmt.Sprintf("room-turn:%s:%d:%s", source.ID, t.round, member), CreatedAt: fromMillis(now)})
		if err != nil {
			return nil, err
		}
		t.turns++
		t.deliveryID = delivery.ID
		t.sessionRef = ""
		t.turnID = ""
		if err := saveRoomTurnTx(ctx, tx, t); err != nil {
			return nil, err
		}
		if _, err := requestWakeTx(ctx, tx, member, now); err != nil {
			return nil, err
		}
		return []string{member}, nil
	}
	return nil, stopRoomTurnTx(ctx, tx, t.roomID, now)
}

func bindRoomTurnTx(ctx context.Context, tx *sql.Tx, binding CollaborationSessionBinding, turnID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE room_turns SET session_ref=?,turn_id=? WHERE room_id=? AND turn_id='' AND EXISTS(SELECT 1 FROM collaboration_messages delivery WHERE delivery.id=room_turns.delivery_id AND delivery.target_session_ref=? AND delivery.consumed_at IS NOT NULL AND delivery.invalidated_at IS NULL)`, binding.SessionRef, turnID, binding.RoomID, binding.SessionRef)
	return err
}

func finishRoomTurnTx(ctx context.Context, tx *sql.Tx, binding CollaborationSessionBinding, turnID string, now int64) ([]string, error) {
	t, err := loadRoomTurnTx(ctx, tx, binding.RoomID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.sessionRef != binding.SessionRef || t.turnID != turnID {
		return nil, nil
	}
	return advanceRoomTurnTx(ctx, tx, t, now)
}

// RoomTurnPrompt is assembled at admission, so a later speaker sees earlier
// replies even if it was queued behind work in another room.
func (s *Service) RoomTurnPrompt(ctx context.Context, deliveryID string) (string, error) {
	var roomID, sourceID, memberID string
	err := s.db.QueryRowContext(ctx, `SELECT turn.room_id,turn.source_message_id,delivery.to_agent_id FROM room_turns turn JOIN collaboration_messages delivery ON delivery.id=turn.delivery_id WHERE turn.delivery_id=?`, deliveryID).Scan(&roomID, &sourceID, &memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	messages, err := s.ListMessageWindow(ctx, RoomHistoryQuery{RoomID: roomID, Limit: 24})
	if err != nil {
		return "", err
	}
	var lines []string
	for _, message := range messages {
		lines = append(lines, fmt.Sprintf("%s %s: %s", message.AuthorType, message.AuthorID, message.Body))
	}
	return fmt.Sprintf("Room discussion, source message %s. You are %s. Read the recent room history below and act on the user's latest request. Earlier speakers may already have answered it. Add useful work or information; if you have nothing new to contribute, call yield_turn. Do not repeat an answer or acknowledge a pass.\n%s", sourceID, memberID, strings.Join(lines, "\n")), nil
}

func refreshRoomTurnMembersTx(ctx context.Context, tx *sql.Tx, roomID string, now int64) ([]string, error) {
	t, err := loadRoomTurnTx(ctx, tx, roomID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM collaboration_messages delivery JOIN room_members member ON member.room_id=delivery.room_id AND member.member_id=delivery.to_agent_id AND member.member_type='agent' WHERE delivery.id=?)`, t.deliveryID).Scan(&present); err != nil {
		return nil, err
	}
	if present {
		return nil, nil
	}
	return advanceRoomTurnTx(ctx, tx, t, now)
}
