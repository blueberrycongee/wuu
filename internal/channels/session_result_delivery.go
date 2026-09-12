package channels

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type SessionResultEnqueueParams struct {
	ParentSessionRef string
	ParentTurnID     string
	SourceSessionRef string
	RequestID        string
	Body             string
}

type SessionResultEnqueueResult struct {
	Message   *CollaborationMessage
	Discarded bool
	Reason    string
}

// EnqueueSessionResult returns a child result to the original parent's scope.
// The host must first verify the child's ownership and parent relationship.
// Stopped parents retain valid results without being automatically resumed.
func (s *Service) EnqueueSessionResult(ctx context.Context, params SessionResultEnqueueParams) (SessionResultEnqueueResult, error) {
	params.ParentSessionRef = strings.TrimSpace(params.ParentSessionRef)
	params.ParentTurnID = strings.TrimSpace(params.ParentTurnID)
	params.SourceSessionRef = strings.TrimSpace(params.SourceSessionRef)
	params.RequestID, params.Body = strings.TrimSpace(params.RequestID), strings.TrimSpace(params.Body)
	if params.ParentSessionRef == "" || params.ParentTurnID == "" || params.SourceSessionRef == "" || params.RequestID == "" || params.Body == "" {
		return SessionResultEnqueueResult{}, errors.New("child result requires parent session and turn, source session, request id and body")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionResultEnqueueResult{}, err
	}
	defer tx.Rollback()
	discard := func(reason string) (SessionResultEnqueueResult, error) {
		return SessionResultEnqueueResult{Discarded: true, Reason: reason}, nil
	}
	parent, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref=?`, params.ParentSessionRef))
	if errors.Is(err, ErrNotFound) {
		return discard("parent session was removed")
	}
	if err != nil {
		return SessionResultEnqueueResult{}, err
	}
	if !parent.Primary || parent.NamedAgentID == "" {
		return SessionResultEnqueueResult{}, ErrUnauthorized
	}
	var scope CollaborationTurnScope
	err = tx.QueryRowContext(ctx, `SELECT room_id,work_id,run_id,goal_revision,work_owner_id FROM collaboration_turn_scopes WHERE session_ref=? AND turn_id=?`, parent.SessionRef, params.ParentTurnID).
		Scan(&scope.RoomID, &scope.WorkID, &scope.RunID, &scope.GoalRevision, &scope.OwnerNamedAgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return discard("parent turn scope is unavailable")
	}
	if err != nil {
		return SessionResultEnqueueResult{}, err
	}
	if err := requireRoomPrincipalAccessTx(ctx, tx, scope.RoomID, parent.PrincipalID); errors.Is(err, ErrUnauthorized) {
		return discard("parent is no longer a room member")
	} else if err != nil {
		return SessionResultEnqueueResult{}, err
	}
	if scope.WorkID != "" {
		work, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id=?`, scope.WorkID))
		if errors.Is(err, ErrNotFound) {
			return discard("task was removed")
		}
		if err != nil {
			return SessionResultEnqueueResult{}, err
		}
		if work.GoalRevision != scope.GoalRevision || terminalWorkState(work.State) {
			return discard("task was revised or ended")
		}
		if scope.OwnerNamedAgentID == "" || scope.OwnerNamedAgentID != work.OwnerNamedAgentID {
			return discard("task ownership changed or is unknown")
		}
		if scope.RunID == "" {
			if work.OwnerNamedAgentID != parent.PrincipalID {
				return discard("task ownership changed")
			}
		} else {
			var valid bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_runs WHERE id=? AND work_id=? AND named_agent_id=? AND goal_revision=? AND state IN ('queued','running','completed'))`, scope.RunID, work.ID, parent.PrincipalID, scope.GoalRevision).Scan(&valid); err != nil {
				return SessionResultEnqueueResult{}, err
			}
			if !valid {
				return discard("parent execution was revoked")
			}
		}
	}
	send := CollaborationSendParams{
		AgentID: parent.PrincipalID, RoomID: scope.RoomID, WorkID: scope.WorkID, CorrelationID: params.ParentTurnID,
		FromSessionRef: params.SourceSessionRef, ToAgentID: parent.PrincipalID, TargetSessionRef: parent.SessionRef,
		TargetKind: CollaborationTargetSession, TargetID: parent.SessionRef, Kind: CollaborationPeerResult,
		Visibility: CollaborationVisibilityPrivate, RequestID: params.RequestID, Body: params.Body,
	}
	hash := collaborationRequestHash(send)
	if message, found, err := findCollaborationRequestTx(ctx, tx, send, hash); found || err != nil {
		if err != nil {
			return SessionResultEnqueueResult{}, err
		}
		return SessionResultEnqueueResult{Message: &message}, nil
	}
	now := s.now()
	message, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
		RoomID: scope.RoomID, WorkID: scope.WorkID, GoalRevision: scope.GoalRevision,
		FromType: MemberAgent, FromID: parent.PrincipalID, FromSessionRef: params.SourceSessionRef,
		ToAgentID: parent.PrincipalID, TargetSessionRef: parent.SessionRef,
		TargetKind: CollaborationTargetSession, TargetID: parent.SessionRef, Kind: CollaborationPeerResult,
		Visibility: CollaborationVisibilityPrivate, Body: params.Body, RequestID: params.RequestID,
		CorrelationID: params.ParentTurnID, TerminalState: CollaborationTerminalCompleted, CreatedAt: now,
	})
	if err != nil {
		return SessionResultEnqueueResult{}, err
	}
	if err := recordCollaborationRequestTx(ctx, tx, send, hash, message.ID); err != nil {
		return SessionResultEnqueueResult{}, err
	}
	wake := acceptsCollaborationSessionDelivery(parent.State)
	if wake {
		if _, err := requestWakeTx(ctx, tx, parent.PrincipalID, toMillis(now)); err != nil {
			return SessionResultEnqueueResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SessionResultEnqueueResult{}, err
	}
	if wake && s.wake != nil {
		s.wake.Deliver(parent.PrincipalID)
	}
	return SessionResultEnqueueResult{Message: &message}, nil
}
