package session

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

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
