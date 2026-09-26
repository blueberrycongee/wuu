package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestHistorySearchFindsPersistedToolTextLiterals(t *testing.T) {
	text := "fmt.Println(\"hello\")\nC:\\work\\report.txt\nx < y && y > z\ncolumn\tvalue\nline\u2028separator"
	for _, tc := range []struct {
		name   string
		result toolresult.Result
	}{
		{name: "text", result: toolresult.FromText(text)},
		{name: "model_text", result: toolresult.Result{
			Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "producer output"}}, ModelText: &text,
		}},
		{name: "multiple_parts", result: toolresult.Result{Content: []toolresult.ContentPart{
			{Type: toolresult.ContentTypeText, Text: `fmt.Println("hello")`},
			{Type: toolresult.ContentTypeText, Text: "C:\\work\\report.txt\nx < y && y > z\ncolumn\tvalue\nline\u2028separator"},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := session.CreateWithMetadata(dir, "literal-search", t.TempDir()); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.AppendHistoryRecord(dir, "literal-search", session.HistoryRecord{
				Role: "tool", Content: text, ToolResult: payload,
			}); err != nil {
				t.Fatal(err)
			}
			env := &Env{SessionsDir: dir, SessionID: "literal-search"}
			read, err := NewHistoryReadTool(env).Execute(context.Background(), `{"start_seq":1}`)
			if err != nil {
				t.Fatal(err)
			}
			var readResult struct {
				Records []historyRecordToolView `json:"records"`
			}
			if err := json.Unmarshal([]byte(read), &readResult); err != nil {
				t.Fatal(err)
			}
			if len(readResult.Records) != 1 || readResult.Records[0].Content != text {
				t.Fatalf("history_read did not restore original text: %s", read)
			}
			for _, query := range []string{
				"hello", `fmt.Println("hello")`, `FMT.PRINTLN("HELLO")`, `C:\work\report.txt`,
				"x < y && y > z", "hello\")\nC:", "column\tvalue", "line\u2028separator",
			} {
				t.Run(query, func(t *testing.T) {
					args, err := json.Marshal(map[string]any{"query": query})
					if err != nil {
						t.Fatal(err)
					}
					search, err := NewHistorySearchTool(env).Execute(context.Background(), string(args))
					if err != nil {
						t.Fatal(err)
					}
					var result struct {
						SnapshotSeq     int  `json:"snapshot_seq"`
						BudgetExhausted bool `json:"budget_exhausted"`
						Matches         []struct {
							Seq     int    `json:"seq"`
							Excerpt string `json:"excerpt"`
						} `json:"matches"`
					}
					if err := json.Unmarshal([]byte(search), &result); err != nil {
						t.Fatal(err)
					}
					if result.SnapshotSeq != 1 || result.BudgetExhausted || len(result.Matches) != 1 ||
						result.Matches[0].Seq != 1 || !strings.Contains(strings.ToLower(result.Matches[0].Excerpt), strings.ToLower(query)) {
						t.Fatalf("literal %q was readable but not found: %s", query, search)
					}
				})
			}
		})
	}
}

