package channels

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// EnqueueSessionInput is the host-side delivery operation used by a
// SessionController after Service has authorized the caller. It preserves the
// actual sender and does not turn a session input into an identity-wide wake.
func (s *Service) EnqueueSessionInput(ctx context.Context, params CollaborationSessionSendParams) (CollaborationMessage, error) {
	params.SessionRef, params.Body = strings.TrimSpace(params.SessionRef), strings.TrimSpace(params.Body)
	params.ActorID, params.SourceSessionRef = strings.TrimSpace(params.ActorID), strings.TrimSpace(params.SourceSessionRef)
	params.RequestID = strings.TrimSpace(params.RequestID)
	if params.SessionRef == "" || params.Body == "" {
		return CollaborationMessage{}, errors.New("session and input body are required")
	}
	if utf8.RuneCountInString(params.Body) > MaxMessageRunes {
		return CollaborationMessage{}, fmt.Errorf("session input exceeds %d characters", MaxMessageRunes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaborationMessage{}, err
	}
	defer tx.Rollback()
	target, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
	if err != nil {
		return CollaborationMessage{}, err
	}
	senderID, senderType := params.ActorID, MemberAgent
	if senderID == "" {
		if params.SourceSessionRef != "" {
			return CollaborationMessage{}, ErrUnauthorized
		}
		senderType = MemberHuman
		if err := tx.QueryRowContext(ctx, `SELECT created_by FROM rooms WHERE id = ?`, target.RoomID).Scan(&senderID); err != nil {
			return CollaborationMessage{}, err
		}
	} else {
		if err := requireRoomPrincipalAccessTx(ctx, tx, target.RoomID, senderID); err != nil {
			return CollaborationMessage{}, err
		}
	}
	send := CollaborationSendParams{AgentID: senderID, RoomID: target.RoomID, FromSessionRef: params.SourceSessionRef, ToAgentID: target.PrincipalID, TargetSessionRef: target.SessionRef, Body: params.Body, Kind: CollaborationControl, TargetKind: CollaborationTargetSession, TargetID: target.SessionRef, Visibility: CollaborationVisibilityPrivate, RequestID: params.RequestID}
	requestHash := collaborationRequestHash(send)
	if message, found, err := findCollaborationRequestTx(ctx, tx, send, requestHash); found || err != nil {
		return message, err
	}
	if !acceptsCollaborationSessionDelivery(target.State) {
		return CollaborationMessage{}, fmt.Errorf("%w: target session is %s", ErrConflict, target.State)
	}
	if err := requireRoomPrincipalAccessTx(ctx, tx, target.RoomID, target.PrincipalID); err != nil {
		return CollaborationMessage{}, err
	}
	if params.SourceSessionRef != "" {
		if err := validateCollaborationSessionWriteTx(ctx, tx, params.SourceSessionRef, params.ActorID, target.RoomID, "", 0); err != nil {
			return CollaborationMessage{}, err
		}
	}
	now := fromMillis(toMillis(s.now()))
	message, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: target.RoomID, FromType: senderType, FromID: senderID, FromSessionRef: params.SourceSessionRef, ToAgentID: target.PrincipalID, TargetSessionRef: target.SessionRef, WorkID: target.WorkID, Kind: CollaborationControl, Body: params.Body, TargetKind: CollaborationTargetSession, TargetID: target.SessionRef, Visibility: CollaborationVisibilityPrivate, RequestID: params.RequestID, CreatedAt: now})
	if err != nil {
		return CollaborationMessage{}, err
	}
	if err := recordCollaborationRequestTx(ctx, tx, send, requestHash, message.ID); err != nil {
		return CollaborationMessage{}, err
	}
	_, err = requestWakeTx(ctx, tx, target.PrincipalID, toMillis(now))
	if err != nil {
		return CollaborationMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollaborationMessage{}, err
	}
	if s.wake != nil {
		s.wake.Deliver(target.PrincipalID)
	}
	return message, nil
}

func collaborationRequestHash(params CollaborationSendParams) string {
	params.Token = ""
	encoded, _ := json.Marshal(params)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func findCollaborationRequestTx(ctx context.Context, tx *sql.Tx, params CollaborationSendParams, requestHash string) (CollaborationMessage, bool, error) {
	if params.RequestID == "" {
		return CollaborationMessage{}, false, nil
	}
	var messageID, storedHash string
	err := tx.QueryRowContext(ctx, `SELECT message_id, request_hash FROM collaboration_send_requests WHERE room_id = ? AND from_id = ? AND from_session_ref = ? AND request_id = ?`, params.RoomID, params.AgentID, params.FromSessionRef, params.RequestID).Scan(&messageID, &storedHash)
	if err == nil {
		if storedHash != requestHash {
			return CollaborationMessage{}, false, fmt.Errorf("%w: collaboration request id was reused for different content", ErrConflict)
		}
		message, err := scanCollaborationMessage(tx.QueryRowContext(ctx, collaborationMessageSelect+` WHERE delivery.id = ?`, messageID))
		return message, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CollaborationMessage{}, false, err
	}
	// Deliveries created before request fingerprints were introduced remain
	// retryable, scoped to their originating session.
	message, err := scanCollaborationMessage(tx.QueryRowContext(ctx, collaborationMessageSelect+` WHERE delivery.room_id = ? AND delivery.from_id = ? AND COALESCE(delivery.from_session_ref, '') = ? AND delivery.request_id = ? ORDER BY delivery.created_at LIMIT 1`, params.RoomID, params.AgentID, params.FromSessionRef, params.RequestID))
	if errors.Is(err, ErrNotFound) {
		return CollaborationMessage{}, false, nil
	}
	if err != nil {
		return CollaborationMessage{}, false, err
	}
	if message.Body != params.Body || message.Kind != params.Kind || (params.TargetSessionRef != "" && message.TargetSessionRef != params.TargetSessionRef) || (params.ToAgentID != "" && params.ToAgentID != message.ToAgentID) {
		return CollaborationMessage{}, false, fmt.Errorf("%w: collaboration request id was reused for different content", ErrConflict)
	}
	return message, true, nil
}

func recordCollaborationRequestTx(ctx context.Context, tx *sql.Tx, params CollaborationSendParams, requestHash, messageID string) error {
	if params.RequestID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO collaboration_send_requests(room_id, from_id, from_session_ref, request_id, request_hash, message_id) VALUES (?, ?, ?, ?, ?, ?)`, params.RoomID, params.AgentID, params.FromSessionRef, params.RequestID, requestHash, messageID)
	return err
}
