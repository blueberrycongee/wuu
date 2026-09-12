package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type unavailableNativeLineageClient struct {
	*mockStreamClient
	probes    []providers.InferenceOperation
	attempts  []providers.InferenceAttempt
	available bool
}

func (c *unavailableNativeLineageClient) NativeCompactionAvailable() bool { return c.available }

func (c *unavailableNativeLineageClient) NativeCompact(ctx context.Context, req providers.ChatRequest) (providers.NativeCompactionResult, error) {
	c.probes = append(c.probes, req.Operation)
	c.attempts = append(c.attempts, req.Attempt)
	if c.available {
		return providers.NativeCompactionResult{Replacement: []providers.ChatMessage{{Role: "user", Content: "Older work summarized; continue."}}}, nil
	}
	return providers.NativeCompactionResult{}, providers.ErrNativeCompactionUnavailable
}

// Record only operations accepted by the real SQLite journal, whose parent
// validation rejects dangling identities and cross-workflow edges.
type lineageRecordingJournal struct {
	providers.InferenceJournal
	operations []providers.InferenceOperation
}

func (j *lineageRecordingJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	if err := j.InferenceJournal.PrepareOperation(record); err != nil {
		return err
	}
	j.operations = append(j.operations, record.Operation)
	return nil
}

func TestProactiveCompactionNativeUnavailablePreservesDurableLineage(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) { testProactiveCompactionDurableLineage(t, false) })
	t.Run("available", func(t *testing.T) { testProactiveCompactionDurableLineage(t, true) })
}

func testProactiveCompactionDurableLineage(t *testing.T, nativeAvailable bool) {
	runtime, err := session.NewInferenceJournalRuntime(t.TempDir(), "named-agent-workspace")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	journal := &lineageRecordingJournal{InferenceJournal: runtime.ForOwner("collab-session-lineage")}
	ctx := providers.WithInferenceJournal(context.Background(), journal)
	client := &unavailableNativeLineageClient{available: nativeAvailable, mockStreamClient: &mockStreamClient{
		chatResponses: []providers.ChatResponse{{Content: "Older work summarized."}},
		attempts: []mockStreamAttempt{
			{events: []providers.StreamEvent{
				{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "c1", Name: "t"}},
				{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: "c1", Name: "t", Arguments: `{}`}},
				{Type: providers.EventDone, Usage: &providers.TokenUsage{InputTokens: 130000}},
			}},
			{events: []providers.StreamEvent{
				{Type: providers.EventContentDelta, Content: "done"},
				{Type: providers.EventDone},
			}},
		},
	}}
	var history []providers.ChatMessage
	for i := 0; i < 6; i++ {
		history = append(history, providers.ChatMessage{Role: "user", Content: strings.Repeat("older request ", 100)}, providers.ChatMessage{Role: "assistant", Content: strings.Repeat("older answer ", 100)})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: "Continue the work."})
	var attempts []CompactAttemptInfo
	result, err := RunToolLoop(ctx, history, LoopConfig{
		Model: "m", MaxContextTokens: 100000, DefaultMaxTokens: 1000,
		Tools: &fakeLoopTools{defs: []providers.ToolDefinition{{Name: "t"}}},
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return compact.CompactWithNativeOrSummary(ctx, messages, client, "m", compact.Budget{ContextTokens: 100000, KeepRecentTokens: 100}, compact.NativeOptions{})
		},
		OnCompactAttempt: func(info CompactAttemptInfo) { attempts = append(attempts, info) },
	}, NewStreamStep(client))
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "done" || !result.HistoryRewritten || len(attempts) != 1 || attempts[0].Status != CompactAttemptSucceeded {
		t.Fatalf("content=%q rewritten=%v attempts=%+v", result.Content, result.HistoryRewritten, attempts)
	}
	wantProbes := 0
	if nativeAvailable {
		wantProbes = 1
	}
	if len(client.probes) != wantProbes || len(journal.operations) != 3 {
		t.Fatalf("native probes=%d want=%d persisted operations=%+v", len(client.probes), wantProbes, journal.operations)
	}
	root, summary, continuation := journal.operations[0], journal.operations[1], journal.operations[2]
	if summary.ParentOperationID != root.ID || continuation.ParentOperationID != summary.ID || root.WorkflowID != summary.WorkflowID || summary.WorkflowID != continuation.WorkflowID {
		t.Fatalf("broken persisted lineage: %+v", journal.operations)
	}
	if nativeAvailable {
		if len(client.attempts) != 1 || !client.attempts[0].Valid() {
			t.Fatalf("native request was not bound to an inference attempt: %+v", client.attempts)
		}
		if summary.ID != client.probes[0].ID {
			t.Fatalf("prepared native operation is not the continuation parent: %+v", journal.operations)
		}
		return
	}
	if len(client.attempts) != 0 {
		t.Fatalf("disabled native compaction prepared attempts: %+v", client.attempts)
	}
}
