// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"context"
	"database/sql"
	"database/sql/driver"

	"errors"
	"fmt"

	"strings"

	"modernc.org/sqlite"
)

func init() {

	sqlite.MustRegisterDeterministicScalarFunction("wuu_history_content", 3, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		record := HistoryRecord{
			Role:       args[0].(string),
			Content:    args[1].(string),
			ToolResult: []byte(args[2].(string)),
		}
		hydrateHistoryRecordFromStorage(&record)
		return record.Content, nil
	})
}

// ReadHistoryPage reads non-meta transcript records at or after startSeq in
// ascending order. limit bounds database and caller memory use.
func ReadHistoryPage(ctx context.Context, sessDir, id string, startSeq, limit int) (HistoryPage, error) {
	return ReadHistoryQuery(ctx, sessDir, HistoryReadQuery{SessionID: id, StartSeq: startSeq, Limit: limit})
}

func ReadHistoryQuery(ctx context.Context, sessDir string, query HistoryReadQuery) (HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return HistoryPage{}, err
	}
	if query.StartSeq < 1 && query.Cursor == nil {
		return HistoryPage{}, errors.New("history start sequence must be positive")
	}
	if query.Limit < 1 {
		return HistoryPage{}, errors.New("history page limit must be positive")
	}
	if query.EndSeq < 0 || query.SnapshotSeq < 0 {
		return HistoryPage{}, errors.New("history snapshot bounds must be non-negative")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return HistoryPage{}, err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("begin history read: %w", err)
	}
	defer tx.Rollback()
	page, err := readHistoryQueryTx(ctx, tx, query)
	if err != nil {
		return HistoryPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return HistoryPage{}, fmt.Errorf("commit history read: %w", err)
	}
	return page, nil
}

// SearchHistoryPage searches model-visible text and structured tool payloads in
// the current session's physical transcript. Results are newest-first. beforeSeq
// is an exclusive cursor; zero starts from the current head.
func SearchHistoryPage(ctx context.Context, sessDir, id, query string, beforeSeq, limit int) (HistoryPage, error) {
	return SearchHistoryQuery(ctx, sessDir, HistorySearchQuery{SessionID: id, Query: query, BeforeSeq: beforeSeq, Limit: limit})
}

func SearchHistoryQuery(ctx context.Context, sessDir string, query HistorySearchQuery) (HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return HistoryPage{}, err
	}
	query.Query = strings.TrimSpace(query.Query)
	if query.Query == "" {
		return HistoryPage{}, errors.New("history search query is required")
	}
	if query.BeforeSeq < 0 || query.StartSeq < 0 || query.EndSeq < 0 || query.SnapshotSeq < 0 {
		return HistoryPage{}, errors.New("history search cursor must be non-negative")
	}
	if query.Limit < 1 {
		return HistoryPage{}, errors.New("history search limit must be positive")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return HistoryPage{}, err
	}
	defer db.Close()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("begin history search: %w", err)
	}
	defer tx.Rollback()
	page, err := searchHistoryQueryTx(ctx, tx, query)
	if err != nil {
		return HistoryPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return HistoryPage{}, fmt.Errorf("commit history search: %w", err)
	}
	return page, nil
}

func readHistoryQueryTx(ctx context.Context, tx *sql.Tx, query HistoryReadQuery) (HistoryPage, error) {
	id, snapshotSeq, err := resolveHistorySnapshot(ctx, tx, query.SessionID, query.SnapshotSeq, query.Cursor)
	if err != nil {
		return HistoryPage{}, err
	}
	startSeq := query.StartSeq
	if startSeq < 1 {
		startSeq = 1
	}
	endSeq := query.EndSeq
	if endSeq == 0 || endSeq > snapshotSeq {
		endSeq = snapshotSeq
	}
	if query.Cursor != nil {
		if query.Cursor.SessionID != id || query.Cursor.SnapshotSeq != snapshotSeq {
			return HistoryPage{}, errors.New("history cursor does not match this query")
		}
		startSeq = query.Cursor.Seq
	}
	if endSeq < startSeq {
		return HistoryPage{HeadSeq: snapshotSeq, SnapshotSeq: snapshotSeq}, nil
	}
	rows, err := tx.QueryContext(ctx, historyRecordsSelect+`
		WHERE session_id = ? AND lower(role) <> 'meta' AND seq >= ? AND seq <= ?
		ORDER BY seq ASC LIMIT ?`, id, startSeq, endSeq, query.effectiveLimit()+1)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("read session history: %w", err)
	}
	records, err := scanHistoryRecords(rows)
	if err != nil {
		return HistoryPage{}, err
	}
	hasMore := len(records) > query.effectiveLimit()
	if hasMore {
		records = records[:query.effectiveLimit()]
	}
	page := HistoryPage{Records: records, HeadSeq: snapshotSeq, SnapshotSeq: snapshotSeq, HasMore: hasMore}
	if hasMore && len(records) > 0 {
		page.Next = &HistoryCursor{SessionID: id, SnapshotSeq: snapshotSeq, Seq: records[len(records)-1].Seq + 1}
	}
	return page, nil
}

