package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type runtimeTestTools struct {
	mu         sync.Mutex
	metadata   map[string]ToolMetadata
	results    map[string]string
	delays     map[string]time.Duration
	discovered map[string][]providers.LoadableToolDefinition
	calls      []providers.ToolCall
	steps      []int
}

type cancelAwareRuntimeTools struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once

	mu    sync.Mutex
	calls []providers.ToolCall
}

type richRuntimeTools struct {
	runtimeTestTools
	result toolresult.Result
}

func (f *richRuntimeTools) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	_, _ = f.runtimeTestTools.Execute(ctx, call)
	return f.result.Clone(), nil
}

func TestTurnToolRuntimePreservesRichResultAndErrorState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tools := &richRuntimeTools{result: toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: "text", Text: "observed"},
			{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"},
		},
		StructuredContent: []byte(`{"url":"https://example.test"}`),
		Meta:              []byte(`{"private":true}`),
		IsError:           true,
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})
	var callback toolresult.Result
	runtime.SetResultCallback(func(_ providers.ToolCall, result toolresult.Result) {
		callback = result.Clone()
	})

	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "call-1", Name: "browser_observe"}}, nil)
	if len(msgs) != 1 || msgs[0].ToolResult == nil {
		t.Fatalf("rich tool message missing: %+v", msgs)
	}
	if !msgs[0].ToolResult.IsError || len(msgs[0].ToolResult.Content) != 2 || len(msgs[0].ToolResult.Meta) == 0 {
		t.Fatalf("rich tool result flattened: %+v", msgs[0].ToolResult)
	}
	if msgs[0].Content != msgs[0].ToolResult.TextProjection() {
		t.Fatalf("compatibility content = %q, projection = %q", msgs[0].Content, msgs[0].ToolResult.TextProjection())
	}
	if callback.JSONProjection() != msgs[0].ToolResult.JSONProjection() {
		t.Fatalf("callback result mismatch: %s != %s", callback.JSONProjection(), msgs[0].ToolResult.JSONProjection())
	}
}

func (f *runtimeTestTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{
		{Name: "read_file"},
		{Name: "run_shell"},
	}
}

func (f *runtimeTestTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	meta, ok := f.metadata[call.Name]
	return meta, ok
}

func (f *runtimeTestTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	if stepIndex, ok := toolctx.StepIndex(ctx); ok {
		f.steps = append(f.steps, stepIndex)
	}
	delay := f.delays[call.ID]
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results != nil {
		if result, ok := f.results[call.ID]; ok {
			return result, nil
		}
	}
	return `{"ok":"` + call.ID + `"}`, nil
}

func (f *runtimeTestTools) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discovered[call.ID]
}

func (f *runtimeTestTools) recordedCalls() []providers.ToolCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]providers.ToolCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *runtimeTestTools) recordedSteps() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.steps))
	copy(out, f.steps)
	return out
}

func (f *cancelAwareRuntimeTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "read_file"}}
}

func (f *cancelAwareRuntimeTools) ToolMetadata(_ providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true
}

func (f *cancelAwareRuntimeTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if call.ID != "orphan" {
		return `{"ok":"` + call.ID + `"}`, nil
	}
	f.once.Do(func() { close(f.started) })
	select {
	case <-ctx.Done():
		close(f.canceled)
		return "", ctx.Err()
	case <-time.After(time.Second):
		return "", context.DeadlineExceeded
	}
}

func (f *cancelAwareRuntimeTools) recordedCalls() []providers.ToolCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]providers.ToolCall, len(f.calls))
	copy(out, f.calls)
	return out
}

type panickingTool struct{}

func (panickingTool) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "boom"}}
}

func (panickingTool) Execute(context.Context, providers.ToolCall) (string, error) {
	panic("kaboom")
}

type fileWritingTool struct {
	path string
}

func (t fileWritingTool) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "write_file"}}
}

func (t fileWritingTool) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", os.WriteFile(t.path, []byte("partial output"), 0o600)
}

