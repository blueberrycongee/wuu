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
)

type CollaborationSessionSettleParams struct {
	SessionRef    string
	State         CollaborationSessionState
	Result        string
	TurnID        string
	FailureReason string
}

// SettleCollaborationSession atomically records a turn's outcome and its
// parent notification. A repeated outcome never changes a newer turn's state.
// This is a trusted host API; agents submit results through their normal turn.
func (s *Service) SettleCollaborationSession(ctx context.Context, params CollaborationSessionSettleParams) (CollaborationSessionBinding, error) {
	params.SessionRef, params.TurnID = strings.TrimSpace(params.SessionRef), strings.TrimSpace(params.TurnID)
	params.Result, params.FailureReason = strings.TrimSpace(params.Result), strings.TrimSpace(params.FailureReason)
	terminal := CollaborationTerminalCompleted
	switch params.State {
	case CollaborationSessionCompleted, CollaborationSessionIdle:
	case CollaborationSessionFailed:
		terminal = CollaborationTerminalFailed
	case CollaborationSessionInterrupted:
		terminal = CollaborationTerminalInterrupted
	default:
		return CollaborationSessionBinding{}, errors.New("session settlement requires completed, idle, failed or interrupted state")
	}
	if params.SessionRef == "" || params.TurnID == "" {
		return CollaborationSessionBinding{}, errors.New("session settlement requires session and turn ids")
	}
	encoded, _ := json.Marshal(params)
	digest := sha256.Sum256(encoded)
	resultHash := hex.EncodeToString(digest[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	defer tx.Rollback()
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	var previousHash string
	err = tx.QueryRowContext(ctx, `SELECT result_hash FROM collaboration_session_settlements WHERE session_ref = ? AND turn_id = ?`, params.SessionRef, params.TurnID).Scan(&previousHash)
	if err == nil {
		if previousHash != resultHash {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: turn settlement was repeated with a different result", ErrConflict)
		}
		return binding, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CollaborationSessionBinding{}, err
	}
	if binding.TurnID != "" && binding.TurnID != params.TurnID {
		return CollaborationSessionBinding{}, fmt.Errorf("%w: settlement belongs to a different turn", ErrConflict)
	}
	if binding.State == CollaborationSessionCancelled || binding.State == CollaborationSessionInterrupted || binding.State == CollaborationSessionMissing || binding.State == CollaborationSessionQueued || binding.State == CollaborationSessionStarting && binding.TurnID == "" {
		return CollaborationSessionBinding{}, fmt.Errorf("%w: session %q cannot accept a late result while %s", ErrConflict, binding.SessionRef, binding.State)
	}
	if binding.WorkID != "" {
		if err := validateCollaborationSessionWriteTx(ctx, tx, binding.SessionRef, binding.PrincipalID, binding.RoomID, binding.WorkID, 0); err != nil {
			return CollaborationSessionBinding{}, err
		}
		if binding.RunID != "" {
			runState := WorkRunCompleted
			if params.State == CollaborationSessionFailed {
				runState = WorkRunFailed
			}
			if params.State == CollaborationSessionInterrupted {
				runState = WorkRunInterrupted
			}
			if _, err := tx.ExecContext(ctx, `UPDATE work_runs SET state = ?, outcome = ?, ended_at = ?, updated_at = ? WHERE id = ? AND state IN ('running', 'queued')`, runState, params.Result, toMillis(s.now()), toMillis(s.now()), binding.RunID); err != nil {
				return CollaborationSessionBinding{}, err
			}
			if err := refreshWorkCurrentRunRefTx(ctx, tx, binding.WorkID, s.now()); err != nil {
				return CollaborationSessionBinding{}, err
			}
		}
	}
	effectiveState := params.State
	if params.State == CollaborationSessionCompleted || params.State == CollaborationSessionIdle {
		var activeChildren int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM collaboration_session_bindings WHERE parent_session_ref = ? AND state IN ('starting', 'running', 'queued', 'waiting')`, binding.SessionRef).Scan(&activeChildren); err != nil {
			return CollaborationSessionBinding{}, err
		}
		if activeChildren > 0 {
			effectiveState = CollaborationSessionWaiting
		}
	}
	now := toMillis(s.now())
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = ?, run_id = NULL, turn_id = ?, failure_reason = ?, updated_at = ? WHERE session_ref = ?`, effectiveState, params.TurnID, params.FailureReason, now, binding.SessionRef); err != nil {
		return CollaborationSessionBinding{}, err
	}
	wakePrincipal := ""
	if binding.ParentSessionRef != "" && effectiveState != CollaborationSessionWaiting && effectiveState != CollaborationSessionIdle {
		parent, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, binding.ParentSessionRef))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return CollaborationSessionBinding{}, err
		}
		if err == nil && parent.RoomID == binding.RoomID && parent.SessionRef != binding.SessionRef {
			// Leaving a room removes access to its private deliveries. Still settle
			// the child so an unreachable parent cannot occupy execution capacity.
			childAccess := requireRoomPrincipalAccessTx(ctx, tx, binding.RoomID, binding.PrincipalID)
			parentAccess := requireRoomPrincipalAccessTx(ctx, tx, binding.RoomID, parent.PrincipalID)
			if childAccess != nil && !errors.Is(childAccess, ErrUnauthorized) {
				return CollaborationSessionBinding{}, childAccess
			}
			if parentAccess != nil && !errors.Is(parentAccess, ErrUnauthorized) {
				return CollaborationSessionBinding{}, parentAccess
			}
			if childAccess == nil && parentAccess == nil {
				result := params.Result
				if result == "" {
					result = params.FailureReason
				}
				if result == "" {
					result = fmt.Sprintf("Session %s %s.", binding.SessionRef, terminal)
				}
				runes := []rune(result)
				if len(runes) > MaxMessageRunes {
					result = string(runes[:MaxMessageRunes]) + "\n\n[Read the source session for the full result.]"
				}
				_, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: binding.RoomID, FromType: MemberAgent, FromID: binding.PrincipalID, FromSessionRef: binding.SessionRef, ToAgentID: parent.PrincipalID, TargetSessionRef: parent.SessionRef, WorkID: parent.WorkID, Kind: CollaborationCompletion, Body: result, TargetKind: CollaborationTargetSession, TargetID: parent.SessionRef, Visibility: CollaborationVisibilityPrivate, CorrelationID: params.TurnID, RequestID: "session-result:" + params.TurnID, TerminalState: terminal, CreatedAt: fromMillis(now)})
				if err != nil {
					return CollaborationSessionBinding{}, err
				}
				// A cancelled or paused parent keeps its durable notification until
				// explicitly resumed, without being woken by a late child completion.
				if acceptsCollaborationSessionDelivery(parent.State) {
					_, err := requestWakeTx(ctx, tx, parent.PrincipalID, now)
					if err != nil {
						return CollaborationSessionBinding{}, err
					}
					wakePrincipal = parent.PrincipalID
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_session_settlements(session_ref, turn_id, result_hash, created_at) VALUES (?, ?, ?, ?)`, binding.SessionRef, params.TurnID, resultHash, now); err != nil {
		return CollaborationSessionBinding{}, err
	}
	updated, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, binding.SessionRef))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if wakePrincipal != "" && s.wake != nil {
		s.wake.Deliver(wakePrincipal)
	}
	return updated, nil
}
