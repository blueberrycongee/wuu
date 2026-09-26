package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"modernc.org/sqlite"
)

func init() {
	// Match the same text readers hydrate, before SQL applies the page limit.
	// Reuse hydration so model_text and multi-part projections cannot drift.
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

const (
	HistoryFieldContent          = "content"
	HistoryFieldDisplayContent   = "display_content"
	HistoryFieldReasoningContent = "reasoning_content"
	HistoryFieldToolCalls        = "tool_calls"
	HistoryFieldToolResult       = "tool_result"
)

var historyReadFields = []string{
	HistoryFieldContent,
	HistoryFieldDisplayContent,
	HistoryFieldReasoningContent,
	HistoryFieldToolCalls,
	HistoryFieldToolResult,
}

// HistoryPage is a bounded, stable view of the physical append-only transcript.
// Records never come from a provider-history checkpoint: Seq always addresses
// session_messages directly.
type HistoryPage struct {
	Records         []HistoryRecord
	HeadSeq         int
	SnapshotSeq     int
	HasMore         bool
	BudgetExhausted bool
	Next            *HistoryCursor
}

// HistoryCursor continues a bounded read or search from the same snapshot.
// Field and Offset resume a truncated record instead of skipping to the next Seq.
type HistoryCursor struct {
	SessionID   string `json:"session_id"`
	SnapshotSeq int    `json:"snapshot_seq"`
	Seq         int    `json:"seq"`
	Field       string `json:"field,omitempty"`
	Offset      int    `json:"offset,omitempty"`
}

func (c HistoryCursor) valid() bool {
	return strings.TrimSpace(c.SessionID) != "" && c.SnapshotSeq >= 0 && c.Seq >= 1 && c.Offset >= 0
}

func EncodeHistoryCursor(cursor HistoryCursor) string {
	parts := []string{
		strings.TrimSpace(cursor.SessionID),
		strconv.Itoa(cursor.SnapshotSeq),
		strconv.Itoa(cursor.Seq),
		strings.TrimSpace(cursor.Field),
		strconv.Itoa(cursor.Offset),
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x1f")))
}

func DecodeHistoryCursor(raw string) (HistoryCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return HistoryCursor{}, errors.New("history cursor is required")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return HistoryCursor{}, fmt.Errorf("decode history cursor: %w", err)
	}
	parts := strings.Split(string(decoded), "\x1f")
	if len(parts) != 5 {
		return HistoryCursor{}, errors.New("history cursor is malformed")
	}
	snapshotSeq, err := strconv.Atoi(parts[1])
	if err != nil {
		return HistoryCursor{}, errors.New("history cursor snapshot is malformed")
	}
	seq, err := strconv.Atoi(parts[2])
	if err != nil {
		return HistoryCursor{}, errors.New("history cursor seq is malformed")
	}
	offset, err := strconv.Atoi(parts[4])
	if err != nil {
		return HistoryCursor{}, errors.New("history cursor offset is malformed")
	}
	cursor := HistoryCursor{
		SessionID:   strings.TrimSpace(parts[0]),
		SnapshotSeq: snapshotSeq,
		Seq:         seq,
		Field:       strings.TrimSpace(parts[3]),
		Offset:      offset,
	}
	if !cursor.valid() {
		return HistoryCursor{}, errors.New("history cursor is incomplete")
	}
	if cursor.Field != "" && !validHistoryField(cursor.Field) {
		return HistoryCursor{}, fmt.Errorf("history cursor field %q is unknown", cursor.Field)
	}
	return cursor, nil
}

// HistoryReadQuery is an explicit, snapshot-bounded physical transcript page.
type HistoryReadQuery struct {
	SessionID   string
	StartSeq    int
	EndSeq      int
	SnapshotSeq int
	Limit       int
	Cursor      *HistoryCursor
}

// HistorySearchQuery is an explicit, snapshot-bounded physical transcript search.
type HistorySearchQuery struct {
	SessionID   string
	Query       string
	StartSeq    int
	EndSeq      int
	SnapshotSeq int
	BeforeSeq   int
	Limit       int
	Cursor      *HistoryCursor
}

func (q HistoryReadQuery) effectiveLimit() int {
	if q.Limit < 1 {
		return 1
	}
	return q.Limit
}

func (q HistorySearchQuery) effectiveLimit() int {
	if q.Limit < 1 {
		return 1
	}
	return q.Limit
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

func validHistoryField(name string) bool {
	for _, field := range historyReadFields {
		if field == name {
			return true
		}
	}
	return false
}

func utf8SafeSlice(value string, start, limit int) (part string, next int, clipped bool) {
	if start < 0 {
		start = 0
	}
	runes := []rune(value)
	if start >= len(runes) {
		return "", start, false
	}
	if limit < 0 {
		limit = 0
	}
	end := start + limit
	if end >= len(runes) {
		return string(runes[start:]), end, false
	}
	return string(runes[start:end]), end, true
}

func utf8RuneCount(value string) int {
	if value == "" {
		return 0
	}
	if utf8.ValidString(value) {
		return utf8.RuneCountInString(value)
	}
	return len([]rune(value))
}