func TestTurnToolRuntimeRecoversFromToolPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: panickingTool{}})
	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{
		ID:   "call_1",
		Name: "boom",
	}}, nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 tool message after panic, got %+v", msgs)
	}
	if msgs[0].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool call id: %q", msgs[0].ToolCallID)
	}
	if !strings.Contains(msgs[0].Content, "tool panicked") || !strings.Contains(msgs[0].Content, "kaboom") {
		t.Fatalf("panic not surfaced in tool result: %q", msgs[0].Content)
	}
	if msgs[0].ToolResult == nil || !msgs[0].ToolResult.IsError {
		t.Fatalf("panic result should be marked as error: %+v", msgs[0].ToolResult)
	}
}

func TestTurnToolRuntimeRejectsTruncatedWriteFileBeforeSideEffects(t *testing.T) {
	target := t.TempDir() + "/page.html"
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: fileWritingTool{path: target}})

	msgs, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{
		ID:        "call_write",
		Name:      "write_file",
		Arguments: `{"path":"page.html","content":"truncated`,
	}}, nil)
	if err != nil {
		t.Fatalf("ExecuteFinalCalls: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("truncated write_file modified target: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, `"error_kind":"invalid_tool_arguments"`) {
		t.Fatalf("expected structured invalid arguments result, got %+v", msgs)
	}
}

func TestTurnToolRuntime_PreservesToolSearchKindOnResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &runtimeTestTools{
		results: map[string]string{"search_1": `{"loadable_tools":[]}`},
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})

	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{
		ID:        "search_1",
		Name:      "tool_search",
		Kind:      providers.ToolCallKindToolSearch,
		Arguments: `{"query":"docs"}`,
	}}, nil)

	calls := tools.recordedCalls()
	if len(calls) != 1 || calls[0].Kind != providers.ToolCallKindToolSearch {
		t.Fatalf("tool call kind not preserved into execution: %+v", calls)
	}
	if len(msgs) != 1 || msgs[0].ToolResultKind != providers.ToolCallKindToolSearch {
		t.Fatalf("tool result kind not preserved: %+v", msgs)
	}
}

func TestTurnToolRuntimeAnnotatesToolExecutionStep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &runtimeTestTools{}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})
	runtime.SetStepIndex(3)

	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{
		ID:   "call_1",
		Name: "read_file",
	}}, nil)
	if len(msgs) != 1 || msgs[0].ToolCallID != "call_1" {
		t.Fatalf("unexpected tool result messages: %+v", msgs)
	}
	if steps := tools.recordedSteps(); len(steps) != 1 || steps[0] != 3 {
		t.Fatalf("tool execution steps = %+v, want [3]", steps)
	}
}

func TestTurnToolRuntimeAttachesDiscoveredToolsToToolResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &runtimeTestTools{
		discovered: map[string][]providers.LoadableToolDefinition{
			"spawn_1": {{
				Type:        "function",
				Name:        "await_agents",
				Description: "Wait for subagents",
				InputSchema: map[string]any{"type": "object"},
			}},
		},
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})

	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{
		ID:   "spawn_1",
		Name: "spawn_agent",
	}}, nil)

	if len(msgs) != 1 {
		t.Fatalf("expected 1 tool message, got %+v", msgs)
	}
	if got := msgs[0].DiscoveredTools; len(got) != 1 || got[0].Name != "await_agents" {
		t.Fatalf("unexpected discovered tools on tool result: %+v", got)
	}
	msgs[0].DiscoveredTools[0].Name = "mutated"
	if tools.discovered["spawn_1"][0].Name != "await_agents" {
		t.Fatalf("tool result discovered tools should be cloned before attaching")
	}
}

