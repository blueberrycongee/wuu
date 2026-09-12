package channels

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/schedule"
)

// Followup is a durable continuation or message, owned by a session, an
// identity in a room, or the room coordinator. Waiting consumes no run slot.
type Followup struct {
	ID               string     `json:"id"`
	OwnerID          string     `json:"owner_id"`
	RoomID           string     `json:"room_id"`
	SessionRef       string     `json:"session_ref,omitempty"`
	SourceSessionRef string     `json:"source_session_ref,omitempty"`
	Scope            string     `json:"scope"`
	Mode             string     `json:"mode"`
	Note             string     `json:"note"`
	Cron             string     `json:"schedule,omitempty"`
	Timezone         string     `json:"timezone,omitempty"`
	WhenSession      string     `json:"when_session,omitempty"`
	WorkID           string     `json:"work_id,omitempty"`
	GoalRevision     int        `json:"goal_revision,omitempty"`
	Refs             []string   `json:"refs,omitempty"`
	NextAt           time.Time  `json:"next_at,omitempty"`
	LastAt           *time.Time `json:"last_at,omitempty"`
	State            string     `json:"state"`
	Reason           string     `json:"reason,omitempty"`
	Revision         int        `json:"revision"`
	CreatedAt        time.Time  `json:"created_at"`
}

type FollowupSetParams struct {
	ID          string     `json:"id,omitempty"`
	Revision    int        `json:"revision,omitempty"`
	RequestID   string     `json:"request_id,omitempty"`
	RoomID      string     `json:"room_id,omitempty"`
	Scope       string     `json:"scope,omitempty"`
	Mode        string     `json:"mode,omitempty"`
	Note        string     `json:"note"`
	After       string     `json:"after,omitempty"`
	FireAt      *time.Time `json:"fire_at,omitempty"`
	Cron        string     `json:"schedule,omitempty"`
	Timezone    string     `json:"timezone,omitempty"`
	WhenSession string     `json:"when_session,omitempty"`
	WorkID      string     `json:"work_id,omitempty"`
	Refs        []string   `json:"refs,omitempty"`
}

func (s *Service) migrateFollowups() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS collaboration_followups (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES collaboration_principals(id) ON DELETE CASCADE,
 room_id TEXT NOT NULL REFERENCES rooms(id) ON DELETE CASCADE, session_ref TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL, next_at INTEGER NOT NULL, spec TEXT NOT NULL, request_hash TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_followups_due ON collaboration_followups(state,next_at);
 CREATE INDEX IF NOT EXISTS idx_followups_session ON collaboration_followups(session_ref,state);`)
	return err
}

func nextFollowupTime(cron, zone string, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	expression, err := schedule.ParseCronExpression(cron)
	if err != nil {
		return time.Time{}, err
	}
	next, err := expression.NextRun(now.In(loc))
	return next.UTC(), err
}

func scanFollowup(row scanner) (Followup, error) {
	var raw string
	var f Followup
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return f, ErrNotFound
		}
		return f, err
	}
	err := json.Unmarshal([]byte(raw), &f)
	return f, err
}

func saveFollowupTx(ctx context.Context, tx *sql.Tx, f Followup) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE collaboration_followups SET state=?,next_at=?,spec=? WHERE id=?`, f.State, toMillis(f.NextAt), string(raw), f.ID)
	return err
}

