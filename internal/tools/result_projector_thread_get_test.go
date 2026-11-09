package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func threadProjectionEnvelope(history []map[string]any, beforeSeq int) string {
	records := make([]any, len(history))
	for i := range history {
		records[i] = history[i]
	}
	return mustMarshalMap(map[string]any{
		"thread_id":            "thread-1",
		"session":              map[string]any{"id": "thread-1", "title": "Referenced work", "entries": len(history), "summary": strings.Repeat("duplicate metadata ", 1000)},
		"history":              records,
		"history_record_count": len(history),
		"before_seq":           beforeSeq,
		"snapshot_seq":         len(history),
		"snapshot_token":       "snapshot-token",
	})
}

func projectThreadEnvelope(t *testing.T, history []map[string]any, beforeSeq int) map[string]any {
	t.Helper()
	pc := projectorContext{CallID: "thread-call", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/thread-call.txt"}
	out, _, ok := projectThreadGetResult(threadProjectionEnvelope(history, beforeSeq), pc)
	if !ok {
		t.Fatal("thread_get projector declined")
	}
	if got := estimateResultTokens(out); got > pc.BudgetTokens {
		t.Fatalf("thread projection used %d tokens, budget %d", got, pc.BudgetTokens)
	}
	return parseOut(t, out)
}

func TestProjectThreadGetUsesLatestCompactSummaryAsExclusiveAnchor(t *testing.T) {
	history := []map[string]any{
		{"seq": 1, "role": "user", "content": "ORIGINAL QUERY"},
		{"seq": 2, "role": "assistant", "content": "old answer"},
		{"seq": 3, "role": "system", "content": compact.ConversationSummaryPrefix + "\nolder summary"},
		{"seq": 4, "role": "system", "content": compact.ConversationSummaryPrefix + "\nLATEST SUMMARY"},
		{"seq": 5, "role": "user", "content": "continue"},
		{"seq": 6, "role": "assistant", "content": "latest conclusion"},
	}
	m := projectThreadEnvelope(t, history, 0)
	anchor := m["context_anchor"].(map[string]any)
	if anchor["kind"] != "compact_summary" || !strings.Contains(anchor["content"].(string), "LATEST SUMMARY") {
		t.Fatalf("wrong anchor: %+v", anchor)
	}
	if strings.Contains(mustMarshalMap(m), "ORIGINAL QUERY") {
		t.Fatal("first query leaked despite compact-summary anchor")
	}
}

func TestProjectThreadGetFallsBackToFirstVisibleUserAndFiltersInternals(t *testing.T) {
	history := []map[string]any{
		{"seq": 1, "role": "user", "content": "hidden setup", "hidden": true},
		{"seq": 2, "role": "system", "content": "internal policy"},
		{"seq": 3, "role": "user", "content": "VISIBLE QUERY"},
		{"seq": 4, "role": "assistant", "content": "answer", "reasoning_content": "SECRET REASONING", "reasoning_blocks": []any{map[string]any{"signature": "SECRET SIGNATURE"}}},
		{"seq": 5, "role": "meta", "content": "TOKEN DEBUG"},
	}
	m := projectThreadEnvelope(t, history, 0)
	anchor := m["context_anchor"].(map[string]any)
	if anchor["kind"] != "first_user_message" || anchor["content"] != "VISIBLE QUERY" {
		t.Fatalf("wrong fallback anchor: %+v", anchor)
	}
	encoded := mustMarshalMap(m)
	for _, forbidden := range []string{"hidden setup", "internal policy", "SECRET REASONING", "SECRET SIGNATURE", "TOKEN DEBUG", "duplicate metadata"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("internal content %q leaked", forbidden)
		}
	}
}