func TestTurnToolRuntime_ReusesStreamingStartedConcurrentRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &runtimeTestTools{
		metadata: map[string]ToolMetadata{
			"read_file": {ReadOnly: true, ConcurrencySafe: true},
		},
		results: map[string]string{
			"call_1": `{"cached":1}`,
			"call_2": `{"cached":2}`,
		},
		delays: map[string]time.Duration{
			"call_1": 20 * time.Millisecond,
			"call_2": 20 * time.Millisecond,
		},
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})
	runtime.SetStepIndex(5)

	for _, call := range []providers.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "call_2", Name: "read_file", Arguments: `{"path":"b.go"}`},
	} {
		runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
			Type:     providers.EventToolUseStart,
			ToolCall: &providers.ToolCall{ID: call.ID, Name: call.Name},
		})
		runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
			Type:     providers.EventToolUseEnd,
			ToolCall: &call,
		})
	}

	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "call_2", Name: "read_file", Arguments: `{"path":"b.go"}`},
	}, nil)

	calls := tools.recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("expected each streamed call to execute once, got %d calls: %+v", len(calls), calls)
	}
	if steps := tools.recordedSteps(); len(steps) != 2 || steps[0] != 5 || steps[1] != 5 {
		t.Fatalf("streamed tool execution steps = %+v, want [5 5]", steps)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 tool messages, got %d", len(msgs))
	}
	if msgs[0].ToolCallID != "call_1" || msgs[0].Content != `{"cached":1}` {
		t.Fatalf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].ToolCallID != "call_2" || msgs[1].Content != `{"cached":2}` {
		t.Fatalf("unexpected second message: %+v", msgs[1])
	}
}

func TestTurnToolRuntime_DoesNotStreamStartAcrossWriteBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &runtimeTestTools{
		metadata: map[string]ToolMetadata{
			"run_shell": {ReadOnly: false, ConcurrencySafe: false},
			"read_file": {ReadOnly: true, ConcurrencySafe: true},
		},
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})

	for _, call := range []providers.ToolCall{
		{ID: "write_first", Name: "run_shell", Arguments: `{"command":"touch x"}`},
		{ID: "read_second", Name: "read_file", Arguments: `{"path":"x"}`},
	} {
		runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
			Type:     providers.EventToolUseStart,
			ToolCall: &providers.ToolCall{ID: call.ID, Name: call.Name},
		})
		runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
			Type:     providers.EventToolUseEnd,
			ToolCall: &call,
		})
	}

	if calls := tools.recordedCalls(); len(calls) != 0 {
		t.Fatalf("expected no streaming-started calls across write barrier, got %+v", calls)
	}

	_, _ = runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{
		{ID: "write_first", Name: "run_shell", Arguments: `{"command":"touch x"}`},
		{ID: "read_second", Name: "read_file", Arguments: `{"path":"x"}`},
	}, nil)

	calls := tools.recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("expected final execution of both calls, got %+v", calls)
	}
	if calls[0].ID != "write_first" || calls[1].ID != "read_second" {
		t.Fatalf("expected final call order to respect write barrier, got %+v", calls)
	}
}

func TestTurnToolRuntime_StreamingErrorDoesNotExecuteAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &cancelAwareRuntimeTools{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})
	runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
		Type:     providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{ID: "orphan", Name: "read_file"},
	})
	runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
		Type: providers.EventToolUseEnd,
		ToolCall: &providers.ToolCall{
			ID:        "orphan",
			Name:      "read_file",
			Arguments: `{"path":"a.go"}`,
		},
	})
	select {
	case <-tools.started:
	case <-time.After(time.Second):
		t.Fatal("expected streaming execution to start")
	}
	runtime.Cancel()

	msgs, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{
		{ID: "orphan", Name: "read_file", Arguments: `{"path":"a.go"}`},
	}, nil)
	if err != nil || len(msgs) != 1 || !strings.Contains(msgs[0].Content, "context canceled") {
		t.Fatalf("expected one settled cancellation result, got messages=%+v error=%v", msgs, err)
	}
	if calls := tools.recordedCalls(); len(calls) != 1 {
		t.Fatalf("streaming execution count = %d, want 1: %+v", len(calls), calls)
	}
	select {
	case <-tools.canceled:
	default:
		t.Fatal("expected streaming execution to observe cancellation")
	}
}