func (c *AgentClient) SetFollowup(ctx context.Context, p FollowupSetParams) (Followup, error) {
	s := c.service
	actor, err := s.AuthenticatePrincipal(ctx, c.agentID, c.token)
	if err != nil {
		return Followup{}, err
	}
	if c.sessionRef == "" {
		return Followup{}, errors.New("follow-up requires a bound session")
	}
	binding, err := s.LookupCollaborationSession(ctx, c.sessionRef)
	if err != nil {
		return Followup{}, err
	}
	if binding.PrincipalID != actor.ID {
		return Followup{}, ErrUnauthorized
	}
	if p.RoomID == "" {
		p.RoomID = binding.RoomID
	}
	if p.RoomID != binding.RoomID {
		return Followup{}, ErrUnauthorized
	}
	if err = s.requireRoomPrincipalAccess(ctx, p.RoomID, actor.ID); err != nil {
		return Followup{}, err
	}
	if p.Scope == "" {
		p.Scope = "session"
	}
	if p.Mode == "" {
		p.Mode = "wake"
	}
	if p.Scope != "session" && p.Scope != "agent" && p.Scope != "room" {
		return Followup{}, errors.New("scope must be session, agent or room")
	}
	if p.Scope == "room" && !actor.IsRoomRuntime() || p.Scope == "agent" && actor.IsRoomRuntime() {
		return Followup{}, ErrUnauthorized
	}
	if p.Mode != "wake" && p.Mode != "message" {
		return Followup{}, errors.New("mode must be wake or message")
	}
	if p.Mode == "message" && actor.IsRoomRuntime() {
		return Followup{}, errors.New("a room coordinator cannot publish reminders; delegate to a named member")
	}
	p.Note = strings.TrimSpace(p.Note)
	if p.Note == "" || utf8.RuneCountInString(p.Note) > MaxMessageRunes || len(p.Refs) > 30 {
		return Followup{}, errors.New("follow-up requires a bounded note and at most 30 references")
	}
	triggers := 0
	if p.After != "" {
		triggers++
	}
	if p.FireAt != nil {
		triggers++
	}
	if p.Cron != "" {
		triggers++
	}
	if p.WhenSession != "" {
		triggers++
	}
	if triggers != 1 {
		return Followup{}, errors.New("choose exactly one of after, fire_at, schedule or when_session")
	}
	now := s.now()
	next := now
	if p.After != "" {
		d, e := time.ParseDuration(p.After)
		if e != nil || d < time.Minute {
			return Followup{}, errors.New("after must be a duration of at least one minute")
		}
		next = now.Add(d)
	}
	if p.FireAt != nil {
		next = p.FireAt.UTC()
		if !next.After(now) {
			return Followup{}, errors.New("fire_at must be in the future")
		}
	}
	if p.Cron != "" {
		if p.Timezone == "" {
			return Followup{}, errors.New("recurring schedules require an IANA timezone")
		}
		next, err = nextFollowupTime(p.Cron, p.Timezone, now)
		if err != nil {
			return Followup{}, err
		}
	}
	if p.WhenSession != "" {
		target, e := c.GetCollaborationSession(ctx, p.WhenSession)
		if e != nil {
			return Followup{}, e
		}
		if target.RoomID != p.RoomID || target.SessionRef == c.sessionRef {
			return Followup{}, errors.New("event target must be another session in this room")
		}
	}
	f := Followup{OwnerID: actor.ID, RoomID: p.RoomID, SourceSessionRef: c.sessionRef, Scope: p.Scope, Mode: p.Mode, Note: p.Note, Cron: p.Cron, Timezone: p.Timezone, WhenSession: p.WhenSession, WorkID: p.WorkID, Refs: p.Refs, NextAt: next, State: "active", Revision: 1, CreatedAt: now}
	if p.Scope == "session" {
		f.SessionRef = c.sessionRef
		if f.WorkID == "" {
			f.WorkID = binding.WorkID
		}
	}
	if f.WorkID != "" {
		work, e := c.GetWork(ctx, f.WorkID)
		if e != nil {
			return Followup{}, e
		}
		if work.RoomID != p.RoomID || terminalWorkState(work.State) {
			return Followup{}, errors.New("follow-up must reference active work in this room")
		}
		f.GoalRevision = work.GoalRevision
	}
	encoded, _ := json.Marshal(p)
	hash := fmt.Sprintf("%x", sha256.Sum256(encoded))
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return f, err
	}
	defer tx.Rollback()
	if err := validateCollaborationSessionWriteTx(ctx, tx, c.sessionRef, actor.ID, p.RoomID, "", 0); err != nil {
		return f, err
	}
	if p.ID != "" {
		previous, e := scanFollowup(tx.QueryRowContext(ctx, `SELECT spec FROM collaboration_followups WHERE id=?`, p.ID))
		if e != nil {
			return f, e
		}
		if previous.OwnerID != actor.ID || previous.RoomID != p.RoomID || previous.Scope == "session" && previous.SessionRef != c.sessionRef {
			return f, ErrUnauthorized
		}
		if previous.Revision != p.Revision {
			return f, fmt.Errorf("%w: follow-up changed; read it before updating", ErrConflict)
		}
		f.ID, f.CreatedAt, f.Revision = p.ID, previous.CreatedAt, previous.Revision+1
		// Updates replace only future intent; already running work has its own controls.
		if _, err = tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=? WHERE correlation_id=? AND consumed_at IS NULL`, toMillis(now), f.ID); err != nil {
			return f, err
		}
		raw, _ := json.Marshal(f)
		_, err = tx.ExecContext(ctx, `UPDATE collaboration_followups SET session_ref=?,state=?,next_at=?,spec=?,request_hash=? WHERE id=?`, f.SessionRef, f.State, toMillis(f.NextAt), string(raw), hash, f.ID)
	} else {
		if strings.TrimSpace(p.RequestID) == "" {
			return f, errors.New("creating a follow-up requires a stable request_id")
		}
		f.ID = fmt.Sprintf("followup-%x", sha256.Sum256([]byte(actor.ID+"\x00"+c.sessionRef+"\x00"+p.RequestID)))
		var oldHash string
		e := tx.QueryRowContext(ctx, `SELECT request_hash FROM collaboration_followups WHERE id=?`, f.ID).Scan(&oldHash)
		if e == nil {
			if oldHash != hash {
				return f, fmt.Errorf("%w: request_id was used with different instructions", ErrConflict)
			}
			return scanFollowup(tx.QueryRowContext(ctx, `SELECT spec FROM collaboration_followups WHERE id=?`, f.ID))
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return f, e
		}
		raw, _ := json.Marshal(f)
		_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_followups VALUES(?,?,?,?,?,?,?,?)`, f.ID, f.OwnerID, f.RoomID, f.SessionRef, f.State, toMillis(next), string(raw), hash)
	}
	if err != nil {
		return f, err
	}
	return f, tx.Commit()
}

