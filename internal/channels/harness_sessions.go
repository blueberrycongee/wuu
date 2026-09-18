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

// HarnessSessionController adapts Collaboration policy to ordinary host sessions.
// Identity and turn provenance are supplied by AgentClient, never by the model.
type HarnessSessionController interface {
	HarnessSession(context.Context, HarnessSessionActor, HarnessSessionParams) (any, error)
}

type HarnessSessionActor struct {
	AgentID      string `json:"agent_id"`
	SessionRef   string `json:"session_ref"`
	TurnID       string `json:"turn_id"`
	RoomID       string `json:"room_id"`
	WorkID       string `json:"work_id,omitempty"`
	GoalRevision int    `json:"goal_revision,omitempty"`
}

type HarnessSessionParams struct {
	Action        string `json:"action"`
	WorkID        string `json:"work_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	Title         string `json:"title,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	Query         string `json:"query,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Effort        string `json:"effort,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Before        int    `json:"before,omitempty"`
	OperationID   string `json:"-"`
}

type HarnessSessionLink struct {
	SessionID        string `json:"session_id"`
	AgentID          string `json:"agent_id"`
	SourceSessionRef string `json:"source_session_ref"`
	SourceTurnID     string `json:"source_turn_id"`
	RoomID           string `json:"room_id"`
	WorkID           string `json:"work_id,omitempty"`
	GoalRevision     int    `json:"goal_revision,omitempty"`
	Objective        string `json:"objective"`
	ControlRevision  int64  `json:"control_revision"`
	Active           bool   `json:"active"`
	LastTurnID       string `json:"last_turn_id,omitempty"`
	Turns            int    `json:"turns"`
	Failures         int    `json:"failures"`
}

// HarnessOperation is an outbox command. Pending commands survive host restarts;
// the executor reconciles their stable input IDs with durable Harness history.
type HarnessOperation struct {
	ID       string               `json:"id"`
	Actor    HarnessSessionActor  `json:"actor"`
	Params   HarnessSessionParams `json:"params"`
	Revision int64                `json:"revision"`
	State    string               `json:"state"`
	TurnID   string               `json:"turn_id,omitempty"`
	Error    string               `json:"error,omitempty"`
	Prepared bool                 `json:"prepared,omitempty"`
}

func (s *Service) migrateHarnessSessions() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS harness_session_links (
		session_id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, room_id TEXT NOT NULL,
		active INTEGER NOT NULL, payload TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS harness_session_operations (
		id TEXT PRIMARY KEY, request_hash TEXT NOT NULL, session_id TEXT NOT NULL,
		state TEXT NOT NULL, payload TEXT NOT NULL, created_at INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS harness_session_pending ON harness_session_operations(state,created_at);`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS harness_session_admissions (
		session_id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, room_id TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS harness_session_usage (
		session_id TEXT NOT NULL, turn_id TEXT NOT NULL, room_id TEXT NOT NULL, work_id TEXT NOT NULL,
		input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL, PRIMARY KEY(session_id,turn_id));`)
	return err
}

var ErrHarnessCapacity = errors.New("execution queued for collaboration capacity")

// ReserveHarnessExecution runs while the host holds the target's execution
// lease. One slot stays available to the managing identity for user messages.
func (s *Service) ReserveHarnessExecution(ctx context.Context, link HarnessSessionLink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var work *Work
	if link.WorkID != "" {
		w, err := scanWork(tx.QueryRowContext(ctx, workSelect+` WHERE work.id=?`, link.WorkID))
		if err != nil {
			return err
		}
		work = &w
	}
	if err := s.checkCollaborationTokenBudgetTx(ctx, tx, link.RoomID, work); err != nil {
		return err
	}
	identity, room, global, err := activeCollaborationCountsTx(ctx, tx, link.AgentID, link.RoomID, link.SessionID)
	if err != nil {
		return err
	}
	if identity >= max(1, s.agentRunLimit-1) || room >= max(1, s.roomRunLimit-1) || global >= max(1, s.globalRunLimit-1) {
		return ErrHarnessCapacity
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO harness_session_admissions(session_id,agent_id,room_id) VALUES(?,?,?)`, link.SessionID, link.AgentID, link.RoomID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) ReleaseHarnessExecution(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM harness_session_admissions WHERE session_id=?`, id)
	return err
}

func (s *Service) RecordHarnessUsage(ctx context.Context, link HarnessSessionLink, turnID string, input, output int) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO harness_session_usage(session_id,turn_id,room_id,work_id,input_tokens,output_tokens) VALUES(?,?,?,?,?,?)`, link.SessionID, turnID, link.RoomID, link.WorkID, input, output)
	return err
}

