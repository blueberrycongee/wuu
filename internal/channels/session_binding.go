package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const collaborationSessionSelect = `
	SELECT binding.session_ref, binding.principal_id, COALESCE(binding.named_agent_id, ''),
		COALESCE(binding.room_id, ''), COALESCE(binding.work_id, ''), COALESCE(binding.run_id, ''),
		binding.purpose, binding.state, binding.created_at, binding.updated_at,
		binding.title, binding.objective, binding.parent_session_ref, binding.provider, binding.model, binding.effort, binding.runtime_version, binding.failure_reason, binding.turn_id
	FROM collaboration_session_bindings binding`

func validCollaborationSessionPurpose(purpose CollaborationSessionPurpose) bool {
	switch purpose {
	case CollaborationSessionConversation, CollaborationSessionCoordination,
		CollaborationSessionWork, CollaborationSessionVerification:
		return true
	default:
		return false
	}
}

func validCollaborationSessionState(state CollaborationSessionState) bool {
	switch state {
	case CollaborationSessionWaiting, CollaborationSessionQueued, CollaborationSessionIdle, CollaborationSessionStarting, CollaborationSessionRunning,
		CollaborationSessionInterrupted, CollaborationSessionMissing, CollaborationSessionCompleted,
		CollaborationSessionCancelled, CollaborationSessionFailed:
		return true
	default:
		return false
	}
}

func scanCollaborationSession(row scanner) (CollaborationSessionBinding, error) {
	var binding CollaborationSessionBinding
	var createdAt, updatedAt int64
	if err := row.Scan(
		&binding.SessionRef, &binding.PrincipalID, &binding.NamedAgentID,
		&binding.RoomID, &binding.WorkID, &binding.RunID, &binding.Purpose,
		&binding.State, &createdAt, &updatedAt,
		&binding.Title, &binding.Objective, &binding.ParentSessionRef, &binding.Provider, &binding.Model, &binding.Effort, &binding.RuntimeVersion, &binding.FailureReason, &binding.TurnID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CollaborationSessionBinding{}, ErrNotFound
		}
		return CollaborationSessionBinding{}, err
	}
	binding.CreatedAt, binding.UpdatedAt = fromMillis(createdAt), fromMillis(updatedAt)
	return binding, nil
}