func TestHistorySearchToolTextKeepsSnapshotPaginationAndBudget(t *testing.T) {
	dir := t.TempDir()
	if _, err := session.CreateWithMetadata(dir, "paged-search", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	const query = `value < "bound"`
	payload, err := json.Marshal(toolresult.FromText(query))
	if err != nil {
		t.Fatal(err)
	}
	record := session.HistoryRecord{Role: "tool", Content: query, ToolResult: payload}
	if err := session.AppendHistoryRecords(dir, "paged-search", []session.HistoryRecord{
		record, {Role: "user", Content: "unrelated"}, record, {Role: "meta", Content: query},
	}); err != nil {
		t.Fatal(err)
	}
	type searchResult struct {
		SnapshotSeq     int  `json:"snapshot_seq"`
		BudgetExhausted bool `json:"budget_exhausted"`
		Matches         []struct {
			Seq     int    `json:"seq"`
			Excerpt string `json:"excerpt"`
		} `json:"matches"`
		Next *struct {
			Cursor string `json:"cursor"`
		} `json:"next"`
	}
	search := func(args map[string]any) searchResult {
		t.Helper()
		args["query"] = query
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		output, err := NewHistorySearchTool(&Env{SessionsDir: dir, SessionID: "paged-search"}).Execute(context.Background(), string(raw))
		if err != nil {
			t.Fatal(err)
		}
		var result searchResult
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := search(map[string]any{"limit": 1})
	if first.SnapshotSeq != 4 || first.BudgetExhausted || len(first.Matches) != 1 || first.Matches[0].Seq != 3 || first.Next == nil {
		t.Fatalf("first page = %+v", first)
	}
	if err := session.AppendHistoryRecord(dir, "paged-search", record); err != nil {
		t.Fatal(err)
	}
	continued := search(map[string]any{"cursor": first.Next.Cursor, "limit": 1})
	if continued.SnapshotSeq != 4 || continued.BudgetExhausted || len(continued.Matches) != 1 || continued.Matches[0].Seq != 1 || continued.Next != nil {
		t.Fatalf("continued page = %+v", continued)
	}
	budgeted := search(map[string]any{"snapshot_seq": 4, "max_chars": 5})
	if !budgeted.BudgetExhausted || len(budgeted.Matches) != 1 || budgeted.Matches[0].Seq != 3 || budgeted.Next == nil {
		t.Fatalf("budgeted page = %+v", budgeted)
	}
	continued = search(map[string]any{"cursor": budgeted.Next.Cursor})
	if continued.SnapshotSeq != 4 || continued.BudgetExhausted || len(continued.Matches) != 1 || continued.Matches[0].Seq != 1 || continued.Next != nil {
		t.Fatalf("budget continuation = %+v", continued)
	}
	bounded := search(map[string]any{"start_seq": 2, "end_seq": 3, "before_seq": 4})
	if len(bounded.Matches) != 1 || bounded.Matches[0].Seq != 3 || bounded.Next != nil {
		t.Fatalf("bounded page = %+v", bounded)
	}
	missing := search(map[string]any{"start_seq": 2, "before_seq": 3})
	if len(missing.Matches) != 0 || missing.Next != nil || missing.BudgetExhausted {
		t.Fatalf("empty range = %+v", missing)
	}
}

func TestHistoryToolsAreBoundToCurrentSession(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"current", "other"} {
		if _, err := session.CreateWithMetadata(dir, id, t.TempDir()); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := session.AppendHistoryRecords(dir, "current", []session.HistoryRecord{
		{Role: "user", Content: "find the regression"},
		{Role: "assistant", Content: "fixed current session"},
	}); err != nil {
		t.Fatalf("append current: %v", err)
	}
	if err := session.AppendHistoryRecord(dir, "other", session.HistoryRecord{Role: "user", Content: "private other session"}); err != nil {
		t.Fatalf("append other: %v", err)
	}
	env := &Env{SessionsDir: dir, SessionID: "current"}

	read, err := NewHistoryReadTool(env).Execute(context.Background(), `{"start_seq":1}`)
	if err != nil {
		t.Fatalf("history_read: %v", err)
	}
	var readResult struct {
		SessionID string                  `json:"session_id"`
		Records   []historyRecordToolView `json:"records"`
	}
	if err := json.Unmarshal([]byte(read), &readResult); err != nil {
		t.Fatalf("decode read result: %v", err)
	}
	if readResult.SessionID != "current" || len(readResult.Records) != 2 {
		t.Fatalf("read result = %+v", readResult)
	}

	search, err := NewHistorySearchTool(env).Execute(context.Background(), `{"query":"fixed current"}`)
	if err != nil {
		t.Fatalf("history_search: %v", err)
	}
	var searchResult struct {
		Matches []struct {
			Seq int `json:"seq"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(search), &searchResult); err != nil {
		t.Fatalf("decode search result: %v", err)
	}
	if len(searchResult.Matches) != 1 || searchResult.Matches[0].Seq != 2 {
		t.Fatalf("search result = %+v", searchResult)
	}

	search, err = NewHistorySearchTool(env).Execute(context.Background(), `{"query":"private other session"}`)
	if err != nil {
		t.Fatalf("cross-session search: %v", err)
	}
	if err := json.Unmarshal([]byte(search), &searchResult); err != nil {
		t.Fatalf("decode cross-session search: %v", err)
	}
	if len(searchResult.Matches) != 0 {
		t.Fatalf("history search escaped current session: %+v", searchResult)
	}
}

func TestHistoryReadReportsPayloadTruncationAndContinuation(t *testing.T) {
	dir := t.TempDir()
	if _, err := session.CreateWithMetadata(dir, "current", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecords(dir, "current", []session.HistoryRecord{
		{Role: "user", Content: "1234567890"},
		{Role: "assistant", Content: "later"},
	}); err != nil {
		t.Fatalf("append history: %v", err)
	}
	tool := NewHistoryReadTool(&Env{SessionsDir: dir, SessionID: "current"})
	result, err := tool.Execute(context.Background(), `{"start_seq":1,"max_chars":5}`)
	if err != nil {
		t.Fatalf("history_read: %v", err)
	}
	var decoded struct {
		PayloadTruncated bool                    `json:"payload_truncated"`
		Records          []historyRecordToolView `json:"records"`
		Next             map[string]any          `json:"next"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !decoded.PayloadTruncated || len(decoded.Records) != 1 || decoded.Records[0].Content != "12345" || decoded.Next["seq"] != float64(1) {
		t.Fatalf("result = %+v", decoded)
	}
	cursor, _ := decoded.Next["cursor"].(string)
	continued, err := tool.Execute(context.Background(), fmt.Sprintf(`{"cursor":%q,"max_chars":5}`, cursor))
	if err != nil {
		t.Fatalf("continue history_read: %v", err)
	}
	if err := json.Unmarshal([]byte(continued), &decoded); err != nil {
		t.Fatalf("decode continued: %v", err)
	}
	if decoded.Records[0].Content != "67890" {
		t.Fatalf("continued = %+v", decoded)
	}
}

func TestHistoryReadCanTargetAnotherSession(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"current", "source"} {
		if _, err := session.CreateWithMetadata(dir, id, t.TempDir()); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	if err := session.AppendHistoryRecord(dir, "source", session.HistoryRecord{Role: "user", Content: "source fact"}); err != nil {
		t.Fatalf("append source: %v", err)
	}
	result, err := NewHistoryReadTool(&Env{SessionsDir: dir, SessionID: "current"}).Execute(context.Background(), `{"session_id":"source","start_seq":1}`)
	if err != nil {
		t.Fatalf("history_read: %v", err)
	}
	var decoded struct {
		SessionID string                  `json:"session_id"`
		Records   []historyRecordToolView `json:"records"`
	}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.SessionID != "source" || len(decoded.Records) != 1 || decoded.Records[0].Content != "source fact" {
		t.Fatalf("result = %+v", decoded)
	}
}

func TestContextWindowToolsRequireRuntimeEnablement(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("new toolkit: %v", err)
	}
	kit.SetActiveProfile(modelprofile.Resolve("openai", "gpt-5-codex"), true)
	assertDefined := func(want bool) {
		t.Helper()
		got := map[string]bool{}
		for _, definition := range kit.Definitions() {
			got[definition.Name] = true
		}
		for _, name := range contextWindowToolNames {
			if got[name] != want {
				t.Fatalf("tool %q visible = %v, want %v", name, got[name], want)
			}
		}
	}
	assertDefined(false)
	kit.SetContextWindowToolsEnabled(true)
	assertDefined(true)
	kit.SetContextWindowToolsEnabled(false)
	assertDefined(false)
	got := map[string]bool{}
	for _, definition := range kit.Definitions() {
		got[definition.Name] = true
	}
	for _, name := range historyRecoveryToolNames {
		if !got[name] {
			t.Fatalf("history recovery tool %q should remain visible without note compaction", name)
		}
	}
}
