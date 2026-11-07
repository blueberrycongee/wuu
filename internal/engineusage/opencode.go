package engineusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// scanOpenCode reads ~/.local/share/opencode/opencode.db. Step-finish parts
// carry per-step token counts; session ids match the references Wuu persists
// for opencode threads. The database is opened read-only and treated as one
// cached file — unchanged mtime and size reuse the previous scan's rows.
func (s *Scanner) scanOpenCode(ctx context.Context, exclude func(string, string) bool) ([]Row, error) {
	path := s.openCodeDB()
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil
	}
	if cached, ok := s.cachedRows(path, info); ok {
		return cached, nil
	}
	rows, err := readOpenCodeRows(ctx, path, exclude)
	if err != nil {
		return nil, err
	}
	s.storeRows(path, info, rows)
	return rows, nil
}

func readOpenCodeRows(ctx context.Context, path string, exclude func(string, string) bool) ([]Row, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query := `SELECT session_id, time_created, json_extract(data, '$.tokens')
		FROM part WHERE json_extract(data, '$.type') = 'step-finish'`
	sqlRows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()
	var rows []Row
	for sqlRows.Next() {
		var sessionID, tokensJSON string
		var createdMS int64
		if err := sqlRows.Scan(&sessionID, &createdMS, &tokensJSON); err != nil {
			return rows, err
		}
		var tokens struct {
			Input     int `json:"input"`
			Output    int `json:"output"`
			Reasoning int `json:"reasoning"`
			Cache     struct {
				Read  int `json:"read"`
				Write int `json:"write"`
			} `json:"cache"`
		}
		if json.Unmarshal([]byte(tokensJSON), &tokens) != nil || exclude("opencode", sessionID) {
			continue
		}
		row := Row{
			Engine:              "opencode",
			SessionID:           sessionID,
			At:                  time.UnixMilli(createdMS).UTC(),
			InputTokens:         tokens.Input,
			OutputTokens:        tokens.Output + tokens.Reasoning,
			CacheCreationTokens: tokens.Cache.Write,
			CacheReadTokens:     tokens.Cache.Read,
		}
		rows = append(rows, row)
	}
	return rows, sqlRows.Err()
}