func (s *Service) BindCollaborationSession(ctx context.Context, params CollaborationSessionBindParams) (CollaborationSessionBinding, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	params.SessionRef = strings.TrimSpace(params.SessionRef)
	params.PrincipalID = strings.TrimSpace(params.PrincipalID)
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.WorkID = strings.TrimSpace(params.WorkID)
	params.RunID = strings.TrimSpace(params.RunID)
	params.Title = strings.TrimSpace(params.Title)
	params.Objective = strings.TrimSpace(params.Objective)
	params.ParentSessionRef = strings.TrimSpace(params.ParentSessionRef)
	params.Provider = strings.TrimSpace(params.Provider)
	params.Model = strings.TrimSpace(params.Model)
	params.Effort = strings.TrimSpace(params.Effort)
	params.RuntimeVersion = strings.TrimSpace(params.RuntimeVersion)
	params.FailureReason = strings.TrimSpace(params.FailureReason)
	if params.PrincipalID == "" {
		params.PrincipalID = actor.ID
	}
	if params.SessionRef == "" {
		return CollaborationSessionBinding{}, errors.New("collaboration session ref is required")
	}
	if params.Purpose == "" {
		params.Purpose = CollaborationSessionConversation
	}
	if params.State == "" {
		params.State = CollaborationSessionIdle
	}
	if !validCollaborationSessionPurpose(params.Purpose) || !validCollaborationSessionState(params.State) {
		return CollaborationSessionBinding{}, errors.New("collaboration session purpose and state are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	defer tx.Rollback()
	binding, err := bindCollaborationSessionTx(ctx, tx, actor, params, fromMillis(toMillis(s.now())))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollaborationSessionBinding{}, fmt.Errorf("commit collaboration session binding: %w", err)
	}
	return binding, nil
}

func bindCollaborationSessionTx(ctx context.Context, tx *sql.Tx, actor AgentRuntime, params CollaborationSessionBindParams, nowTime time.Time) (CollaborationSessionBinding, error) {
	now := toMillis(nowTime)
	var principalKind PrincipalKind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM collaboration_principals WHERE id = ?`, params.PrincipalID).Scan(&principalKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: collaboration principal %q", ErrNotFound, params.PrincipalID)
		}
		return CollaborationSessionBinding{}, fmt.Errorf("load collaboration session principal: %w", err)
	}
	if actor.ID != params.PrincipalID {
		if !actor.IsRoomRuntime() || params.RoomID == "" || actor.RoomID != params.RoomID {
			return CollaborationSessionBinding{}, ErrUnauthorized
		}
	}
	if params.RoomID != "" {
		if err := requireRoomPrincipalAccessTx(ctx, tx, params.RoomID, params.PrincipalID); err != nil {
			return CollaborationSessionBinding{}, err
		}
		if actor.IsRoomRuntime() && actor.RoomID != params.RoomID {
			return CollaborationSessionBinding{}, ErrUnauthorized
		}
	} else if params.WorkID != "" || params.RunID != "" || actor.ID != params.PrincipalID {
		return CollaborationSessionBinding{}, errors.New("scoped collaboration session requires a room")
	}
	if params.WorkID != "" {
		var workRoomID string
		if err := tx.QueryRowContext(ctx, `SELECT room_id FROM works WHERE id = ?`, params.WorkID).Scan(&workRoomID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CollaborationSessionBinding{}, fmt.Errorf("%w: work %q", ErrNotFound, params.WorkID)
			}
			return CollaborationSessionBinding{}, fmt.Errorf("load collaboration session work: %w", err)
		}
		if workRoomID != params.RoomID {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: collaboration session work belongs to another room", ErrConflict)
		}
	}
	if params.ParentSessionRef != "" {
		parent, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.ParentSessionRef))
		if err != nil {
			return CollaborationSessionBinding{}, fmt.Errorf("load parent collaboration session: %w", err)
		}
		if parent.SessionRef == params.SessionRef || parent.RoomID != params.RoomID || params.RoomID == "" {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: parent session must be another session in the same room", ErrConflict)
		}
		if err := requireRoomPrincipalAccessTx(ctx, tx, params.RoomID, parent.PrincipalID); err != nil {
			return CollaborationSessionBinding{}, err
		}
	}
	namedAgentID := ""
	if principalKind == PrincipalNamedAgent {
		namedAgentID = params.PrincipalID
	}
	if params.RunID != "" {
		var runWorkID, runSessionRef, runNamedAgentID string
		if err := tx.QueryRowContext(ctx, `
			SELECT work_id, COALESCE(session_ref, ''), COALESCE(named_agent_id, '')
			FROM work_runs WHERE id = ?`, params.RunID).Scan(&runWorkID, &runSessionRef, &runNamedAgentID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CollaborationSessionBinding{}, fmt.Errorf("%w: work run %q", ErrNotFound, params.RunID)
			}
			return CollaborationSessionBinding{}, fmt.Errorf("load collaboration session run: %w", err)
		}
		if runWorkID != params.WorkID || runSessionRef != params.SessionRef || runNamedAgentID != namedAgentID {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: collaboration session run binding does not match", ErrConflict)
		}
	}
	existing, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
	if err == nil {
		if existing.PrincipalID != params.PrincipalID {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: session %q already belongs to another principal", ErrConflict, params.SessionRef)
		}
		if (existing.State == CollaborationSessionRunning || existing.State == CollaborationSessionStarting) &&
			(params.State != existing.State || existing.RoomID != params.RoomID ||
				existing.WorkID != params.WorkID || existing.RunID != params.RunID || existing.Purpose != params.Purpose || (params.ParentSessionRef != "" && existing.ParentSessionRef != params.ParentSessionRef) ||
				(params.Model != "" && existing.Model != params.Model) || (params.Provider != "" && existing.Provider != params.Provider)) {
			return CollaborationSessionBinding{}, fmt.Errorf("%w: running session %q cannot change scope or state", ErrConflict, params.SessionRef)
		}
		if existing.RoomID != params.RoomID || existing.WorkID != params.WorkID {
			var pending int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM collaboration_messages
				WHERE target_session_ref = ? AND pulled_at IS NULL AND invalidated_at IS NULL`, params.SessionRef).Scan(&pending); err != nil {
				return CollaborationSessionBinding{}, fmt.Errorf("check collaboration session pending deliveries: %w", err)
			}
			if pending != 0 {
				return CollaborationSessionBinding{}, fmt.Errorf("%w: session %q still has pending deliveries", ErrConflict, params.SessionRef)
			}
		}
	} else if !errors.Is(err, ErrNotFound) {
		return CollaborationSessionBinding{}, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO collaboration_session_bindings(
			session_ref, principal_id, named_agent_id, room_id, work_id, run_id,
			purpose, state, created_at, updated_at, title, objective, parent_session_ref, provider, model, effort, runtime_version, failure_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_ref) DO UPDATE SET
			room_id = excluded.room_id,
			work_id = excluded.work_id,
			run_id = excluded.run_id,
			purpose = excluded.purpose,
			state = excluded.state,
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE title END,
			objective = CASE WHEN excluded.objective != '' THEN excluded.objective ELSE objective END,
			parent_session_ref = CASE WHEN excluded.parent_session_ref != '' THEN excluded.parent_session_ref ELSE parent_session_ref END,
			provider = CASE WHEN excluded.provider != '' THEN excluded.provider ELSE provider END,
			model = CASE WHEN excluded.model != '' THEN excluded.model ELSE model END,
			effort = CASE WHEN excluded.effort != '' THEN excluded.effort ELSE effort END,
			runtime_version = CASE WHEN excluded.runtime_version != '' THEN excluded.runtime_version ELSE runtime_version END,
			failure_reason = excluded.failure_reason,
			updated_at = excluded.updated_at`,
		params.SessionRef, params.PrincipalID, nullableString(namedAgentID),
		nullableString(params.RoomID), nullableString(params.WorkID), nullableString(params.RunID),
		params.Purpose, params.State, now, now, params.Title, params.Objective, params.ParentSessionRef, params.Provider, params.Model, params.Effort, params.RuntimeVersion, params.FailureReason)
	if err != nil {
		return CollaborationSessionBinding{}, fmt.Errorf("bind collaboration session: %w", err)
	}
	return scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.SessionRef))
}

func (s *Service) GetCollaborationSession(ctx context.Context, agentID, token, sessionRef string) (CollaborationSessionBinding, error) {
	actor, err := s.AuthenticatePrincipal(ctx, agentID, token)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding, err := scanCollaborationSession(s.db.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, strings.TrimSpace(sessionRef)))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if !canAccessCollaborationSession(actor, binding) {
		if binding.RoomID == "" {
			return CollaborationSessionBinding{}, ErrUnauthorized
		}
		if err := s.requireRoomPrincipalAccess(ctx, binding.RoomID, actor.ID); err != nil {
			return CollaborationSessionBinding{}, err
		}
		if err := s.requireRoomPrincipalAccess(ctx, binding.RoomID, binding.PrincipalID); err != nil {
			return CollaborationSessionBinding{}, err
		}
	}
	return binding, nil
}

func (s *Service) ListCollaborationSessions(ctx context.Context, params CollaborationSessionListParams) ([]CollaborationSessionBinding, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return nil, err
	}
	params.PrincipalID = strings.TrimSpace(params.PrincipalID)
	params.RoomID = strings.TrimSpace(params.RoomID)
	if actor.IsRoomRuntime() {
		if params.RoomID == "" {
			params.RoomID = actor.RoomID
		}
		if params.RoomID != actor.RoomID {
			return nil, ErrUnauthorized
		}
	} else if params.RoomID != "" {
		if err := s.requireRoomPrincipalAccess(ctx, params.RoomID, actor.ID); err != nil {
			return nil, err
		}
	} else {
		if params.PrincipalID == "" {
			params.PrincipalID = actor.ID
		}
		if params.PrincipalID != actor.ID {
			return nil, ErrUnauthorized
		}
	}
	query := collaborationSessionSelect + ` WHERE 1 = 1`
	if params.RoomID != "" && !actor.IsRoomRuntime() {
		query += ` AND EXISTS (SELECT 1 FROM room_members member WHERE member.room_id = binding.room_id AND member.member_type = 'agent' AND member.member_id = binding.principal_id)`
	}
	args := make([]any, 0, 2)
	if params.PrincipalID != "" {
		query += ` AND binding.principal_id = ?`
		args = append(args, params.PrincipalID)
	}
	if params.RoomID != "" {
		query += ` AND binding.room_id = ?`
		args = append(args, params.RoomID)
	}
	query += ` ORDER BY binding.updated_at DESC, binding.session_ref`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list collaboration sessions: %w", err)
	}
	defer rows.Close()
	bindings := make([]CollaborationSessionBinding, 0)
	for rows.Next() {
		binding, err := scanCollaborationSession(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Service) UpdateCollaborationSessionState(ctx context.Context, params CollaborationSessionStateParams) (CollaborationSessionBinding, error) {
	actor, err := s.AuthenticatePrincipal(ctx, params.AgentID, params.Token)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	params.SessionRef = strings.TrimSpace(params.SessionRef)
	if params.SessionRef == "" || !validCollaborationSessionState(params.State) {
		return CollaborationSessionBinding{}, errors.New("collaboration session ref and valid state are required")
	}
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
	if !canAccessCollaborationSession(actor, binding) {
		return CollaborationSessionBinding{}, ErrUnauthorized
	}
	if binding.RunID != "" && params.State != CollaborationSessionRunning {
		var runState WorkRunState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM work_runs WHERE id = ?`, binding.RunID).Scan(&runState); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return CollaborationSessionBinding{}, fmt.Errorf("load collaboration session run state: %w", err)
		}
		if runState == WorkRunQueued || runState == WorkRunRunning {
			if params.State == CollaborationSessionIdle {
				return CollaborationSessionBinding{}, fmt.Errorf("%w: active work session cannot become idle", ErrConflict)
			}
			now := fromMillis(toMillis(s.now()))
			if _, err := tx.ExecContext(ctx, `
				UPDATE work_runs
				SET state = 'interrupted', outcome = ?, ended_at = ?, updated_at = ?
				WHERE id = ? AND state IN ('queued', 'running')`,
				"session marked "+string(params.State), toMillis(now), toMillis(now), binding.RunID); err != nil {
				return CollaborationSessionBinding{}, fmt.Errorf("interrupt collaboration session run: %w", err)
			}
			if err := refreshWorkCurrentRunRefTx(ctx, tx, binding.WorkID, now); err != nil {
				return CollaborationSessionBinding{}, fmt.Errorf("settle collaboration session run handle: %w", err)
			}
		}
	}
	clearRun := params.State != CollaborationSessionRunning
	if _, err := tx.ExecContext(ctx, `
		UPDATE collaboration_session_bindings
		SET state = ?, run_id = CASE WHEN ? THEN NULL ELSE run_id END, failure_reason = ?, turn_id = CASE WHEN ? != '' THEN ? ELSE turn_id END, updated_at = ?
		WHERE session_ref = ?`, params.State, clearRun, strings.TrimSpace(params.FailureReason), strings.TrimSpace(params.TurnID), strings.TrimSpace(params.TurnID), toMillis(s.now()), binding.SessionRef); err != nil {
		return CollaborationSessionBinding{}, fmt.Errorf("update collaboration session state: %w", err)
	}
	updated, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, binding.SessionRef))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollaborationSessionBinding{}, err
	}
	return updated, nil
}

