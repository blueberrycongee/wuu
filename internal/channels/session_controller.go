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

// SessionController connects durable collaboration state to the host's agent
// execution. Implementations must reuse CreateSession's reserved SessionRef.
type SessionController interface {
	CreateSession(context.Context, CollaborationSessionCreateParams) (CollaborationSessionBinding, error)
	SendSession(context.Context, CollaborationSessionSendParams) (CollaborationSessionBinding, error)
	StopSession(context.Context, CollaborationSessionControlParams) (CollaborationSessionBinding, error)
	ResumeSession(context.Context, CollaborationSessionControlParams) (CollaborationSessionBinding, error)
}

type CollaborationSessionCreateParams struct {
	NamedAgentID     string
	RoomID           string
	Title            string
	Objective        string
	ParentSessionRef string
	Provider         string
	Model            string
	Effort           string
	RequestID        string
	SessionRef       string
	ActorID          string
	SourceSessionRef string
	Token            string `json:"-"`
}

type CollaborationSessionSendParams struct {
	SessionRef       string
	Body             string
	RequestID        string
	ActorID          string
	SourceSessionRef string
	Token            string `json:"-"`
}

type CollaborationSessionControlParams struct {
	SessionRef       string
	ActorID          string
	SourceSessionRef string
	Token            string `json:"-"`
}

func (s *Service) SetSessionController(controller SessionController) {
	s.sessionControllerMu.Lock()
	defer s.sessionControllerMu.Unlock()
	s.sessionController = controller
}

func (s *Service) sessionExecutionController() (SessionController, error) {
	s.sessionControllerMu.RLock()
	defer s.sessionControllerMu.RUnlock()
	if s.sessionController == nil {
		return nil, errors.New("collaboration session execution is unavailable")
	}
	return s.sessionController, nil
}