func TestProjectThreadGetContinuationReturnsOlderDisjointCompleteTurns(t *testing.T) {
	history := []map[string]any{{"seq": 1, "role": "user", "content": "initial request"}}
	seq := 2
	for turn := 0; turn < 40; turn++ {
		history = append(history,
			map[string]any{"seq": seq, "role": "user", "content": fmt.Sprintf("turn-%02d user %s", turn, strings.Repeat("detail ", 30))},
			map[string]any{"seq": seq + 1, "role": "assistant", "content": fmt.Sprintf("turn-%02d assistant %s", turn, strings.Repeat("result ", 30))},
		)
		seq += 2
	}
	first := projectThreadEnvelope(t, history, 0)
	firstTurns := first["turns"].([]any)
	if len(firstTurns) == 0 || len(firstTurns) >= 40 {
		t.Fatalf("expected bounded first page, got %d turns", len(firstTurns))
	}
	page := first["page"].(map[string]any)
	next := page["next"].(map[string]any)
	beforeSeq := int(next["before_seq"].(float64))
	second := projectThreadEnvelope(t, history, beforeSeq)
	if _, repeatedAnchor := second["context_anchor"]; repeatedAnchor {
		t.Fatal("continuation repeated the context anchor")
	}
	secondTurns := second["turns"].([]any)
	if len(secondTurns) == 0 {
		t.Fatal("continuation returned no older turns")
	}
	seen := map[int]bool{}
	for _, value := range firstTurns {
		turn := value.(map[string]any)
		seen[int(turn["start_seq"].(float64))] = true
	}
	for _, value := range secondTurns {
		turn := value.(map[string]any)
		start := int(turn["start_seq"].(float64))
		if seen[start] || start >= beforeSeq {
			t.Fatalf("continuation repeated or crossed cursor at seq %d (before %d)", start, beforeSeq)
		}
		records := turn["records"].([]any)
		if len(records) != 2 || records[0].(map[string]any)["role"] != "user" || records[1].(map[string]any)["role"] != "assistant" {
			t.Fatalf("incomplete turn: %+v", records)
		}
	}
}

func TestProjectThreadGetPagesCoverEverySemanticRecordExactlyOnce(t *testing.T) {
	history := make([]map[string]any, 0, 62)
	seq := 1
	for turn := 0; turn < 31; turn++ {
		history = append(history,
			map[string]any{"seq": seq, "role": "user", "content": fmt.Sprintf("question %02d %s", turn, strings.Repeat("q", 180))},
			map[string]any{"seq": seq + 1, "role": "assistant", "content": fmt.Sprintf("answer %02d %s", turn, strings.Repeat("a", 180))},
		)
		seq += 2
	}
	seen := map[int]bool{}
	beforeSeq := 0
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page := projectThreadEnvelope(t, history, beforeSeq)
		if pageNumber == 0 {
			anchor := page["context_anchor"].(map[string]any)
			seen[int(anchor["seq"].(float64))] = true
		} else if _, repeated := page["context_anchor"]; repeated {
			t.Fatal("continuation repeated the context anchor")
		}
		for _, value := range page["turns"].([]any) {
			turn := value.(map[string]any)
			for _, recordValue := range turn["records"].([]any) {
				record := recordValue.(map[string]any)
				recordSeq := int(record["seq"].(float64))
				if seen[recordSeq] {
					t.Fatalf("record seq %d was repeated across semantic pages", recordSeq)
				}
				seen[recordSeq] = true
			}
		}
		pageMeta := page["page"].(map[string]any)
		if !pageMeta["has_more"].(bool) {
			break
		}
		next := pageMeta["next"].(map[string]any)
		beforeSeq = int(next["before_seq"].(float64))
	}
	if len(seen) != len(history) {
		t.Fatalf("semantic paging missed records: seen=%d want=%d", len(seen), len(history))
	}
}