func canAccessCollaborationSession(actor AgentRuntime, binding CollaborationSessionBinding) bool {
	if actor.ID == binding.PrincipalID {
		return true
	}
	return actor.IsRoomRuntime() && binding.RoomID != "" && actor.RoomID == binding.RoomID
}

func validateCollaborationSessionRouteTx(ctx context.Context, tx *sql.Tx, sessionRef, principalID, roomID, workID string) error {
	sessionRef = strings.TrimSpace(sessionRef)
	if sessionRef == "" {
		return nil
	}
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, sessionRef))
	if err != nil {
		return fmt.Errorf("validate collaboration session route: %w", err)
	}
	if binding.PrincipalID != principalID {
		return fmt.Errorf("%w: session %q belongs to another principal", ErrUnauthorized, sessionRef)
	}
	if binding.RoomID != roomID || binding.WorkID != workID {
		return fmt.Errorf("%w: session %q does not match the delivery room/work scope", ErrConflict, sessionRef)
	}
	if !acceptsCollaborationSessionDelivery(binding.State) {
		return fmt.Errorf("%w: session %q is unavailable", ErrConflict, sessionRef)
	}
	return nil
}

func validateCollaborationSessionWriteTx(
	ctx context.Context,
	tx *sql.Tx,
	sessionRef, principalID, roomID, targetWorkID string,
	targetGoalRevision int,
) error {
	sessionRef = strings.TrimSpace(sessionRef)
	if sessionRef == "" {
		return nil
	}
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, sessionRef))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: collaboration session %q is unavailable", ErrConflict, sessionRef)
		}
		return fmt.Errorf("validate collaboration session write: %w", err)
	}
	if binding.PrincipalID != principalID {
		return fmt.Errorf("%w: session %q belongs to another principal", ErrUnauthorized, sessionRef)
	}
	if binding.RoomID != roomID {
		return fmt.Errorf("%w: session %q does not belong to room %q", ErrConflict, sessionRef, roomID)
	}
	if !availableCollaborationSessionState(binding.State) {
		return fmt.Errorf("%w: session %q is unavailable", ErrConflict, sessionRef)
	}
	if binding.WorkID == "" {
		if binding.RunID != "" {
			return fmt.Errorf("%w: session %q has an invalid run binding", ErrConflict, sessionRef)
		}
		if targetWorkID != "" {
			return fmt.Errorf("%w: session %q is not bound to work %q", ErrConflict, sessionRef, targetWorkID)
		}
		return nil
	}
	if targetWorkID != "" && binding.WorkID != targetWorkID {
		return fmt.Errorf("%w: session %q belongs to another work item", ErrConflict, sessionRef)
	}
	if binding.State != CollaborationSessionRunning || binding.RunID == "" {
		return fmt.Errorf("%w: work session %q has no active run", ErrConflict, sessionRef)
	}

	var runWorkID, runNamedAgentID, runSessionRef, workRoomID string
	var runState WorkRunState
	var workState WorkState
	var runGoalRevision, workGoalRevision int
	err = tx.QueryRowContext(ctx, `
		SELECT run.work_id, COALESCE(run.named_agent_id, ''), COALESCE(run.session_ref, ''),
			run.state, run.goal_revision, work.room_id, work.state, work.goal_revision
		FROM work_runs run
		JOIN works work ON work.id = run.work_id
		WHERE run.id = ?`, binding.RunID).Scan(
		&runWorkID, &runNamedAgentID, &runSessionRef, &runState, &runGoalRevision,
		&workRoomID, &workState, &workGoalRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: work session %q run is unavailable", ErrConflict, sessionRef)
	}
	if err != nil {
		return fmt.Errorf("validate collaboration session run: %w", err)
	}
	if runWorkID != binding.WorkID || runNamedAgentID != principalID || runSessionRef != sessionRef || workRoomID != roomID {
		return fmt.Errorf("%w: work session %q run binding does not match", ErrConflict, sessionRef)
	}
	if runState != WorkRunQueued && runState != WorkRunRunning {
		return fmt.Errorf("%w: work session %q run is %s", ErrConflict, sessionRef, runState)
	}
	if terminalWorkState(workState) {
		return fmt.Errorf("%w: work is %s", ErrConflict, workState)
	}
	if runGoalRevision != workGoalRevision || (targetGoalRevision != 0 && targetGoalRevision != workGoalRevision) {
		return fmt.Errorf("%w: work session %q goal revision is stale", ErrConflict, sessionRef)
	}
	return nil
}

