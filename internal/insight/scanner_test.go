package insight

import (
	"encoding/json"
	"testing"
	"time"

	sessionstore "github.com/blueberrycongee/wuu/internal/session"
)

func TestCollectUsageScanReturnsProviderAndModel(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, time.April, 17, 9, 0, 0, 0, time.UTC)

	writeInsightSessionRecords(t, dir, "sess-anthropic", []memoryRecord{
		{Role: "user", Content: "implement feature a", At: start},
		{Role: "assistant", Content: "ok", At: start.Add(time.Minute)},
		{Role: "meta", Content: "token_usage", Provider: "anthropic", Model: "claude-sonnet-4-6", InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 80, CacheReadTokens: 20, At: start.Add(2 * time.Minute)},
		{Role: "meta", Content: "token_usage", Provider: "anthropic", Model: "claude-sonnet-4-6", InputTokens: 200, OutputTokens: 80, CacheCreationTokens: 100, CacheReadTokens: 50, At: start.Add(3 * time.Minute)},
	})
	writeInsightSessionRecords(t, dir, "sess-openai", []memoryRecord{
		{Role: "user", Content: "implement feature b", At: start},
		{Role: "assistant", Content: "ok", At: start.Add(time.Minute)},
		{Role: "meta", Content: "token_usage", Provider: "openai", Model: "gpt-4o", InputTokens: 500, OutputTokens: 200, CacheReadTokens: 100, At: start.Add(2 * time.Minute)},
	})
	writeInsightSessionRecords(t, dir, "sess-mixed", []memoryRecord{
		{Role: "user", Content: "legacy session", At: start},
		{Role: "assistant", Content: "ok", At: start.Add(time.Minute)},
		{Role: "meta", Content: "token_usage", InputTokens: 50, OutputTokens: 10, At: start.Add(2 * time.Minute)},
	})

	scan, err := CollectUsageScan(dir)
	if err != nil {
		t.Fatalf("CollectUsageScan: %v", err)
	}
	if len(scan.TokenRows) != 4 {
		t.Fatalf("expected 4 token rows, got %d (%+v)", len(scan.TokenRows), scan.TokenRows)
	}
	if len(scan.Skills) != 0 {
		t.Fatalf("expected no skill usage, got %+v", scan.Skills)
	}

	bySession := make(map[string][]TokenUsageRow, 3)
	for _, row := range scan.TokenRows {
		bySession[row.SessionID] = append(bySession[row.SessionID], row)
	}
	anthropicRows := bySession["sess-anthropic"]
	if len(anthropicRows) != 2 {
		t.Fatalf("anthropic session: expected 2 rows, got %+v", anthropicRows)
	}
	for _, row := range anthropicRows {
		if row.Provider != "anthropic" || row.Model != "claude-sonnet-4-6" {
			t.Fatalf("anthropic session: unexpected row %+v", row)
		}
	}
	if anthropicRows[0].InputTokens != 100 || anthropicRows[1].OutputTokens != 80 || anthropicRows[1].CacheReadTokens != 50 {
		t.Fatalf("anthropic session: unexpected row values %+v", anthropicRows)
	}
	openAIRows := bySession["sess-openai"]
	if len(openAIRows) != 1 || openAIRows[0].Provider != "openai" || openAIRows[0].Model != "gpt-4o" || openAIRows[0].InputTokens != 500 {
		t.Fatalf("openai session: unexpected rows %+v", openAIRows)
	}
	legacyRows := bySession["sess-mixed"]
	if len(legacyRows) != 1 || legacyRows[0].Provider != "" || legacyRows[0].Model != "" || legacyRows[0].InputTokens != 50 {
		t.Fatalf("legacy session: unexpected rows %+v", legacyRows)
	}
}

func TestCollectUsageScanCountsSkillCallsAcrossPersistedShapes(t *testing.T) {
	dir := t.TempDir()
	sessionID := "usage-skills"
	if _, err := sessionstore.CreateWithMetadata(dir, sessionID, ""); err != nil {
		t.Fatal(err)
	}
	writeInsightSessionRecords(t, dir, sessionID, []memoryRecord{
		{
			Role: "assistant",
			ToolCalls: []toolCallRec{
				{ID: "call-review", Name: "functions.load_skill", Arguments: `{"name":"review"}`},
			},
		},
		{
			Role:       "tool",
			Name:       "load_skill",
			ToolCallID: "call-review",
			Content:    `{"metadata":{"name":"review"}}`,
		},
		{
			// Provider checkpoints can retain the tool result without the
			// assistant call that originally carried its arguments.
			Role:       "tool",
			Name:       "functions/load_skill",
			ToolCallID: "call-commit",
			Content:    `{"metadata":{"name":"commit"}}`,
		},
		{
			Role:       "tool",
			Name:       "load_skill",
			ToolCallID: "call-malformed",
			Content:    `not-json`,
		},
	})

	scan, err := CollectUsageScan(dir)
	if err != nil {
		t.Fatalf("CollectUsageScan: %v", err)
	}
	if len(scan.Skills) != 2 {
		t.Fatalf("expected two skill buckets, got %+v", scan.Skills)
	}
	if scan.Skills[0] != (SkillUsage{Name: "commit", Count: 1}) ||
		scan.Skills[1] != (SkillUsage{Name: "review", Count: 1}) {
		t.Fatalf("unexpected skill usage: %+v", scan.Skills)
	}
}

type memoryRecord struct {
	Role                string        `json:"role"`
	Content             string        `json:"content"`
	At                  time.Time     `json:"at"`
	ToolCalls           []toolCallRec `json:"tool_calls,omitempty"`
	ToolCallID          string        `json:"tool_call_id,omitempty"`
	Name                string        `json:"name,omitempty"`
	InputTokens         int           `json:"input_tokens,omitempty"`
	OutputTokens        int           `json:"output_tokens,omitempty"`
	CacheCreationTokens int           `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int           `json:"cache_read_tokens,omitempty"`
	Provider            string        `json:"provider,omitempty"`
	Model               string        `json:"model,omitempty"`
}

func writeInsightSessionRecords(t *testing.T, sessDir, id string, records []memoryRecord) {
	t.Helper()

	if _, exists, err := sessionstore.Find(sessDir, id); err != nil {
		t.Fatalf("find session: %v", err)
	} else if !exists {
		if _, err := sessionstore.CreateWithMetadata(sessDir, id, ""); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}
	history := make([]sessionstore.HistoryRecord, 0, len(records))
	for _, rec := range records {
		history = append(history, historyRecordFromMemoryRecord(t, rec))
	}
	if err := sessionstore.RewriteHistoryRecords(sessDir, id, history); err != nil {
		t.Fatalf("rewrite session records: %v", err)
	}
}

func historyRecordFromMemoryRecord(t *testing.T, rec memoryRecord) sessionstore.HistoryRecord {
	t.Helper()
	toolCalls, err := json.Marshal(rec.ToolCalls)
	if err != nil {
		t.Fatalf("marshal tool calls: %v", err)
	}
	return sessionstore.HistoryRecord{
		Role:                rec.Role,
		Content:             rec.Content,
		ToolCalls:           toolCalls,
		ToolCallID:          rec.ToolCallID,
		Name:                rec.Name,
		At:                  rec.At,
		InputTokens:         rec.InputTokens,
		OutputTokens:        rec.OutputTokens,
		CacheCreationTokens: rec.CacheCreationTokens,
		CacheReadTokens:     rec.CacheReadTokens,
		Provider:            rec.Provider,
		Model:               rec.Model,
	}
}