func TestTurnToolRuntime_CancelsStreamingRunsMissingFromFinalCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	tools := &cancelAwareRuntimeTools{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})
	runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
		Type:     providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{ID: "orphan", Name: "read_file"},
	})
	runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
		Type: providers.EventToolUseEnd,
		ToolCall: &providers.ToolCall{
			ID:        "orphan",
			Name:      "read_file",
			Arguments: `{"path":"orphan.go"}`,
		},
	})

	select {
	case <-tools.started:
	case <-time.After(time.Second):
		t.Fatal("expected orphan streaming run to start")
	}

	msgs, _ := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{
		{ID: "kept", Name: "read_file", Arguments: `{"path":"kept.go"}`},
	}, nil)
	if len(msgs) != 1 || msgs[0].ToolCallID != "kept" {
		t.Fatalf("expected only final kept result, got %+v", msgs)
	}
	select {
	case <-tools.canceled:
	case <-time.After(time.Second):
		t.Fatal("expected missing streaming run to be canceled")
	}

	runtime.mu.Lock()
	_, stillRegistered := runtime.byID["orphan"]
	runtime.mu.Unlock()
	if stillRegistered {
		t.Fatal("expected missing streaming run to be dropped from runtime registry")
	}
}

func TestTurnToolRuntimeDurablySettlesBeforeReturningResult(t *testing.T) {
	ctx := context.Background()
	tools := &runtimeTestTools{results: map[string]string{"call-1": "done"}}
	ledger, err := toolledger.New(t.TempDir(), "thread-durable")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{
		Executor: tools, Ledger: ledger, OperationID: "operation-1", StepIndex: 3,
	})
	msgs, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "call-1", Name: "write_file", Arguments: `{}`}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "done" || msgs[0].ToolInvocationID == "" {
		t.Fatalf("durable tool message = %+v", msgs)
	}
	pending, err := ledger.PendingProjection(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != msgs[0].ToolInvocationID {
		t.Fatalf("pending invocation = %+v", pending)
	}
	if calls := tools.recordedCalls(); len(calls) != 1 {
		t.Fatalf("tool executions = %+v", calls)
	}
}

type finalizingRuntimeTools struct {
	runtimeTestTools
	err error
}

func (f *finalizingRuntimeTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	text, _ := f.runtimeTestTools.Execute(ctx, call)
	return text, f.err
}

func (*finalizingRuntimeTools) FinalizeToolResult(_ providers.ToolCall, result toolresult.Result) toolresult.Result {
	text := "settled: " + result.TextProjection()
	result.ModelText = &text
	return result
}