func availableCollaborationSessionState(state CollaborationSessionState) bool {
	return state == CollaborationSessionWaiting || state == CollaborationSessionQueued || state == CollaborationSessionIdle || state == CollaborationSessionStarting || state == CollaborationSessionRunning
}

func (s *Service) requireRoomPrincipalAccess(ctx context.Context, roomID, principalID string) error {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM room_members WHERE room_id = ? AND member_type = 'agent' AND member_id = ? UNION ALL SELECT 1 FROM room_runtimes WHERE room_id = ? AND id = ? LIMIT 1`, roomID, principalID, roomID, principalID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	return err
}

// LookupCollaborationSession reads routing metadata for a trusted host caller.
// Agent-facing callers must use GetCollaborationSession instead.
func (s *Service) LookupCollaborationSession(ctx context.Context, sessionRef string) (CollaborationSessionBinding, error) {
	return scanCollaborationSession(s.db.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, strings.TrimSpace(sessionRef)))
}

// ListAllCollaborationSessions provides the host with persisted execution state
// for restart reconciliation. It never includes session transcripts.
func (s *Service) ListAllCollaborationSessions(ctx context.Context) ([]CollaborationSessionBinding, error) {
	rows, err := s.db.QueryContext(ctx, collaborationSessionSelect+` ORDER BY binding.created_at, binding.session_ref`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]CollaborationSessionBinding, 0)
	for rows.Next() {
		session, err := scanCollaborationSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func acceptsCollaborationSessionDelivery(state CollaborationSessionState) bool {
	return availableCollaborationSessionState(state) || state == CollaborationSessionCompleted
}
