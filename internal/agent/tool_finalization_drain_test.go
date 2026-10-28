package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// The parent returns after its child is admitted. The child physically settles
// only when the runtime owner drains, so the test never relies on scheduling.
type drainFinalizationTools struct {
	runtime     *TurnToolRuntime
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func (e *drainFinalizationTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "parent"}}
}
func (e *drainFinalizationTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{Orchestrator: call.Name == "parent", ConcurrencySafe: true, ReadOnly: true}, true
}
func (e *drainFinalizationTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if call.Name == "parent" {
		nested, _ := toolctx.Nested(ctx)
		go func() { _, _ = nested.Invoke(ctx, providers.ToolCall{ID: "child", Name: "child", Arguments: `{}`}) }()
		select {
		case <-e.entered:
			return "parent result", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	e.runtime.mu.Lock()
	run := e.runtime.byID[call.ID]
	cancel := e.runtime.running[run]
	e.runtime.running[run] = func() { cancel(); e.releaseOnce.Do(func() { close(e.release) }) }
	e.runtime.mu.Unlock()
	close(e.entered)
	<-e.release
	return "child result", nil
}
func (e *drainFinalizationTools) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) ToolFinalization {
	outcome := ToolFinalization{Result: result}
	if call.Name == "child" {
		outcome.AdditionalContext = "late-child-finalization-evidence"
	}
	return outcome
}

func TestToolBatchOwnersDrainBeforeContextProjection(t *testing.T) {
	for _, mode := range []string{"gateway", "loop"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tools := &drainFinalizationTools{entered: make(chan struct{}), release: make(chan struct{})}
			runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools, RunContext: ctx})
			tools.runtime = runtime
			t.Cleanup(runtime.Close)
			calls := []providers.ToolCall{{ID: "parent-call", Name: "parent", Arguments: `{}`}}
			var projected any
			if mode == "gateway" {
				gateway := newStreamTurnGateway(ctx, &StreamRunner{Client: &mockStreamClient{}, Model: "test", Tools: tools}, loopdriver.PersistedInput{}, nil)
				defer gateway.close()
				gateway.streamedBatches = map[string]driverToolStream{"batch": {runtime: runtime, calls: calls}}
				if _, err := gateway.CallTools(ctx, loopdriver.ToolBatch{ID: "batch", Calls: calls}); err != nil {
					t.Fatal(err)
				}
				segments, err := gateway.ReadRequestContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				projected = assembleModelRequest(nil, segments).Messages
				if b, _ := json.Marshal(gateway.produced); strings.Contains(string(b), "late-child-finalization-evidence") {
					t.Fatal("request-only context leaked to durable transcript")
				}
			} else {
				step := &fakeStep{results: []StepResult{{ToolCalls: calls, ToolRuntime: runtime}, {Content: "done", FinishReason: providers.FinishReasonStop}}}
				result, err := RunToolLoop(ctx, nil, LoopConfig{Model: "test", Tools: tools}, step)
				if err != nil {
					t.Fatal(err)
				}
				if len(step.calls) != 2 {
					t.Fatalf("requests=%d", len(step.calls))
				}
				projected = step.calls[1].Messages
				if b, _ := json.Marshal(result.NewMessages); strings.Contains(string(b), "late-child-finalization-evidence") {
					t.Fatal("request-only context leaked to durable transcript")
				}
			}
			b, _ := json.Marshal(projected)
			if strings.Count(string(b), "late-child-finalization-evidence") != 1 {
				t.Fatalf("late admitted child metadata missing/duplicated: %s", b)
			}
			if len(runtime.TakeRequestContextSegments()) != 0 {
				t.Fatal("child metadata remained after projection")
			}
		})
	}
}
