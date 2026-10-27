package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Public batch failures: duplicate dispatch, malformed arguments, omitted calls,
// per-call cancellation, provider failure, and cancellation after an effect starts
// must not create a second effect or lose the first result.
type batchContractTools struct {
	mu               sync.Mutex
	calls, finalized map[string]int
	entered, release chan struct{}
}

func (e *batchContractTools) Definitions() []providers.ToolDefinition { return nil }
func (e *batchContractTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	e.mu.Lock()
	e.calls[call.ID]++
	e.mu.Unlock()
	if e.entered != nil {
		close(e.entered)
		select {
		case <-e.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if call.Name == "fail" {
		return "", errors.New("individual failure")
	}
	return call.ID, nil
}
func (e *batchContractTools) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.finalized[call.ID]++
	return result
}
func TestBatchContractSettlesEveryCallOnce(t *testing.T) {
	tools := &batchContractTools{calls: map[string]int{}, finalized: map[string]int{}}
	ledger, ledgerErr := toolledger.New(agentTestDocuments(t, t.TempDir()), "batch")
	if ledgerErr != nil {
		t.Fatal(ledgerErr)
	}
	calls := []providers.ToolCall{{ID: "one", Name: "ok", Arguments: "{}"}, {ID: "bad", Name: "ok", Arguments: "{"}, {ID: "failure", Name: "fail", Arguments: "{}"}, {ID: "omitted", Name: "ok", Arguments: "{}"}}
	backend := toolbatch.ExecutorFunc(func(ctx context.Context, req toolbatch.Request, gateway toolbatch.Resources) error {
		req.Calls[0].Arguments = "forged"
		resolved, resolveErr := gateway.Resolve(ctx, 0)
		if resolveErr != nil || resolved.Call.Arguments != "{}" {
			t.Fatalf("batch input authority mutated: %+v %v", resolved, resolveErr)
		}
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := gateway.Execute(ctx, 0); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		observed, _ := gateway.Execute(ctx, 0)
		observed.Result.Content[0].Text = "forged result"
		if _, err := gateway.Settle(ctx, 1, toolresult.FromErrorText("provider rejected malformed arguments")); err != nil {
			t.Error(err)
		}
		if _, err := gateway.Execute(ctx, 2); err != nil {
			t.Error(err)
		}
		return errors.New("batch backend stopped")
	})
	messages, _, err := executeSelectedToolBatch(context.Background(), backend, toolbatch.Request{ID: "batch", Calls: calls}, ToolRuntimeConfig{Executor: tools, Ledger: ledger, OperationID: "batch-op"}, nil)
	if err == nil || len(messages) != 4 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	for i, m := range messages {
		if m.ToolCallID != calls[i].ID || m.ToolResult == nil || m.ToolResult.IsError != (i > 0) {
			t.Fatalf("message %d=%+v", i, m)
		}
	}
	if messages[0].Content != "one" {
		t.Fatal("provider mutated a durable outcome")
	}
	if err := providers.ValidateToolCallHistory(append([]providers.ChatMessage{{Role: "assistant", ToolCalls: calls}}, messages...)); err != nil {
		t.Fatal(err)
	}
	if tools.calls["one"] != 1 || tools.calls["bad"] != 0 || tools.calls["failure"] != 1 || tools.calls["omitted"] != 0 {
		t.Fatalf("effects=%v", tools.calls)
	}
	pending, persistErr := ledger.PendingProjection(context.Background())
	if persistErr != nil || len(pending) != len(calls) {
		t.Fatalf("durable outcomes=%+v err=%v", pending, persistErr)
	}
	for _, c := range calls {
		if tools.finalized[c.ID] != 1 {
			t.Fatalf("finalized=%v", tools.finalized)
		}
	}
}
func TestBatchContractCancellationDrainsAcceptedEffect(t *testing.T) {
	tools := &batchContractTools{calls: map[string]int{}, finalized: map[string]int{}, entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := toolbatch.ExecutorFunc(func(ctx context.Context, req toolbatch.Request, gateway toolbatch.Resources) error {
		done := make(chan struct{})
		go func() { defer close(done); _, _ = gateway.Execute(ctx, 0) }()
		<-tools.entered
		cancel()
		<-done
		return ctx.Err()
	})
	messages, _, err := executeSelectedToolBatch(ctx, backend, toolbatch.Request{ID: "cancel", Calls: []providers.ToolCall{{ID: "one", Name: "ok", Arguments: "{}"}, {ID: "pending", Name: "ok", Arguments: "{}"}}}, ToolRuntimeConfig{Executor: tools}, nil)
	if !errors.Is(err, context.Canceled) || len(messages) != 2 || tools.calls["one"] != 1 || tools.calls["pending"] != 0 || tools.finalized["one"] != 1 || tools.finalized["pending"] != 1 {
		t.Fatalf("messages=%+v calls=%v finalized=%v err=%v", messages, tools.calls, tools.finalized, err)
	}
}

func TestBatchContractAdapterKeepsNestedExecutions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "nested-batch")
	if err != nil {
		t.Fatal(err)
	}
	leaves := 0
	tools := orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			leaves++
			return "nested-result", nil
		}
		nested, ok := toolctx.Nested(ctx)
		if !ok {
			return "", errors.New("nested authority missing")
		}
		result, err := nested.Invoke(ctx, providers.ToolCall{ID: "local", Name: "write", Arguments: "{}"})
		return result.TextProjection(), err
	}}
	calls := []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: "{}"}}
	messages, _, err := executeSelectedToolBatch(ctx, toolbatch.V1Adapter{}, toolbatch.Request{ID: "nested", Calls: calls, MaxParallel: 1}, ToolRuntimeConfig{Executor: tools, Ledger: ledger, OperationID: "nested-operation", Gate: NewToolExecutionGate(1)}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "nested-result" || leaves != 1 {
		t.Fatalf("messages=%+v leaves=%d err=%v", messages, leaves, err)
	}
	pending, err := ledger.PendingProjection(ctx)
	if err != nil || len(pending) != 1 || pending[0].ProviderCallID != "parent" {
		t.Fatalf("nested provider projection=%+v err=%v", pending, err)
	}
}