func TestTurnToolRuntimeFinalizesBeforeDurableSettlement(t *testing.T) {
	for _, failed := range []bool{false, true} {
		ctx := context.Background()
		executor := &finalizingRuntimeTools{runtimeTestTools: runtimeTestTools{results: map[string]string{"call-1": "producer"}}}
		if failed {
			executor.err = errors.New("execution failed")
		}
		dir := t.TempDir()
		ledger, err := toolledger.New(dir, "owner")
		if err != nil {
			t.Fatal(err)
		}
		runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "turn"})
		var observed toolresult.Result
		runtime.SetResultCallback(func(_ providers.ToolCall, result toolresult.Result) { observed = result })
		messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{}`}}, nil)
		if err != nil || len(messages) != 1 {
			t.Fatalf("execute: %v messages=%v", err, messages)
		}
		// Reopening reads the serialized ledger, not the in-memory completion.
		reopened, err := toolledger.New(dir, "owner")
		if err != nil {
			t.Fatal(err)
		}
		pending, err := reopened.PendingProjection(ctx)
		if err != nil || len(pending) != 1 {
			t.Fatalf("replay: %v pending=%v", err, pending)
		}
		result := pending[0].Result
		if result.ModelText == nil || !strings.HasPrefix(*result.ModelText, "settled: ") || result.IsError != failed {
			t.Fatalf("replay lost the final view or error state: %+v", result)
		}
		if result.JSONProjection() != observed.JSONProjection() || result.JSONProjection() != messages[0].ToolResult.JSONProjection() || result.TextProjection() != messages[0].Content {
			t.Fatal("ledger, callback and history disagree")
		}
		if failed && !strings.Contains(*result.ModelText, executor.err.Error()) {
			t.Fatal("finalizer ran before execution-error normalization")
		}
	}
}

type historyAwareRuntimeTools struct{}

func (historyAwareRuntimeTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "barrier_tool"}}
}

func (historyAwareRuntimeTools) Execute(ctx context.Context, _ providers.ToolCall) (string, error) {
	if len(HistoryFromContext(ctx)) == 0 {
		return "", errors.New("parent history is unavailable")
	}
	return "history available", nil
}

func (historyAwareRuntimeTools) ToolMetadata(_ providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: true, ConcurrencySafe: false}, true
}

func TestTurnToolRuntimeFinalOnlyToolUsesFinalHistoryContext(t *testing.T) {
	call := providers.ToolCall{ID: "call-barrier", Name: "barrier_tool", Arguments: `{}`}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{
		Executor:   historyAwareRuntimeTools{},
		RunContext: context.Background(),
	})
	runtime.ObserveStreamEvent(context.Background(), providers.StreamEvent{
		Type:     providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{ID: call.ID, Name: call.Name},
	})
	runtime.ObserveStreamEvent(context.Background(), providers.StreamEvent{
		Type:     providers.EventToolUseEnd,
		ToolCall: &call,
	})

	finalCtx := ContextWithHistory(context.Background(), []providers.ChatMessage{{Role: "user", Content: "parent"}})
	msgs, err := runtime.ExecuteFinalCalls(finalCtx, []providers.ToolCall{call}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "history available" {
		t.Fatalf("final-only tool did not receive final history context: %+v", msgs)
	}
}

func TestTurnToolRuntimeStopsWhenLedgerCannotPrepare(t *testing.T) {
	tools := &runtimeTestTools{results: map[string]string{"call-1": "must not execute"}}
	ledger, err := toolledger.New(t.TempDir(), "thread-invalid-operation")
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools, Ledger: ledger})
	msgs, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{{ID: "call-1", Name: "write_file", Arguments: `{}`}}, nil)
	if err == nil || len(msgs) != 0 {
		t.Fatalf("ledger preparation failure returned messages=%+v error=%v", msgs, err)
	}
	if calls := tools.recordedCalls(); len(calls) != 0 {
		t.Fatalf("tool ran without durable preparation: %+v", calls)
	}
}

func BenchmarkTurnToolRuntimeNoStreamingOverlap(b *testing.B) {
	benchmarkTurnToolRuntimeOverlap(b, false)
}

func BenchmarkTurnToolRuntimeStreamingOverlap(b *testing.B) {
	benchmarkTurnToolRuntimeOverlap(b, true)
}

func benchmarkTurnToolRuntimeOverlap(b *testing.B, streaming bool) {
	calls := []providers.ToolCall{
		{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
		{ID: "call_2", Name: "read_file", Arguments: `{"path":"b.go"}`},
		{ID: "call_3", Name: "read_file", Arguments: `{"path":"c.go"}`},
		{ID: "call_4", Name: "read_file", Arguments: `{"path":"d.go"}`},
	}
	const toolDelay = time.Millisecond
	const modelTail = time.Millisecond

	var totalToolExecs int
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := context.Background()
		tools := &runtimeTestTools{
			metadata: map[string]ToolMetadata{
				"read_file": {ReadOnly: true, ConcurrencySafe: true},
			},
			delays: map[string]time.Duration{
				"call_1": toolDelay,
				"call_2": toolDelay,
				"call_3": toolDelay,
				"call_4": toolDelay,
			},
		}
		runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: tools})
		if streaming {
			for _, call := range calls {
				runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
					Type:     providers.EventToolUseStart,
					ToolCall: &providers.ToolCall{ID: call.ID, Name: call.Name},
				})
				runtime.ObserveStreamEvent(ctx, providers.StreamEvent{
					Type:     providers.EventToolUseEnd,
					ToolCall: &call,
				})
			}
		}
		time.Sleep(modelTail)
		msgs, _ := runtime.ExecuteFinalCalls(ctx, calls, nil)
		if len(msgs) != len(calls) {
			b.Fatalf("expected %d tool messages, got %d", len(calls), len(msgs))
		}
		totalToolExecs += len(tools.recordedCalls())
	}
	b.StopTimer()
	b.ReportMetric(float64(totalToolExecs)/float64(b.N), "tool_execs/op")
}
