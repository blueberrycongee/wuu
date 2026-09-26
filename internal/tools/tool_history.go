package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const (
	historyReadToolName       = "history_read"
	historySearchToolName     = "history_search"
	defaultHistoryReadLimit   = 20
	maximumHistoryReadLimit   = 50
	defaultHistoryReadChars   = 12_000
	maximumHistoryReadChars   = 40_000
	defaultHistorySearchLimit = 10
	maximumHistorySearchLimit = 25
	historySearchExcerptChars = 800
	maximumHistoryEnvelope    = 96 * 1024
)

type HistoryReadTool struct{ env *Env }

func NewHistoryReadTool(env *Env) *HistoryReadTool { return &HistoryReadTool{env: env} }
func (*HistoryReadTool) Name() string              { return historyReadToolName }
func (*HistoryReadTool) IsReadOnly() bool          { return true }
func (*HistoryReadTool) IsConcurrencySafe() bool   { return true }

func (*HistoryReadTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: historyReadToolName,
		Description: "Read a bounded range from an append-only original conversation and tool history by stable Seq address. " +
			"Omit session_id to read this session. Cross-session reads require an explicit session_id and may include snapshot_seq. " +
			"Use cursor to continue the same truncated record. Results never invent missing text.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"session_id":   map[string]any{"type": "string", "description": "Session to read. Omit to read the current session."},
				"start_seq":    map[string]any{"type": "integer", "minimum": 1, "description": "Inclusive stable Seq at which to start. Required unless cursor is provided."},
				"end_seq":      map[string]any{"type": "integer", "minimum": 1, "description": "Inclusive stable Seq at which to stop. Must be at or before snapshot_seq."},
				"snapshot_seq": map[string]any{"type": "integer", "minimum": 1, "description": "Fixed original-history cutoff. Omit on first read to capture the current head."},
				"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": maximumHistoryReadLimit, "description": "Maximum records to return (default 20)."},
				"max_chars":    map[string]any{"type": "integer", "minimum": 1, "maximum": maximumHistoryReadChars, "description": "Maximum model-visible payload characters (default 12000)."},
				"cursor":       map[string]any{"type": "string", "description": "Opaque continuation from a previous truncated page. Continues the same record when a field was clipped."},
			},
		},
	}
}

