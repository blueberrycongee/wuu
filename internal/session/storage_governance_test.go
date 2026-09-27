package session

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestBudgetedToolResultSurvivesStorageHydration(t *testing.T) {
	for _, text := range []string{"budgeted projection", ""} {
		t.Run(fmt.Sprintf("bytes_%d", len(text)), func(t *testing.T) {
			dir := t.TempDir()
			if _, err := CreateWithMetadata(dir, "budget-storage", t.TempDir()); err != nil {
				t.Fatal(err)
			}
			result := toolresult.Result{
				Content:           []toolresult.ContentPart{{Type: "text", Text: "original unbounded text"}},
				StructuredContent: json.RawMessage(`{"continuation":{"next":"cursor"}}`),
				Meta:              json.RawMessage(`{"private":"metadata"}`), ModelText: &text, IsError: true,
			}
			payload, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := AppendHistoryRecord(dir, "budget-storage", HistoryRecord{Role: "tool", Content: text, ToolCallID: "call_1", ToolResult: payload}); err != nil {
				t.Fatal(err)
			}
			if _, err := MaintainRedundantStorage(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			history, err := LoadHistoryRecords(dir, "budget-storage", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 || history[0].Content != text {
				t.Fatal("storage hydration restored omitted text")
			}
			var restored toolresult.Result
			if err := json.Unmarshal(history[0].ToolResult, &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored, result) {
				t.Fatal("storage lost projection or recovery data")
			}
			if providers.ProjectToolResult(restored).ToolText != text {
				t.Fatal("replay restored omitted text")
			}
		})
	}
}

func TestToolResultProjectionIsStoredOnceAndHydrated(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-tool-storage", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	result := toolresult.Result{
		Content: []toolresult.ContentPart{{Type: toolresult.ContentTypeText, Text: "projected \"output\"\nC:\\work\\report.txt <end>"}},
		Meta:    json.RawMessage(`{"source":"test"}`),
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	record := HistoryRecord{Role: "tool", Content: result.TextProjection(), ToolCallID: "call-1", ToolResult: payload}
	if err := AppendHistoryRecord(dir, "thread-tool-storage", record); err != nil {
		t.Fatal(err)
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var storedContent string
	if err := db.QueryRow(`SELECT content FROM session_messages WHERE session_id = ?`, "thread-tool-storage").Scan(&storedContent); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if storedContent != "" {
		t.Fatalf("redundant tool content was persisted: %q", storedContent)
	}
	history, err := LoadHistoryRecords(dir, "thread-tool-storage", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != record.Content || !reflect.DeepEqual(history[0].ToolResult, record.ToolResult) {
		t.Fatalf("hydrated history = %+v, want %+v", history, record)
	}
	page, err := SearchHistoryPage(context.Background(), dir, "thread-tool-storage", record.Content, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 || page.Records[0].Content != record.Content {
		t.Fatalf("deduplicated tool content was not searchable: %+v", page)
	}
}

func TestMaintainRedundantStoragePreservesAuthoritativeCopies(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-maintenance", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	toolResult := toolresult.FromText("legacy projected output")
	payload, err := json.Marshal(toolResult)
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-maintenance", HistoryRecord{
		Role: "tool", Content: toolResult.TextProjection(), ToolCallID: "call-1", ToolResult: payload,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`UPDATE session_messages SET content = 'legacy projected output' WHERE session_id = 'thread-maintenance'`,
		`INSERT INTO tool_batches (id, owner_id, operation_id, step_index, status, created_at, updated_at, terminal_at)
VALUES ('batch-maintenance', 'thread-maintenance', 'operation-1', 1, 'projected', 1, 1, 1)`,
		`INSERT INTO tool_invocations (id, batch_id, provider_call_id, tool_name, arguments_json, replay_policy, state, result_json, prepared_at, running_at, settled_at, projected_at)
VALUES ('invocation-maintenance', 'batch-maintenance', 'call-1', 'read_file', '{}', 'at_most_once', 'succeeded', '{"content":[{"type":"text","text":"legacy projected output"}]}', 1, 1, 1, 1)`,
		`INSERT INTO session_history_checkpoints (session_id, version, kind, through_seq, replacement_json, created_at)
VALUES ('thread-maintenance', 1, 'old', 1, '[]', '2026-01-01T00:00:00Z')`,
		`INSERT INTO session_history_checkpoints (session_id, version, kind, through_seq, replacement_json, created_at)
VALUES ('thread-maintenance', 2, 'current', 1, '[]', '2026-01-02T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	maintenance, err := MaintainRedundantStorage(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.ProjectedToolResultsCleared != 1 || maintenance.SupersededCheckpointsDeleted != 1 || maintenance.ToolMessageContentsCompacted != 1 {
		t.Fatalf("maintenance result = %+v", maintenance)
	}
	db, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var invocationResult, messageContent string
	var checkpointCount, checkpointVersion int
	if err := db.QueryRow(`SELECT result_json FROM tool_invocations WHERE id = 'invocation-maintenance'`).Scan(&invocationResult); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT content FROM session_messages WHERE session_id = 'thread-maintenance'`).Scan(&messageContent); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*), MAX(version) FROM session_history_checkpoints WHERE session_id = 'thread-maintenance'`).Scan(&checkpointCount, &checkpointVersion); err != nil {
		t.Fatal(err)
	}
	if invocationResult != "" || messageContent != "" || checkpointCount != 1 || checkpointVersion != 2 {
		t.Fatalf("maintained storage = invocation %q, message %q, checkpoints %d@%d", invocationResult, messageContent, checkpointCount, checkpointVersion)
	}
	history, err := loadHistoryRecordsDB(db, "thread-maintenance", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "legacy projected output" {
		t.Fatalf("maintenance changed observable history: %+v", history)
	}
}

func TestHistoryProjectionAtomicallyClearsLedgerRecoveryPayload(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-ledger-handoff", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	result := toolresult.FromText("settled output")
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO tool_batches (id, owner_id, operation_id, step_index, status, created_at, updated_at, terminal_at)
VALUES ('batch-handoff', 'thread-ledger-handoff', 'operation-1', 1, 'settled', 1, 1, 1)`,
		`INSERT INTO tool_invocations (id, batch_id, provider_call_id, tool_name, arguments_json, replay_policy, state, result_json, prepared_at, running_at, settled_at)
VALUES ('invocation-handoff', 'batch-handoff', 'call-1', 'read_file', '{}', 'at_most_once', 'succeeded', '{"content":[{"type":"text","text":"settled output"}]}', 1, 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-ledger-handoff", HistoryRecord{
		Role: "tool", Content: result.TextProjection(), ToolCallID: "call-1", ToolInvocationID: "invocation-handoff", ToolResult: payload,
	}); err != nil {
		t.Fatal(err)
	}
	db, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var storedResult, storedContent string
	var projectedAt int64
	if err := db.QueryRow(`SELECT result_json, projected_at FROM tool_invocations WHERE id = 'invocation-handoff'`).Scan(&storedResult, &projectedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT content FROM session_messages WHERE session_id = 'thread-ledger-handoff'`).Scan(&storedContent); err != nil {
		t.Fatal(err)
	}
	if storedResult != "" || storedContent != "" || projectedAt == 0 {
		t.Fatalf("atomic handoff retained duplicate data: result=%q content=%q projected_at=%d", storedResult, storedContent, projectedAt)
	}
}

func TestConsecutiveHistoryCheckpointsRetainOnlyCurrentPayload(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-checkpoints", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := AppendHistoryRecord(dir, "thread-checkpoints", HistoryRecord{Role: "user", Content: "request"}); err != nil {
		t.Fatal(err)
	}
	first, err := StoreHistoryCheckpointAtBaseline(dir, "thread-checkpoints", "automatic", []HistoryRecord{{Seq: 1, Role: "user", Content: "first"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := StoreHistoryCheckpointAtBaseline(dir, "thread-checkpoints", "manual", []HistoryRecord{{Seq: 1, Role: "user", Content: "second"}}, first.ThroughSeq)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != 2 {
		t.Fatalf("second checkpoint version = %d", second.Version)
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count, version int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(version) FROM session_history_checkpoints WHERE session_id = ?`, "thread-checkpoints").Scan(&count, &version); err != nil {
		t.Fatal(err)
	}
	if count != 1 || version != second.Version {
		t.Fatalf("retained checkpoints = %d@%d, want 1@%d", count, version, second.Version)
	}
}
