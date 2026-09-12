package channels

import (
	"context"
	"errors"
	"time"
)

type RoomHistoryQuery struct {
	RoomID    string
	AfterSeq  int64
	BeforeSeq int64
	ThreadID  string
	Query     string
	Limit     int
}

func (c *AgentClient) QueryRoomHistory(ctx context.Context, p RoomHistoryQuery) ([]Message, error) {
	if _, err := c.service.AuthenticatePrincipal(ctx, c.agentID, c.token); err != nil {
		return nil, err
	}
	if err := c.service.requireRoomPrincipalAccess(ctx, p.RoomID, c.agentID); err != nil {
		return nil, err
	}
	if p.AfterSeq < 0 || p.BeforeSeq < 0 {
		return nil, errors.New("sequences must be non-negative")
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}
	return c.service.queryMessages(ctx, p)
}

type SessionResult struct {
	ID         int64     `json:"id"`
	SessionRef string    `json:"session_ref"`
	TurnID     string    `json:"turn_id"`
	Body       string    `json:"body"`
	Truncated  bool      `json:"truncated,omitempty"`
	State      string    `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
}
type SessionResults struct {
	Results []SessionResult `json:"results"`
	Next    int64           `json:"next,omitempty"`
}

func (c *AgentClient) ReadSessionResults(ctx context.Context, ref string, after int64, limit int) (SessionResults, error) {
	result := SessionResults{Results: []SessionResult{}}
	if _, err := c.GetCollaborationSession(ctx, ref); err != nil {
		return result, err
	}
	if after < 0 {
		return result, errors.New("after must be non-negative")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := c.service.db.QueryContext(ctx, `SELECT id,session_ref,turn_id,body,state,created_at FROM collaboration_results WHERE session_ref=? AND id>? ORDER BY id LIMIT ?`, ref, after, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(result.Results) == limit {
			result.Next = result.Results[len(result.Results)-1].ID
			break
		}
		var item SessionResult
		var at int64
		if err = rows.Scan(&item.ID, &item.SessionRef, &item.TurnID, &item.Body, &item.State, &at); err != nil {
			return result, err
		}
		item.CreatedAt = fromMillis(at)
		if body := []rune(item.Body); len(body) > 400 {
			item.Body = string(body[:400])
			item.Truncated = true
		}
		result.Results = append(result.Results, item)
	}
	return result, rows.Err()
}

// ReadSessionResultPage retrieves a selected result without loading a private transcript.
func (c *AgentClient) ReadSessionResultPage(ctx context.Context, ref string, id int64, offset int) (map[string]any, error) {
	if _, err := c.GetCollaborationSession(ctx, ref); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, errors.New("offset must be non-negative")
	}
	var body string
	if err := c.service.db.QueryRowContext(ctx, `SELECT body FROM collaboration_results WHERE session_ref=? AND id=?`, ref, id).Scan(&body); err != nil {
		return nil, err
	}
	chars := []rune(body)
	if offset > len(chars) {
		return nil, errors.New("offset exceeds result length")
	}
	end := min(offset+16000, len(chars))
	next := 0
	if end < len(chars) {
		next = end
	}
	return map[string]any{"id": id, "session_ref": ref, "body": string(chars[offset:end]), "next_offset": next}, nil
}