func (t *HistoryReadTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		StartSeq    int    `json:"start_seq"`
		EndSeq      int    `json:"end_seq"`
		SnapshotSeq int    `json:"snapshot_seq"`
		Limit       int    `json:"limit"`
		MaxChars    int    `json:"max_chars"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	cursor, err := decodeOptionalHistoryCursor(args.Cursor)
	if err != nil {
		return "", err
	}
	if args.StartSeq < 1 && cursor == nil {
		return "", errors.New("history_read requires a positive start_seq")
	}
	if args.EndSeq < 0 || args.SnapshotSeq < 0 {
		return "", errors.New("history_read snapshot bounds must be non-negative")
	}
	limit, err := boundedPositive(args.Limit, defaultHistoryReadLimit, maximumHistoryReadLimit, "history_read limit")
	if err != nil {
		return "", err
	}
	maxChars, err := boundedPositive(args.MaxChars, defaultHistoryReadChars, maximumHistoryReadChars, "history_read max_chars")
	if err != nil {
		return "", err
	}
	sessDir, sessionID, err := resolveHistorySession(t.env, historyReadToolName, args.SessionID, cursor)
	if err != nil {
		return "", err
	}
	page, err := session.ReadHistoryQuery(ctx, sessDir, session.HistoryReadQuery{
		SessionID: sessionID, StartSeq: args.StartSeq, EndSeq: args.EndSeq, SnapshotSeq: args.SnapshotSeq, Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return "", historyToolError(historyReadToolName, err)
	}
	views, next, payloadTruncated := boundedHistoryViews(page.Records, sessionID, page.SnapshotSeq, cursor, maxChars)
	if next == nil && page.Next != nil {
		next = page.Next
	}
	result := map[string]any{
		"action":            historyReadToolName,
		"session_id":        sessionID,
		"head_seq":          page.HeadSeq,
		"snapshot_seq":      page.SnapshotSeq,
		"records":           views,
		"payload_truncated": payloadTruncated,
	}
	if next != nil {
		result["next"] = historyNextEnvelope(*next)
	}
	return mustBoundedJSON(result)
}

type HistorySearchTool struct{ env *Env }

func NewHistorySearchTool(env *Env) *HistorySearchTool { return &HistorySearchTool{env: env} }
func (*HistorySearchTool) Name() string                { return historySearchToolName }
func (*HistorySearchTool) IsReadOnly() bool            { return true }
func (*HistorySearchTool) IsConcurrencySafe() bool     { return true }

func (*HistorySearchTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: historySearchToolName,
		Description: "Search an append-only original conversation and tool history. Omit session_id to search this session. " +
			"Returns bounded newest-first matches with stable Seq addresses; use history_read for surrounding exact records. " +
			"Search excerpts are locators, not complete originals.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"query"},
			"properties": map[string]any{
				"session_id":   map[string]any{"type": "string", "description": "Session to search. Omit to search the current session."},
				"query":        map[string]any{"type": "string", "description": "Case-insensitive literal text to find in messages, reasoning, tool calls, or tool results."},
				"start_seq":    map[string]any{"type": "integer", "minimum": 1, "description": "Inclusive lower Seq bound."},
				"end_seq":      map[string]any{"type": "integer", "minimum": 1, "description": "Inclusive upper Seq bound. Must be at or before snapshot_seq."},
				"snapshot_seq": map[string]any{"type": "integer", "minimum": 1, "description": "Fixed original-history cutoff. Omit on first search to capture the current head."},
				"before_seq":   map[string]any{"type": "integer", "minimum": 0, "description": "Exclusive older-page cursor. Use next.before_seq from the previous result."},
				"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": maximumHistorySearchLimit, "description": "Maximum matches to return (default 10)."},
				"max_chars":    map[string]any{"type": "integer", "minimum": 1, "maximum": maximumHistoryReadChars, "description": "Maximum model-visible excerpt characters across the page."},
				"cursor":       map[string]any{"type": "string", "description": "Opaque continuation from a previous page. Keeps the same snapshot."},
			},
		},
	}
}

func (t *HistorySearchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SessionID   string `json:"session_id"`
		Query       string `json:"query"`
		StartSeq    int    `json:"start_seq"`
		EndSeq      int    `json:"end_seq"`
		SnapshotSeq int    `json:"snapshot_seq"`
		BeforeSeq   int    `json:"before_seq"`
		Limit       int    `json:"limit"`
		MaxChars    int    `json:"max_chars"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return "", errors.New("history_search requires query")
	}
	if args.BeforeSeq < 0 || args.StartSeq < 0 || args.EndSeq < 0 || args.SnapshotSeq < 0 {
		return "", errors.New("history_search snapshot bounds must be non-negative")
	}
	cursor, err := decodeOptionalHistoryCursor(args.Cursor)
	if err != nil {
		return "", err
	}
	limit, err := boundedPositive(args.Limit, defaultHistorySearchLimit, maximumHistorySearchLimit, "history_search limit")
	if err != nil {
		return "", err
	}
	maxChars, err := boundedPositive(args.MaxChars, defaultHistoryReadChars, maximumHistoryReadChars, "history_search max_chars")
	if err != nil {
		return "", err
	}
	sessDir, sessionID, err := resolveHistorySession(t.env, historySearchToolName, args.SessionID, cursor)
	if err != nil {
		return "", err
	}
	page, err := session.SearchHistoryQuery(ctx, sessDir, session.HistorySearchQuery{
		SessionID: sessionID, Query: args.Query, StartSeq: args.StartSeq, EndSeq: args.EndSeq,
		SnapshotSeq: args.SnapshotSeq, BeforeSeq: args.BeforeSeq, Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return "", historyToolError(historySearchToolName, err)
	}
	remaining := maxChars
	matches := make([]map[string]any, 0, len(page.Records))
	for _, record := range page.Records {
		if remaining <= 0 {
			page.HasMore = true
			page.Next = &session.HistoryCursor{SessionID: sessionID, SnapshotSeq: page.SnapshotSeq, Seq: record.Seq + 1}
			page.BudgetExhausted = true
			break
		}
		excerptLimit := historySearchExcerptChars
		if excerptLimit > remaining {
			excerptLimit = remaining
		}
		excerpt := historySearchExcerpt(historyRecordSearchText(record), args.Query, excerptLimit)
		remaining -= len([]rune(excerpt))
		matches = append(matches, map[string]any{
			"seq":     record.Seq,
			"role":    record.Role,
			"name":    record.Name,
			"excerpt": excerpt,
		})
	}
	result := map[string]any{
		"action":           historySearchToolName,
		"session_id":       sessionID,
		"head_seq":         page.HeadSeq,
		"snapshot_seq":     page.SnapshotSeq,
		"query":            args.Query,
		"matches":          matches,
		"budget_exhausted": page.BudgetExhausted,
	}
	if page.HasMore && page.Next != nil {
		result["next"] = historyNextEnvelope(*page.Next)
	} else if page.HasMore && len(page.Records) > 0 {
		result["next"] = historyNextEnvelope(session.HistoryCursor{
			SessionID: sessionID, SnapshotSeq: page.SnapshotSeq, Seq: page.Records[len(page.Records)-1].Seq,
		})
	}
	return mustBoundedJSON(result)
}