func searchHistoryQueryTx(ctx context.Context, tx *sql.Tx, query HistorySearchQuery) (HistoryPage, error) {
	id, snapshotSeq, err := resolveHistorySnapshot(ctx, tx, query.SessionID, query.SnapshotSeq, query.Cursor)
	if err != nil {
		return HistoryPage{}, err
	}
	startSeq := query.StartSeq
	if startSeq < 1 {
		startSeq = 1
	}
	endSeq := query.EndSeq
	if endSeq == 0 || endSeq > snapshotSeq {
		endSeq = snapshotSeq
	}
	upper := query.BeforeSeq
	if query.Cursor != nil {
		if query.Cursor.SessionID != id || query.Cursor.SnapshotSeq != snapshotSeq {
			return HistoryPage{}, errors.New("history cursor does not match this query")
		}
		upper = query.Cursor.Seq
	}
	if upper <= 0 || upper > endSeq+1 {
		upper = endSeq + 1
	}
	if upper <= startSeq {
		return HistoryPage{HeadSeq: snapshotSeq, SnapshotSeq: snapshotSeq}, nil
	}
	searchable := `lower(
		wuu_history_content(role, content, tool_result_json) || char(10) || coalesce(display_content, '') || char(10) ||
		coalesce(reasoning_content, '') || char(10) || coalesce(tool_calls_json, '') || char(10) ||
		coalesce(tool_result_json, '') || char(10) || coalesce(name, '')
	)`
	rows, err := tx.QueryContext(ctx, historyRecordsSelect+`
		WHERE session_id = ? AND lower(role) <> 'meta' AND seq >= ? AND seq < ? AND seq <= ?
		  AND instr(`+searchable+`, lower(?)) > 0
		ORDER BY seq DESC LIMIT ?`, id, startSeq, upper, endSeq, query.Query, query.effectiveLimit()+1)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return HistoryPage{HeadSeq: snapshotSeq, SnapshotSeq: snapshotSeq, BudgetExhausted: true}, nil
		}
		return HistoryPage{}, fmt.Errorf("search session history: %w", err)
	}
	records, err := scanHistoryRecords(rows)
	if err != nil {
		return HistoryPage{}, err
	}
	hasMore := len(records) > query.effectiveLimit()
	if hasMore {
		records = records[:query.effectiveLimit()]
	}
	page := HistoryPage{Records: records, HeadSeq: snapshotSeq, SnapshotSeq: snapshotSeq, HasMore: hasMore}
	if hasMore && len(records) > 0 {
		page.Next = &HistoryCursor{SessionID: id, SnapshotSeq: snapshotSeq, Seq: records[len(records)-1].Seq}
	}
	return page, nil
}

func resolveHistorySnapshot(ctx context.Context, tx *sql.Tx, id string, snapshotSeq int, cursor *HistoryCursor) (string, int, error) {
	id = strings.TrimSpace(id)
	if cursor != nil {
		if !cursor.valid() {
			return "", 0, errors.New("history cursor is incomplete")
		}
		if id == "" {
			id = cursor.SessionID
		} else if id != cursor.SessionID {
			return "", 0, errors.New("history cursor session does not match")
		}
		if snapshotSeq == 0 {
			snapshotSeq = cursor.SnapshotSeq
		} else if snapshotSeq != cursor.SnapshotSeq {
			return "", 0, errors.New("history cursor snapshot does not match")
		}
	}
	if id == "" {
		return "", 0, errors.New("history session id is required")
	}
	ok, err := sessionExistsTx(tx, id)
	if err != nil {
		return "", 0, err
	}
	if !ok {
		return "", 0, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	var headSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM session_messages WHERE session_id = ?`, id).Scan(&headSeq); err != nil {
		return "", 0, fmt.Errorf("load history head: %w", err)
	}
	if snapshotSeq == 0 {
		snapshotSeq = headSeq
	}
	if snapshotSeq > headSeq {
		return "", 0, fmt.Errorf("%w: requested snapshot %d is beyond head %d", ErrHistorySnapshotGone, snapshotSeq, headSeq)
	}
	return id, snapshotSeq, nil
}
