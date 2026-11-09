// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"fmt"
)

// ListTokenUsage returns every persisted token_usage row without reading
// conversation content, so its cost does not grow with message or tool output
// size. Row order is unspecified. A missing store has no rows yet.
func ListTokenUsage(sessDir string) ([]TokenUsageRow, error) {
	db, ok, err := openStoreForScan(sessDir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT m.session_id, m.at, m.provider, m.model,
		m.input_tokens, m.output_tokens, m.cache_creation_tokens, m.cache_read_tokens
		FROM sessions s CROSS JOIN session_messages m
		ON m.session_id = s.id AND m.role = 'meta' AND m.content = ?`, tokenUsageContent)
	if err != nil {
		return nil, fmt.Errorf("list token usage: %w", err)
	}
	defer rows.Close()

	var out []TokenUsageRow
	for rows.Next() {
		var row TokenUsageRow
		var at sql.NullString
		if err := rows.Scan(&row.SessionID, &at, &row.Provider, &row.Model,
			&row.InputTokens, &row.OutputTokens, &row.CacheCreationTokens, &row.CacheReadTokens); err != nil {
			return nil, fmt.Errorf("scan token usage: %w", err)
		}
		if at.Valid {
			row.At = parseTime(at.String)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list token usage: %w", err)
	}
	return out, nil
}