type historyRecordToolView struct {
	Seq              int    `json:"seq"`
	Role             string `json:"role"`
	Name             string `json:"name,omitempty"`
	Content          string `json:"content,omitempty"`
	DisplayContent   string `json:"display_content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	ToolCalls        string `json:"tool_calls,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	ToolResult       string `json:"tool_result,omitempty"`
	FinishReason     string `json:"finish_reason,omitempty"`
	Truncated        bool   `json:"truncated,omitempty"`
	PayloadTruncated bool   `json:"payload_truncated,omitempty"`
	Fragment         bool   `json:"fragment,omitempty"`
	StoredTruncated  bool   `json:"stored_truncated,omitempty"`
}

func boundedHistoryViews(records []session.HistoryRecord, sessionID string, snapshotSeq int, cursor *session.HistoryCursor, maxChars int) ([]historyRecordToolView, *session.HistoryCursor, bool) {
	views := make([]historyRecordToolView, 0, len(records))
	remaining := maxChars
	startField := ""
	startOffset := 0
	if cursor != nil {
		startField = strings.TrimSpace(cursor.Field)
		startOffset = cursor.Offset
	}
	for recordIndex, record := range records {
		if remaining <= 0 {
			next := &session.HistoryCursor{SessionID: sessionID, SnapshotSeq: snapshotSeq, Seq: record.Seq}
			return views, next, true
		}
		view := historyRecordToolView{
			Seq: record.Seq, Role: record.Role, Name: record.Name, ToolCallID: record.ToolCallID,
			FinishReason: record.FinishReason, Truncated: record.Truncated, StoredTruncated: record.Truncated,
		}
		skipUntilField := startField != "" && recordIndex == 0
		for _, field := range []struct {
			name   string
			source string
			target *string
		}{
			{session.HistoryFieldContent, record.Content, &view.Content},
			{session.HistoryFieldDisplayContent, record.DisplayContent, &view.DisplayContent},
			{session.HistoryFieldReasoningContent, record.ReasoningContent, &view.ReasoningContent},
			{session.HistoryFieldToolCalls, string(record.ToolCalls), &view.ToolCalls},
			{session.HistoryFieldToolResult, string(record.ToolResult), &view.ToolResult},
		} {
			if skipUntilField {
				if field.name != startField {
					continue
				}
				skipUntilField = false
			} else {
				startOffset = 0
			}
			part, nextOffset, clipped := takeHistoryField(field.source, startOffset, remaining)
			*field.target = part
			remaining -= len([]rune(part))
			if clipped {
				view.PayloadTruncated = true
				if field.name == session.HistoryFieldToolCalls || field.name == session.HistoryFieldToolResult {
					view.Fragment = true
				}
				next := &session.HistoryCursor{SessionID: sessionID, SnapshotSeq: snapshotSeq, Seq: record.Seq, Field: field.name, Offset: nextOffset}
				views = append(views, view)
				return views, next, true
			}
			startOffset = 0
		}
		views = append(views, view)
		startField = ""
		startOffset = 0
	}
	return views, nil, false
}

