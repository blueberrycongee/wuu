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
	// PublicReply projects a room conversation's final answer into its public
	// timeline. Omitting it preserves settlement fingerprints from older hosts.
	PublicReply      string       `json:",omitempty"`
	Provider         string       `json:",omitempty"`
	Model            string       `json:",omitempty"`
	InputTokens      int64        `json:",omitempty"`
	OutputTokens     int64        `json:",omitempty"`
	RunState         WorkRunState `json:",omitempty"`
	AdmissionFailure bool         `json:",omitempty"`
}

// SettleCollaborationSession atomically records a turn's outcome, parent
// notification and optional public reply. A replay never changes a newer turn.
// This is a trusted host API; agents submit results through their normal turn.
func (s *Service) SettleCollaborationSession(ctx context.Context, params CollaborationSessionSettleParams) (CollaborationSessionBinding, error) {
	params.SessionRef, params.TurnID = strings.TrimSpace(params.SessionRef), strings.TrimSpace(params.TurnID)
	params.Result, params.FailureReason = strings.TrimSpace(params.Result), strings.TrimSpace(params.FailureReason)
	if strings.TrimSpace(params.PublicReply) == "" {
		params.PublicReply = ""
	}
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
	if params.InputTokens < 0 || params.OutputTokens < 0 {
		return CollaborationSessionBinding{}, errors.New("turn usage cannot be negative")
	}
	if params.AdmissionFailure && params.State != CollaborationSessionFailed {
		return CollaborationSessionBinding{}, errors.New("admission failure requires a failed settlement")
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
	scope, err := recordCollaborationTurnScopeTx(ctx, tx, binding, params.TurnID)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding.TurnID = params.TurnID
	if err := validateCollaborationTurnScopeTx(ctx, tx, binding, binding.WorkID, true); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_turn_scopes SET input_tokens=?,output_tokens=? WHERE session_ref=? AND turn_id=?`, params.InputTokens, params.OutputTokens, binding.SessionRef, params.TurnID); err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding.RoomID, binding.WorkID = scope.RoomID, scope.WorkID
	if binding.WorkID != "" && !binding.Primary {
		if err := validateCollaborationSessionWriteTx(ctx, tx, binding.SessionRef, binding.PrincipalID, binding.RoomID, binding.WorkID, 0); err != nil && !binding.Primary {
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
		waiting, err := collaborationSessionWaitingTx(ctx, tx, binding.SessionRef)
		if err != nil {
			return CollaborationSessionBinding{}, err
		}
		if waiting {
			effectiveState = CollaborationSessionWaiting
		}
	}
	now := toMillis(s.now())
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = ?, work_id = CASE WHEN ? THEN NULL ELSE work_id END, run_id = NULL, turn_id = ?, failure_reason = ?, updated_at = ? WHERE session_ref = ?`, effectiveState, binding.Primary, params.TurnID, params.FailureReason, now, binding.SessionRef); err != nil {
		return CollaborationSessionBinding{}, err
	}
	wakePrincipals := make([]string, 0)
	if binding.Primary && scope.WorkID != "" {
		ids, err := s.settleIdentityWorkTx(ctx, tx, binding, scope, params, effectiveState)
		if err != nil {
			return CollaborationSessionBinding{}, err
		}
		wakePrincipals = appendUniqueStrings(wakePrincipals, ids...)
	}
	if params.PublicReply != "" {
		ids, err := insertConversationReplyTx(ctx, tx, binding, params.TurnID, params.PublicReply, now)
		if err != nil {
			return CollaborationSessionBinding{}, err
		}
		wakePrincipals = appendUniqueStrings(wakePrincipals, ids...)
	}
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
				_, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: binding.RoomID, FromType: MemberAgent, FromID: binding.PrincipalID, FromSessionRef: binding.SessionRef, ToAgentID: parent.PrincipalID, TargetSessionRef: parent.SessionRef, WorkID: parent.WorkID, Kind: CollaborationCompletion, Body: result, Visibility: CollaborationVisibilityPrivate, CorrelationID: params.TurnID, RequestID: "session-result:" + params.TurnID, TerminalState: terminal, CreatedAt: fromMillis(now)})
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
					wakePrincipals = append(wakePrincipals, parent.PrincipalID)
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_results(session_ref,turn_id,body,state,created_at) VALUES(?,?,?,?,?) ON CONFLICT(session_ref,turn_id) DO UPDATE SET body=excluded.body,state=excluded.state`, binding.SessionRef, params.TurnID, params.Result, params.State, now); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_session_settlements(session_ref, turn_id, result_hash, created_at) VALUES (?, ?, ?, ?)`, binding.SessionRef, params.TurnID, resultHash, now); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if params.AdmissionFailure && binding.Primary && scope.WorkID != "" {
		// The failed attempt is now durable. Retire its pending assignment so
		// a budget or provider failure cannot block unrelated queued rooms.
		if _, err := tx.ExecContext(ctx, `UPDATE collaboration_messages SET pulled_at=COALESCE(pulled_at,?),consumed_at=COALESCE(consumed_at,?)
			WHERE to_agent_id=? AND room_id=? AND work_id=? AND target_session_ref=?`, now, now, binding.PrincipalID, scope.RoomID, scope.WorkID, binding.SessionRef); err != nil {
			return CollaborationSessionBinding{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbox_items SET pulled_at=COALESCE(pulled_at,?) WHERE member_type='agent' AND member_id=? AND message_id=? AND kind='task'`, now, binding.PrincipalID, scope.WorkID); err != nil {
			return CollaborationSessionBinding{}, err
		}
		if err := recomputeAgentWakeTx(ctx, tx, binding.PrincipalID, now); err != nil {
			return CollaborationSessionBinding{}, err
		}
	}
	updated, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, binding.SessionRef))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if s.wake != nil {
		for _, principalID := range wakePrincipals {
			s.wake.Deliver(principalID)
		}
	}
	return updated, nil
}

// Dependencies remain durable while a yielded turn releases execution capacity.
func collaborationSessionWaitingTx(ctx context.Context, tx *sql.Tx, sessionRef string) (bool, error) {
	var waiting bool
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM collaboration_session_bindings WHERE parent_session_ref = ? AND state IN ('starting','running','queued','waiting'))
		OR EXISTS(SELECT 1 FROM collaboration_followups WHERE session_ref = ? AND state = 'active')`, sessionRef, sessionRef).Scan(&waiting)
	return waiting, err
}