// CreateSession accepts an authenticated AgentClient or a trusted host call
// with ActorID empty. The durable reservation makes a retry reuse the same
// execution even if the host exits between creation and the response.
func (s *Service) CreateSession(ctx context.Context, params CollaborationSessionCreateParams) (CollaborationSessionBinding, error) {
	controller, err := s.sessionExecutionController()
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	params.NamedAgentID = strings.TrimSpace(params.NamedAgentID)
	params.RoomID = strings.TrimSpace(params.RoomID)
	params.ActorID = strings.TrimSpace(params.ActorID)
	params.SourceSessionRef = strings.TrimSpace(params.SourceSessionRef)
	params.ParentSessionRef = strings.TrimSpace(params.ParentSessionRef)
	params.Title, params.Objective = strings.TrimSpace(params.Title), strings.TrimSpace(params.Objective)
	params.Provider, params.Model, params.Effort = strings.TrimSpace(params.Provider), strings.TrimSpace(params.Model), strings.TrimSpace(params.Effort)
	params.RequestID = strings.TrimSpace(params.RequestID)
	if params.NamedAgentID == "" {
		params.NamedAgentID = params.ActorID
	}
	if params.ParentSessionRef == "" {
		params.ParentSessionRef = params.SourceSessionRef
	}
	if params.NamedAgentID == "" || params.RoomID == "" || params.Objective == "" {
		return CollaborationSessionBinding{}, errors.New("collaboration session agent, room and objective are required")
	}
	if _, err := s.GetNamedAgent(ctx, params.NamedAgentID); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := s.authorizeSessionActor(ctx, params.ActorID, params.Token, params.SourceSessionRef, params.RoomID); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := s.requireRoomPrincipalAccess(ctx, params.RoomID, params.NamedAgentID); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if params.ParentSessionRef != "" {
		parent, err := scanCollaborationSession(s.db.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, params.ParentSessionRef))
		if err != nil {
			return CollaborationSessionBinding{}, err
		}
		if parent.RoomID != params.RoomID {
			return CollaborationSessionBinding{}, ErrUnauthorized
		}
		if params.ActorID != "" && parent.PrincipalID != params.ActorID {
			actor, err := s.AuthenticatePrincipal(ctx, params.ActorID, params.Token)
			if err != nil || !actor.IsRoomRuntime() || actor.RoomID != params.RoomID {
				return CollaborationSessionBinding{}, ErrUnauthorized
			}
		}
	}
	if params.RequestID == "" {
		params.RequestID, err = randomID("session-request", 12)
		if err != nil {
			return CollaborationSessionBinding{}, err
		}
	}
	// Caller-provided handles are never allowed to replace an existing session.
	params.SessionRef = ""
	encoded, err := json.Marshal(params)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	requestHash := hex.EncodeToString(digest[:])
	reserved, err := randomID("collab-session", 16)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO collaboration_session_requests(actor_id, source_session_ref, request_id, session_ref, request_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`, params.ActorID, params.SourceSessionRef, params.RequestID, reserved, requestHash, toMillis(s.now())); err != nil {
		return CollaborationSessionBinding{}, fmt.Errorf("reserve collaboration session: %w", err)
	}
	var storedHash string
	if err := s.db.QueryRowContext(ctx, `SELECT session_ref, request_hash FROM collaboration_session_requests WHERE actor_id = ? AND source_session_ref = ? AND request_id = ?`, params.ActorID, params.SourceSessionRef, params.RequestID).Scan(&params.SessionRef, &storedHash); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if storedHash != requestHash {
		return CollaborationSessionBinding{}, fmt.Errorf("%w: session request id was reused for different content", ErrConflict)
	}
	params.Token = ""
	return controller.CreateSession(ctx, params)
}

func (s *Service) authorizeSessionActor(ctx context.Context, actorID, token, sourceSessionRef, roomID string) error {
	if actorID == "" {
		if sourceSessionRef != "" {
			return ErrUnauthorized
		}
		return nil
	}
	actor, err := s.AuthenticatePrincipal(ctx, actorID, token)
	if err != nil {
		return err
	}
	if err := s.requireRoomPrincipalAccess(ctx, roomID, actor.ID); err != nil {
		return err
	}
	if sourceSessionRef == "" {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A stale goal or cancelled source cannot create new descendants or send
	// fresh work, even if another session under its identity remains active.
	return validateCollaborationSessionWriteTx(ctx, tx, sourceSessionRef, actor.ID, roomID, "", 0)
}

func (s *Service) authorizeSessionTarget(ctx context.Context, params CollaborationSessionControlParams, control bool) (CollaborationSessionBinding, error) {
	binding, err := scanCollaborationSession(s.db.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, strings.TrimSpace(params.SessionRef)))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := s.authorizeSessionActor(ctx, params.ActorID, params.Token, params.SourceSessionRef, binding.RoomID); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if params.ActorID == "" {
		return binding, nil
	}
	if err := s.requireRoomPrincipalAccess(ctx, binding.RoomID, binding.PrincipalID); err != nil {
		return CollaborationSessionBinding{}, err
	}
	if !control || params.ActorID == binding.PrincipalID {
		return binding, nil
	}
	actor, err := s.AuthenticatePrincipal(ctx, params.ActorID, params.Token)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if actor.IsRoomRuntime() && actor.RoomID == binding.RoomID {
		return binding, nil
	}
	if binding.ParentSessionRef != "" {
		var parentPrincipal string
		if err := s.db.QueryRowContext(ctx, `SELECT principal_id FROM collaboration_session_bindings WHERE session_ref = ?`, binding.ParentSessionRef).Scan(&parentPrincipal); err == nil && parentPrincipal == params.ActorID {
			return binding, nil
		}
	}
	return CollaborationSessionBinding{}, ErrUnauthorized
}

func (s *Service) SendSession(ctx context.Context, params CollaborationSessionSendParams) (CollaborationSessionBinding, error) {
	controller, err := s.sessionExecutionController()
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding, err := s.authorizeSessionTarget(ctx, CollaborationSessionControlParams{SessionRef: params.SessionRef, ActorID: params.ActorID, SourceSessionRef: params.SourceSessionRef, Token: params.Token}, false)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	params.Body = strings.TrimSpace(params.Body)
	if params.Body == "" {
		return CollaborationSessionBinding{}, errors.New("collaboration session message body is required")
	}
	if !acceptsCollaborationSessionDelivery(binding.State) {
		return CollaborationSessionBinding{}, fmt.Errorf("%w: session %q is %s; resume it before sending", ErrConflict, binding.SessionRef, binding.State)
	}
	params.SessionRef, params.Token = binding.SessionRef, ""
	return controller.SendSession(ctx, params)
}

func (s *Service) StopSession(ctx context.Context, params CollaborationSessionControlParams) (CollaborationSessionBinding, error) {
	controller, err := s.sessionExecutionController()
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding, err := s.authorizeSessionTarget(ctx, params, true)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	params.SessionRef, params.Token = binding.SessionRef, ""
	return controller.StopSession(ctx, params)
}

func (s *Service) ResumeSession(ctx context.Context, params CollaborationSessionControlParams) (CollaborationSessionBinding, error) {
	controller, err := s.sessionExecutionController()
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding, err := s.authorizeSessionTarget(ctx, params, true)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	params.SessionRef, params.Token = binding.SessionRef, ""
	return controller.ResumeSession(ctx, params)
}

func (c *AgentClient) CreateSession(ctx context.Context, params CollaborationSessionCreateParams) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, ErrUnauthorized
	}
	params.ActorID, params.Token, params.SourceSessionRef = c.agentID, c.token, c.sessionRef
	return c.service.CreateSession(ctx, params)
}

func (c *AgentClient) SendSession(ctx context.Context, params CollaborationSessionSendParams) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, ErrUnauthorized
	}
	params.ActorID, params.Token, params.SourceSessionRef = c.agentID, c.token, c.sessionRef
	return c.service.SendSession(ctx, params)
}

func (c *AgentClient) StopSession(ctx context.Context, params CollaborationSessionControlParams) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, ErrUnauthorized
	}
	params.ActorID, params.Token, params.SourceSessionRef = c.agentID, c.token, c.sessionRef
	return c.service.StopSession(ctx, params)
}

func (c *AgentClient) ResumeSession(ctx context.Context, params CollaborationSessionControlParams) (CollaborationSessionBinding, error) {
	if c == nil || c.service == nil {
		return CollaborationSessionBinding{}, ErrUnauthorized
	}
	params.ActorID, params.Token, params.SourceSessionRef = c.agentID, c.token, c.sessionRef
	return c.service.ResumeSession(ctx, params)
}

// SessionLimits applies to every collaboration session, including those that
// have no Work record. Waiting and queued sessions do not occupy a run slot.
type CollaborationSessionLimits struct {
	PerIdentity int `json:"per_identity"`
	PerRoom     int `json:"per_room"`
	Global      int `json:"global"`
}

func (s *Service) SessionLimits() CollaborationSessionLimits {
	return CollaborationSessionLimits{PerIdentity: s.agentRunLimit, PerRoom: s.roomRunLimit, Global: s.globalRunLimit}
}

func (s *Service) AdmitCollaborationSession(ctx context.Context, sessionRef string) (CollaborationSessionBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	defer tx.Rollback()
	binding, err := scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, strings.TrimSpace(sessionRef)))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if binding.State == CollaborationSessionStarting || binding.State == CollaborationSessionRunning {
		return binding, nil
	}
	if binding.State != CollaborationSessionIdle && binding.State != CollaborationSessionQueued && binding.State != CollaborationSessionCompleted && binding.State != CollaborationSessionWaiting {
		return CollaborationSessionBinding{}, fmt.Errorf("%w: session must be idle, queued, waiting or completed before admission", ErrConflict)
	}
	identityCount, roomCount, globalCount, err := activeCollaborationCountsTx(ctx, tx, binding.PrincipalID, binding.RoomID, binding.SessionRef)
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	state := CollaborationSessionStarting
	if identityCount >= s.agentRunLimit || roomCount >= s.roomRunLimit || globalCount >= s.globalRunLimit {
		state = CollaborationSessionQueued
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_session_bindings SET state = ?, turn_id = CASE WHEN ? = 'starting' THEN '' ELSE turn_id END, updated_at = ? WHERE session_ref = ?`, state, state, toMillis(s.now()), binding.SessionRef); err != nil {
		return CollaborationSessionBinding{}, err
	}
	binding, err = scanCollaborationSession(tx.QueryRowContext(ctx, collaborationSessionSelect+` WHERE binding.session_ref = ?`, binding.SessionRef))
	if err != nil {
		return CollaborationSessionBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return CollaborationSessionBinding{}, err
	}
	return binding, nil
}