func historyNextEnvelope(cursor session.HistoryCursor) map[string]any {
	next := map[string]any{
		"cursor":       session.EncodeHistoryCursor(cursor),
		"session_id":   cursor.SessionID,
		"snapshot_seq": cursor.SnapshotSeq,
		"seq":          cursor.Seq,
	}
	if cursor.Field == "" {
		next["start_seq"] = cursor.Seq
		next["before_seq"] = cursor.Seq
	} else {
		next["field"] = cursor.Field
		next["offset"] = cursor.Offset
	}
	return next
}

func decodeOptionalHistoryCursor(raw string) (*session.HistoryCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	cursor, err := session.DecodeHistoryCursor(raw)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func resolveHistorySession(env *Env, toolName, requestedID string, cursor *session.HistoryCursor) (string, string, error) {
	sessDir, currentID, err := currentHistorySession(env, toolName)
	if err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(requestedID)
	if id == "" && cursor != nil {
		id = cursor.SessionID
	}
	if id == "" {
		id = currentID
	}
	if cursor != nil && cursor.SessionID != id {
		return "", "", fmt.Errorf("%s: cursor session does not match session_id", toolName)
	}
	return sessDir, id, nil
}

func currentHistorySession(env *Env, toolName string) (string, string, error) {
	if env == nil || strings.TrimSpace(env.SessionID) == "" {
		return "", "", fmt.Errorf("%s: current session is unavailable", toolName)
	}
	sessDir := strings.TrimSpace(env.SessionsDir)
	if sessDir == "" {
		home, err := statepath.Home("")
		if err != nil {
			return "", "", fmt.Errorf("%s: resolve wuu home: %w", toolName, err)
		}
		sessDir = statepath.SessionsDir(home)
	}
	if sessDir == "" {
		return "", "", fmt.Errorf("%s: sessions dir is empty", toolName)
	}
	return sessDir, strings.TrimSpace(env.SessionID), nil
}

func boundedPositive(value, defaultValue, maximum int, name string) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maximum {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return value, nil
}

func takeHistoryChars(value string, limit int) (string, bool) {
	part, _, clipped := takeHistoryField(value, 0, limit)
	return part, clipped
}

func takeHistoryField(value string, start, limit int) (string, int, bool) {
	if start < 0 {
		start = 0
	}
	runes := []rune(value)
	if start > len(runes) {
		start = len(runes)
	}
	if limit <= 0 {
		return "", start, start < len(runes)
	}
	end := start + limit
	if end >= len(runes) {
		return string(runes[start:]), end, false
	}
	return string(runes[start:end]), end, true
}

func historyRecordSearchText(record session.HistoryRecord) string {
	return strings.Join([]string{
		record.Content, record.DisplayContent, record.ReasoningContent, string(record.ToolCalls), string(record.ToolResult), record.Name,
	}, "\n")
}

func historySearchExcerpt(text, query string, limit int) string {
	textRunes := []rune(strings.TrimSpace(text))
	if len(textRunes) <= limit {
		return string(textRunes)
	}
	lowerText := []rune(strings.ToLower(string(textRunes)))
	lowerQuery := []rune(strings.ToLower(query))
	match := 0
	for index := 0; index+len(lowerQuery) <= len(lowerText); index++ {
		if string(lowerText[index:index+len(lowerQuery)]) == string(lowerQuery) {
			match = index
			break
		}
	}
	start := match - limit/3
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > len(textRunes) {
		end = len(textRunes)
		start = max(0, end-limit)
	}
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(textRunes) {
		suffix = "…"
	}
	return prefix + string(textRunes[start:end]) + suffix
}

func historyToolError(toolName string, err error) error {
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		return fmt.Errorf("%s: %w", toolName, session.ErrHistoryUnavailable)
	case errors.Is(err, session.ErrHistoryUnavailable), errors.Is(err, session.ErrHistorySnapshotGone):
		return fmt.Errorf("%s: %w", toolName, err)
	default:
		return fmt.Errorf("%s: %w", toolName, err)
	}
}

func mustBoundedJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(raw) > maximumHistoryEnvelope {
		return "", fmt.Errorf("history payload exceeds %d bytes; request a smaller limit or continue from cursor", maximumHistoryEnvelope)
	}
	return string(raw), nil
}