// ToolCall.Arguments is canonical JSON for custom tools as well as function
// tools. Literal custom input belongs only to the provider transport encoding.
func TestBatchContractMalformedCustomArgumentsCannotExecute(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprintf("provider-rejection-%t", reject), func(t *testing.T) {
			tools := &batchContractTools{calls: map[string]int{}, finalized: map[string]int{}}
			ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "custom")
			if err != nil {
				t.Fatal(err)
			}
			calls := []providers.ToolCall{{ID: "bad-custom", Name: "custom", Kind: providers.ToolCallKindCustom, Arguments: "{"}}
			var backend toolbatch.Executor = toolbatch.V1Adapter{}
			if reject {
				backend = toolbatch.ExecutorFunc(func(ctx context.Context, request toolbatch.Request, resources toolbatch.Resources) error {
					_, err := resources.Settle(ctx, 0, toolresult.FromErrorText("provider rejected"))
					return err
				})
			}
			messages, _, err := executeSelectedToolBatch(context.Background(), backend, toolbatch.Request{ID: "custom-batch", Calls: calls, MaxParallel: 1}, ToolRuntimeConfig{Executor: tools, Ledger: ledger, OperationID: "custom-operation"}, nil)
			if err != nil || len(messages) != 1 || tools.calls["bad-custom"] != 0 || tools.finalized["bad-custom"] != 1 {
				t.Fatalf("malformed custom escaped validation: messages=%+v calls=%v finalized=%v err=%v", messages, tools.calls, tools.finalized, err)
			}
			if !messages[0].ToolResult.IsError || !strings.Contains(messages[0].Content, `"error_kind":"invalid_tool_arguments"`) {
				t.Fatalf("noncanonical malformed-custom outcome: %+v", messages[0])
			}
			if err := providers.ValidateToolCallHistory(append([]providers.ChatMessage{{Role: "assistant", ToolCalls: calls}}, messages...)); err != nil {
				t.Fatal(err)
			}
			pending, err := ledger.PendingProjection(context.Background())
			if err != nil || len(pending) != 1 || pending[0].Result.TextProjection() != messages[0].Content {
				t.Fatalf("durable custom outcome=%+v err=%v", pending, err)
			}
		})
	}
}
func TestBatchContractCanonicalCustomStillStreamsOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := providers.ToolCall{ID: "custom-stream", Name: "read", Kind: providers.ToolCallKindCustom, Arguments: `{"input":"literal input { does not need to be JSON itself"}`}
	started := make(chan struct{})
	effects := 0
	tools := orchestrationTestTools{run: func(ctx context.Context, received providers.ToolCall) (string, error) {
		effects++
		if received.Arguments != call.Arguments || received.Kind != providers.ToolCallKindCustom {
			return "", errors.New("canonical custom encoding changed")
		}
		close(started)
		return "custom-result", nil
	}}
	ledger, err := toolledger.New(agentTestDocuments(t, t.TempDir()), "custom-stream")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools, Ledger: ledger, OperationID: "stream-custom-operation", RunContext: ctx, StreamAdmission: func(ctx context.Context, call providers.ToolCall) (bool, error) {
		return (toolbatch.V1Adapter{}).AdmitStream(ctx, toolbatch.StreamRequest{BatchID: "stream", Call: call, ReadOnly: true, ConcurrencySafe: true})
	}})
	defer runtime.Close()
	for _, kind := range []providers.StreamEventType{providers.EventToolUseStart, providers.EventToolUseEnd} {
		if err := runtime.ObserveStreamEvent(ctx, providers.StreamEvent{Type: kind, ToolCall: &call}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("canonical custom did not start before final batch")
	}
	messages, _, err := executeSelectedToolBatch(ctx, toolbatch.V1Adapter{}, toolbatch.Request{ID: "stream", Calls: []providers.ToolCall{call}, MaxParallel: 1}, ToolRuntimeConfig{Executor: tools}, nil, runtime)
	if err != nil || effects != 1 || len(messages) != 1 || messages[0].Content != "custom-result" {
		t.Fatalf("custom streaming messages=%+v effects=%d err=%v", messages, effects, err)
	}
	if err := providers.ValidateToolCallHistory(append([]providers.ChatMessage{{Role: "assistant", ToolCalls: []providers.ToolCall{call}}}, messages...)); err != nil {
		t.Fatal(err)
	}
}