func (c *AgentClient) HarnessSession(ctx context.Context, p HarnessSessionParams) (any, error) {
	if c == nil || c.service == nil || c.sessionRef == "" {
		return nil, ErrUnauthorized
	}
	s := c.service
	b, err := s.GetCollaborationSession(ctx, c.agentID, c.token, c.sessionRef)
	if err != nil {
		return nil, err
	}
	scope, err := s.LookupCollaborationTurnScope(ctx, c.sessionRef, b.TurnID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeSessionActor(ctx, c.agentID, c.token, c.sessionRef, scope.RoomID); err != nil {
		return nil, err
	}
	controller, err := s.sessionExecutionController()
	if err != nil {
		return nil, err
	}
	host, ok := controller.(HarnessSessionController)
	if !ok {
		return nil, errors.New("Harness session management is unavailable")
	}
	return host.HarnessSession(ctx, HarnessSessionActor{AgentID: c.agentID, SessionRef: c.sessionRef, TurnID: b.TurnID, RoomID: scope.RoomID, WorkID: scope.WorkID, GoalRevision: scope.GoalRevision}, p)
}

func (s *Service) PutHarnessLink(ctx context.Context, link HarnessSessionLink) error {
	data, err := json.Marshal(link)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO harness_session_links(session_id,agent_id,room_id,active,payload) VALUES(?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET agent_id=excluded.agent_id,room_id=excluded.room_id,active=excluded.active,payload=excluded.payload WHERE json_extract(harness_session_links.payload,'$.control_revision')<=json_extract(excluded.payload,'$.control_revision')`, link.SessionID, link.AgentID, link.RoomID, link.Active, string(data))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *Service) HarnessLinks(ctx context.Context, agentID, roomID string) ([]HarnessSessionLink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM harness_session_links WHERE (?='' OR agent_id=?) AND (?='' OR room_id=?) ORDER BY rowid`, agentID, agentID, roomID, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := make([]HarnessSessionLink, 0)
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var link HarnessSessionLink
		if err := json.Unmarshal([]byte(data), &link); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (s *Service) HarnessLink(ctx context.Context, id string) (HarnessSessionLink, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM harness_session_links WHERE session_id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return HarnessSessionLink{}, ErrNotFound
	}
	if err != nil {
		return HarnessSessionLink{}, err
	}
	var link HarnessSessionLink
	err = json.Unmarshal([]byte(data), &link)
	return link, err
}

func (s *Service) ReserveHarnessOperation(ctx context.Context, actor HarnessSessionActor, p HarnessSessionParams, reservedSession string, revision int64) (HarnessOperation, bool, error) {
	if strings.TrimSpace(p.OperationID) == "" {
		return HarnessOperation{}, false, errors.New("missing host operation id")
	}
	key := sha256.Sum256([]byte(actor.SessionRef + "\x00" + actor.TurnID + "\x00" + p.OperationID))
	id := "harness-" + hex.EncodeToString(key[:16])
	request, _ := json.Marshal(p)
	digest := sha256.Sum256(request)
	hash := hex.EncodeToString(digest[:])
	p.SessionID = reservedSession
	op := HarnessOperation{ID: id, Actor: actor, Params: p, Revision: revision, State: "pending"}
	payload, err := json.Marshal(op)
	if err != nil {
		return op, false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO harness_session_operations(id,request_hash,session_id,state,payload,created_at) VALUES(?,?,?,?,?,?)`, id, hash, reservedSession, op.State, string(payload), toMillis(s.now()))
	if err != nil {
		return op, false, err
	}
	affected, _ := result.RowsAffected()
	var storedHash, data string
	err = s.db.QueryRowContext(ctx, `SELECT request_hash,payload FROM harness_session_operations WHERE id=?`, id).Scan(&storedHash, &data)
	if err != nil {
		return op, false, err
	}
	if storedHash != hash {
		return op, false, fmt.Errorf("%w: operation id reused with different input", ErrConflict)
	}
	err = json.Unmarshal([]byte(data), &op)
	return op, affected == 1, err
}

func (s *Service) PutHarnessOperation(ctx context.Context, op HarnessOperation) error {
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE harness_session_operations SET state=?,payload=? WHERE id=?`, op.State, string(data), op.ID)
	return err
}

func (s *Service) PendingHarnessOperations(ctx context.Context) ([]HarnessOperation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM harness_session_operations WHERE state IN ('pending','submitted') ORDER BY created_at,rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []HarnessOperation
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var op HarnessOperation
		if err := json.Unmarshal([]byte(data), &op); err != nil {
			return nil, err
		}
		result = append(result, op)
	}
	return result, rows.Err()
}

func (s *Service) HarnessOperation(ctx context.Context, id string) (HarnessOperation, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM harness_session_operations WHERE id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return HarnessOperation{}, ErrNotFound
	}
	if err != nil {
		return HarnessOperation{}, err
	}
	var op HarnessOperation
	err = json.Unmarshal([]byte(data), &op)
	return op, err
}