// ListRoomFollowups is a host read. Agents use their bound client below.
func (s *Service) ListRoomFollowups(ctx context.Context, roomID string) ([]Followup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT spec FROM collaboration_followups WHERE room_id=? ORDER BY next_at,id`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Followup{}
	for rows.Next() {
		f, e := scanFollowup(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, f)
	}
	return result, rows.Err()
}
func (c *AgentClient) ListFollowups(ctx context.Context, roomID string) ([]Followup, error) {
	if roomID == "" {
		b, e := c.service.LookupCollaborationSession(ctx, c.sessionRef)
		if e != nil {
			return nil, e
		}
		roomID = b.RoomID
	}
	if _, e := c.service.AuthenticatePrincipal(ctx, c.agentID, c.token); e != nil {
		return nil, e
	}
	if e := c.service.requireRoomPrincipalAccess(ctx, roomID, c.agentID); e != nil {
		return nil, e
	}
	return c.service.ListRoomFollowups(ctx, roomID)
}

func (c *AgentClient) ControlFollowup(ctx context.Context, id, state string, revision int) (Followup, error) {
	if _, err := c.service.AuthenticatePrincipal(ctx, c.agentID, c.token); err != nil {
		return Followup{}, err
	}
	return c.service.controlFollowup(ctx, id, state, revision, c.agentID, c.sessionRef, "")
}

// ControlRoomFollowup is the human room-management path.
func (s *Service) ControlRoomFollowup(ctx context.Context, roomID, id, state string, revision int) (Followup, error) {
	return s.controlFollowup(ctx, id, state, revision, "", "", roomID)
}
func (s *Service) controlFollowup(ctx context.Context, id, state string, revision int, actor, ref, room string) (Followup, error) {
	if state != "paused" && state != "active" && state != "cancelled" {
		return Followup{}, errors.New("state must be active, paused or cancelled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Followup{}, err
	}
	defer tx.Rollback()
	f, err := scanFollowup(tx.QueryRowContext(ctx, `SELECT spec FROM collaboration_followups WHERE id=?`, id))
	if err != nil {
		return f, err
	}
	if actor != "" {
		if f.OwnerID != actor || f.Scope == "session" && f.SessionRef != ref {
			return f, ErrUnauthorized
		}
		if err = requireRoomPrincipalAccessTx(ctx, tx, f.RoomID, actor); err != nil {
			return f, err
		}
	} else if room == "" || room != f.RoomID {
		return f, ErrUnauthorized
	}
	if f.Revision != revision {
		return f, fmt.Errorf("%w: follow-up changed", ErrConflict)
	}
	if state == "active" && f.State != "paused" {
		return f, errors.New("only a paused follow-up can resume; create a new arrangement after completion or cancellation")
	}
	f.State = state
	f.Revision++
	if state != "active" {
		if _, err = tx.ExecContext(ctx, `UPDATE collaboration_messages SET invalidated_at=? WHERE correlation_id=? AND consumed_at IS NULL`, toMillis(s.now()), id); err != nil {
			return f, err
		}
	}
	if err = saveFollowupTx(ctx, tx, f); err != nil {
		return f, err
	}
	return f, tx.Commit()
}
