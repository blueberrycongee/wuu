package engineusage

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"
)

// scanDevin reads ~/.local/share/devin/cli/sessions.db. Devin threads run over
// ACP, which reports no token usage, so Wuu's trail never covers a Devin
// session: bound sessions stay in the result and the caller attributes them
// to the in-Wuu column itself. Assistant message nodes carry per-request
// metrics; the same request_id is copied onto several nodes when history
// branches, so rows dedupe by request_id, then message_id. The database is
// opened read-only and cached like the other ledgers — keyed on the main
// file plus its -wal sidecar, since WAL-mode writes leave the main file's
// size and mtime unchanged.
func (s *Scanner) scanDevin(ctx context.Context) ([]Row, error) {
	path := s.devinDB()
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil
	}
	size, modTime := info.Size(), info.ModTime()
	if wal, err := os.Stat(path + "-wal"); err == nil {
		size += wal.Size()
		if wal.ModTime().After(modTime) {
			modTime = wal.ModTime()
		}
	}
	if cached, ok := s.cachedRows(path, size, modTime); ok {
		return cached, nil
	}
	rows, err := readDevinRows(ctx, path)
	if err != nil {
		return nil, err
	}
	s.storeRows(path, size, modTime, rows)
	return rows, nil
}

func readDevinRows(ctx context.Context, path string) ([]Row, error) {
	db, err := openLedgerDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query := `SELECT session_id, node_id, created_at,
		COALESCE(json_extract(chat_message, '$.message_id'), ''),
		COALESCE(json_extract(chat_message, '$.metadata.request_id'), ''),
		COALESCE(json_extract(chat_message, '$.metadata.generation_model'), ''),
		json_extract(chat_message, '$.metadata.metrics')
		FROM message_nodes
		WHERE json_extract(chat_message, '$.role') = 'assistant'
		  AND json_type(chat_message, '$.metadata.metrics') = 'object'
		ORDER BY row_id`
	sqlRows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer sqlRows.Close()
	var rows []Row
	indexByKey := make(map[string]int)
	for sqlRows.Next() {
		var sessionID, messageID, requestID, model, metricsJSON string
		var nodeID, createdSec int64
		if err := sqlRows.Scan(&sessionID, &nodeID, &createdSec, &messageID, &requestID, &model, &metricsJSON); err != nil {
			return rows, err
		}
		var metrics struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_tokens"`
			CacheReadTokens     int `json:"cache_read_tokens"`
		}
		if json.Unmarshal([]byte(metricsJSON), &metrics) != nil {
			continue
		}
		key := "node:" + sessionID + "|" + strconv.FormatInt(nodeID, 10)
		switch {
		case requestID != "":
			key = "req:" + requestID
		case messageID != "":
			key = "msg:" + sessionID + "|" + messageID
		}
		at := time.Unix(createdSec, 0).UTC()
		if idx, ok := indexByKey[key]; ok {
			if at.Before(rows[idx].At) {
				rows[idx].At = at
			}
			continue
		}
		indexByKey[key] = len(rows)
		rows = append(rows, Row{
			Engine:              "devin",
			SessionID:           sessionID,
			At:                  at,
			Model:               model,
			InputTokens:         metrics.InputTokens,
			OutputTokens:        metrics.OutputTokens,
			CacheCreationTokens: metrics.CacheCreationTokens,
			CacheReadTokens:     metrics.CacheReadTokens,
		})
	}
	return rows, sqlRows.Err()
}
