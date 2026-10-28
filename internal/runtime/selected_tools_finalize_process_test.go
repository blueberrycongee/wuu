package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// The packed SDK provider adds metadata only after execution has settled. It
// completes calls out of order and invokes both successful and failing children.
// All metadata must stay attached to that call, including normalized rejections.
func TestSelectedToolBatchProcessFinalizationMetadata(t *testing.T) {
	for _, scenario := range []struct{ selectedBatch, streaming bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		t.Run(fmt.Sprintf("selected-batch-%t/streaming-%t", scenario.selectedBatch, scenario.streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			host, kernel := batchProcessHost(t, "finalize")
			executor := newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				return toolservice.Backend{}, errors.New("default toolkit must remain dormant")
			}), selectedToolIdentity{threadID: "finalization", cwd: t.TempDir()})
			// Ordinary hook decoration must not hide the selected provider's result metadata.
			tools := hooks.NewHookedExecutor(executor, hooks.NewDispatcher(hooks.NewRegistry(nil)), "finalization", "")
			calls := []providers.ToolCall{
				{ID: "first", Name: "echo", Arguments: "{}"},
				{ID: "failed", Name: "fail", Arguments: "{}"},
				{ID: "rejected", Name: "reject", Arguments: "{}"},
				{ID: "malformed", Name: "echo", Arguments: "{"},
				{ID: "parent", Name: "orchestrate", Arguments: "{}"},
				{ID: "last", Name: "echo", Arguments: "{}"},
			}
			model := &batchProcessModel{calls: calls}
			dir := t.TempDir()
			store := testSessionStore(t, dir)
			if _, err := store.Create("finalization"); err != nil {
				t.Fatal(err)
			}
			ledger, err := toolledger.New(testSessionDocuments(t, dir), "finalization")
			if err != nil {
				t.Fatal(err)
			}
			runner := &agent.StreamRunner{Client: model, Model: "controlled", Tools: tools, ToolLedger: ledger,
				LoopDriver:             resolveLoopDriver("", host, kernel.driverGateways),
				ModelInputReceiptStore: sessionModelInputReceiptStore{sessions: store, sessionID: "finalization"}}
			if scenario.selectedBatch {
				runner.SelectedToolBatch = newSelectedToolBatch(host)
			}
			if scenario.streaming {
				completed := make(chan struct{})
				runner.Tools = &finalizationObservedTools{HookedExecutor: tools, completed: completed}
				runner.Client = &finalizationStreamModel{batchProcessModel: model, completed: completed}
				runner.StreamingToolExecution = true
			}
			turn := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalization", ExecutionID: "turn"})
			result, err := runner.RunWithCallback(turn, []providers.ChatMessage{{Role: "user", Content: "settle tool metadata"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			toolMessages := 0
			for _, message := range result.NewMessages {
				if message.Role != "tool" {
					continue
				}
				toolMessages++
				want := "finalize_" + message.ToolCallID
				found := false
				for _, definition := range message.DiscoveredTools {
					if definition.Name == want {
						found = true
					} else if definition.Name != "execute_"+message.ToolCallID {
						t.Fatalf("call %s acquired another call's discovery: %+v", message.ToolCallID, message.DiscoveredTools)
					}
				}
				if !found {
					t.Fatalf("call %s lost finalization discovery: %+v", message.ToolCallID, message)
				}
			}
			if toolMessages != len(calls) {
				t.Fatalf("tool result messages=%d want=%d", toolMessages, len(calls))
			}
			records, err := testSessionStore(t, dir).LoadModelInputReceipts("finalization")
			if err != nil || len(records) != 2 {
				t.Fatalf("durable requests=%d err=%v", len(records), err)
			}
			var receipt agent.ModelInputReceipt
			if err := json.Unmarshal(records[1].Payload, &receipt); err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for _, message := range receipt.Messages {
				text.WriteString(message.Content)
			}
			for _, marker := range []string{"first", "last", "parent", "nested", "nested-failed", "failed", "rejected", "malformed"} {
				needle := "finalize-context[" + marker + "]"
				if count := strings.Count(text.String(), needle); count != 1 {
					t.Fatalf("durable next request contains %s %d times: %s", needle, count, text.String())
				}
				if marker != "nested-failed" && marker != "failed" && marker != "rejected" && marker != "malformed" {
					executed := "execute-context[" + marker + "]"
					if strings.Count(text.String(), executed) != 1 || strings.Index(text.String(), executed) > strings.Index(text.String(), needle) {
						t.Fatalf("execution context lost or reordered after finalization for %s: %s", marker, text.String())
					}
				}
			}
			for _, marker := range []string{"nested-failed", "failed", "rejected", "malformed"} {
				if !strings.Contains(text.String(), "finalize-context["+marker+"]:error") {
					t.Fatalf("finalizer did not see normalized %s error: %s", marker, text.String())
				}
			}
			if strings.Index(text.String(), "finalize-context[nested]") > strings.Index(text.String(), "finalize-context[nested-failed]") {
				t.Fatalf("nested registration order was lost: %s", text.String())
			}
			// A fresh turn on the same executor must not inherit a consumed envelope.
			fresh := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalization", ExecutionID: "fresh"})
			if _, err := runner.RunWithCallback(fresh, []providers.ChatMessage{{Role: "user", Content: "fresh turn"}}, nil); err != nil {
				t.Fatal(err)
			}
			records, err = testSessionStore(t, dir).LoadModelInputReceipts("finalization")
			if err != nil || len(records) != 3 || strings.Contains(string(records[2].Payload), "finalize-context[") || strings.Contains(string(records[2].Payload), "execute-context[") {
				t.Fatalf("consumed metadata leaked into later turn: records=%d err=%v", len(records), err)
			}
			t.Log("packed SDK finalize metadata: parallel, nested and normalized failures reconstructed once in durable next-model receipt; fresh turn isolated")
		})
	}
}

// Hold model completion until the real SDK parent and both children have
// finalized. Final-call registration must retain those speculative child runs.
type finalizationObservedTools struct {
	*hooks.HookedExecutor
	completed chan struct{}
}

func (e *finalizationObservedTools) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) agent.ToolFinalization {
	settled := e.HookedExecutor.FinalizeToolOutcome(call, result)
	if call.ID == "parent" {
		close(e.completed)
	}
	return settled
}

type finalizationStreamModel struct {
	*batchProcessModel
	completed <-chan struct{}
}

func (m *finalizationStreamModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	input, err := m.batchProcessModel.StreamChat(ctx, request)
	if err != nil {
		return nil, err
	}
	output := make(chan providers.StreamEvent)
	go func() {
		defer close(output)
		for event := range input {
			if event.Type == providers.EventDone && event.FinishReason == providers.FinishReasonToolCalls {
				select {
				case <-m.completed:
				case <-ctx.Done():
					return
				}
			}
			select {
			case output <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return output, nil
}