func (s *Service) ListQueuedCollaborationSessions(ctx context.Context) ([]CollaborationSessionBinding, error) {
	rows, err := s.db.QueryContext(ctx, collaborationSessionSelect+` WHERE binding.state = 'queued' ORDER BY binding.created_at, binding.session_ref`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CollaborationSessionBinding, 0)
	for rows.Next() {
		binding, err := scanCollaborationSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

// A Work run and its session are one reservation. Include runs without a
// binding and independent sessions without double-counting attached work.
func activeCollaborationCountsTx(ctx context.Context, tx *sql.Tx, principalID, roomID, excludeSession string) (identity, room, global int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN principal_id = ? THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN room_id = ? THEN 1 ELSE 0 END),0),COUNT(*) FROM (
 SELECT COALESCE(run.named_agent_id,'') AS principal_id,work.room_id FROM work_runs run JOIN works work ON work.id=run.work_id WHERE run.state='running'
 UNION ALL
 SELECT binding.principal_id,binding.room_id FROM collaboration_session_bindings binding WHERE binding.state IN ('starting','running') AND binding.session_ref != ? AND NOT EXISTS(SELECT 1 FROM work_runs run WHERE run.id=binding.run_id AND run.state='running')
 )`, principalID, roomID, excludeSession).Scan(&identity, &room, &global)
	return
}

// AdmitQueuedWorkRuns lets any released collaboration slot advance Work
// reservations as well as independent sessions. Dispatch happens after commit.
func (s *Service) AdmitQueuedWorkRuns(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	wakeIDs, err := s.admitQueuedWorkRunsTx(ctx, tx, s.now())
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.wake != nil {
		for _, id := range wakeIDs {
			s.wake.Deliver(id)
		}
	}
	return nil
}