func TestProjectThreadGetFitsEscapedAnchorWithinBudget(t *testing.T) {
	raw := threadProjectionEnvelope([]map[string]any{
		{"seq": 1, "role": "system", "content": compact.ConversationSummaryPrefix + "\n" + strings.Repeat("\x01", 10_000)},
		{"seq": 2, "role": "user", "content": "latest question"},
		{"seq": 3, "role": "assistant", "content": "latest answer"},
	}, 0)
	pc := projectorContext{CallID: "escaped-anchor", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/escaped.txt"}
	out, _, ok := projectThreadGetResult(raw, pc)
	if !ok {
		t.Fatal("projector declined escaped anchor")
	}
	if got := estimateResultTokens(out); got > pc.BudgetTokens {
		t.Fatalf("escaped anchor exceeded projection budget: %d > %d", got, pc.BudgetTokens)
	}
	anchor := parseOut(t, out)["context_anchor"].(map[string]any)
	if int(anchor["content_omitted_runes"].(float64)) <= 0 {
		t.Fatalf("escaped anchor did not report semantic clipping: %+v", anchor)
	}
	if len(parseOut(t, out)["turns"].([]any)) != 1 {
		t.Fatal("escaped anchor displaced the latest complete turn")
	}
}

func TestProjectThreadGetClipsOversizedTurnWithoutDroppingRecordBoundaries(t *testing.T) {
	raw := threadProjectionEnvelope([]map[string]any{
		{"seq": 1, "role": "user", "content": strings.Repeat("large question ", 10_000)},
		{"seq": 2, "role": "assistant", "content": strings.Repeat("large answer ", 10_000)},
	}, 0)
	pc := projectorContext{CallID: "large-turn", BudgetTokens: defaultProjectionTokenBudget, ArtifactRef: "/s/large-turn.txt"}
	out, _, ok := projectThreadGetResult(raw, pc)
	if !ok || estimateResultTokens(out) > pc.BudgetTokens {
		t.Fatalf("oversized complete turn was not bounded: ok=%v tokens=%d", ok, estimateResultTokens(out))
	}
	turn := parseOut(t, out)["turns"].([]any)[0].(map[string]any)
	records := turn["records"].([]any)
	if int(turn["anchor_seq"].(float64)) != 1 || len(records) != 1 || records[0].(map[string]any)["role"] != "assistant" {
		t.Fatalf("oversized anchored turn lost semantic boundaries: %+v", turn)
	}
}

func TestProjectThreadGetPreservesFailureAndSummarizesHugeSuccess(t *testing.T) {
	hugeSuccess := mustMarshalMap(map[string]any{
		"action":  "read",
		"path":    "large.log",
		"content": strings.Repeat("successful bulk output\n", defaultProjectionTokenBudget*8),
	})
	failure := mustMarshalMap(map[string]any{"exit_code": 1, "stderr_tail": "FAIL TestProjection: expected anchor, got nil"})
	history := []map[string]any{
		{"seq": 1, "role": "user", "content": "debug"},
		{"seq": 2, "role": "assistant", "tool_calls": []any{map[string]any{"name": "read_file", "arguments": `{"path":"large.log"}`}, map[string]any{"name": "bash", "arguments": `{"command":"go test"}`}}},
		{"seq": 3, "role": "tool", "name": "read_file", "content": hugeSuccess},
		{"seq": 4, "role": "tool", "name": "bash", "tool_result_kind": "error", "content": failure},
	}
	encoded := mustMarshalMap(projectThreadEnvelope(t, history, 0))
	if strings.Contains(encoded, "successful bulk output") {
		t.Fatal("huge successful tool payload was not summarized")
	}
	if !strings.Contains(encoded, "large.log") || !strings.Contains(encoded, "FAIL TestProjection") {
		t.Fatalf("tool summary or failure evidence missing: %s", snip(encoded, 1000))
	}
}

func TestProjectThreadGetFinalizerAlwaysUsesSemanticSchema(t *testing.T) {
	raw := toolresult.FromText(threadProjectionEnvelope([]map[string]any{{"seq": 1, "role": "user", "content": "small"}}, 0))
	got, diag := finalizeBuiltInToolResult(t.TempDir(), threadGetName, "thread-e2e", raw, 0)
	if !diag.Applied || diag.Reason != reasonProjected || !diag.ArtifactWritten {
		t.Fatalf("thread_get finalize diag = %+v", diag)
	}
	if strings.Contains(got.TextProjection(), `"history"`) || !strings.Contains(got.TextProjection(), `"context_anchor"`) {
		t.Fatalf("thread_get did not use semantic schema: %s", got.TextProjection())
	}
}
