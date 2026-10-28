package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

type orchestrationTestTools struct {
	run func(context.Context, providers.ToolCall) (string, error)
}

func (e orchestrationTestTools) Definitions() []providers.ToolDefinition { return nil }
func (e orchestrationTestTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	return e.run(ctx, call)
}
func (e orchestrationTestTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	if call.Name == "orchestrate" {
		return ToolMetadata{Orchestrator: true, ConcurrencySafe: true}, true
	}
	return ToolMetadata{ReadOnly: call.Name == "read", ConcurrencySafe: call.Name == "read"}, true
}

func TestNestedCallsUseLedgerWithoutProviderProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	ledger, err := toolledger.New(agentTestDocuments(t, dir), "nested-owner")
	if err != nil {
		t.Fatal(err)
	}
	var retained toolctx.NestedExecutor
	var leafCalls atomic.Int32
	executor := orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			if _, ok := toolctx.Nested(ctx); ok {
				return "", errors.New("leaf inherited parent delegation authority")
			}
			leafCalls.Add(1)
			return "written", nil
		}
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", errors.New("no nested scope")
		}
		retained = scope
		child := providers.ToolCall{ID: "local", Name: "write", Arguments: `{}`}
		first, err := scope.Invoke(ctx, child)
		if err != nil {
			return "", err
		}
		second, err := scope.Invoke(ctx, child)
		if err != nil || first.TextProjection() != second.TextProjection() {
			return "", errors.New("idempotent retry lost child result")
		}
		child.Arguments = `{"different":true}`
		if _, err := scope.Invoke(ctx, child); err == nil {
			return "", errors.New("conflicting local ID was accepted")
		}
		return first.TextProjection(), nil
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Ledger: ledger, OperationID: "nested-operation", Gate: NewToolExecutionGate(1)})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "written" {
		t.Fatalf("parent: %+v, %v", messages, err)
	}
	if leafCalls.Load() != 1 {
		t.Fatalf("nested write replayed %d times", leafCalls.Load())
	}
	// The provider only issued parent. Injecting child result messages on
	// recovery would violate provider tool-call pairing and duplicate output.
	pending, err := ledger.PendingProjection(ctx)
	if err != nil || len(pending) != 1 || pending[0].ProviderCallID != "parent" {
		t.Fatalf("provider projection: %+v, %v", pending, err)
	}
	view, err := agentTestDocuments(t, dir).Read(ctx, persistence.Query{Namespace: toolstate.Invocations})
	if err != nil {
		t.Fatal(err)
	}
	children := 0
	for _, row := range view.Documents {
		var invocation toolstate.Invocation
		if err := json.Unmarshal(row.Value, &invocation); err != nil {
			t.Fatal(err)
		}
		if invocation.ParentInvocationID != messages[0].ToolInvocationID {
			continue
		}
		children++
		if invocation.State != string(toolledger.InvocationSucceeded) || invocation.ProviderCallID == "local" || invocation.Result == nil {
			t.Fatalf("child not independently recorded: %+v", invocation)
		}
	}
	if children != 1 {
		t.Fatalf("independent child receipts = %d, want 1", children)
	}
	if _, err := retained.Invoke(ctx, providers.ToolCall{ID: "late", Name: "write"}); err == nil {
		t.Fatal("completed parent retained execution authority")
	}
}

func TestNestedParentsDoNotConsumeLeafSlots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const count = maxToolConcurrency + 2
	parents := make(chan struct{}, count)
	release := make(chan struct{})
	var writes atomic.Int32
	executor := orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			writes.Add(1)
			return "ok", nil
		}
		parents <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		scope, _ := toolctx.Nested(ctx)
		result, err := scope.Invoke(ctx, providers.ToolCall{ID: "same-local-key", Name: "write", Arguments: `{}`})
		return result.TextProjection(), err
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Gate: NewToolExecutionGate(1)})
	var calls []providers.ToolCall
	for i := range count {
		calls = append(calls, providers.ToolCall{ID: fmt.Sprint(i), Name: "orchestrate"})
	}
	finished := make(chan error, 1)
	go func() { _, err := runtime.ExecuteFinalCalls(ctx, calls, nil); finished <- err }()
	for range count {
		select {
		case <-parents:
		case <-ctx.Done():
			t.Fatal("parents are occupying leaf slots")
		}
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if writes.Load() != count {
		t.Fatalf("only %d children ran", writes.Load())
	}
}

func TestNestedCancellationRevokesChildren(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, stopped := make(chan struct{}), make(chan struct{})
	executor := orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name == "orchestrate" {
			scope, _ := toolctx.Nested(ctx)
			result, err := scope.Invoke(context.Background(), providers.ToolCall{ID: "local", Name: "write"})
			return result.TextProjection(), err
		}
		close(entered)
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate"}}, nil)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("child never started")
	}
	runtime.Cancel()
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("child outlived runtime cancellation")
	}
	<-finished
}

func TestToolExecutionGateSerializesWritersAgainstReaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gate := NewToolExecutionGate(3)
	var activeReaders, activeWriters atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 60 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			shared := i%3 != 0
			release, err := gate.acquire(ctx, shared)
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			if shared {
				if n := activeReaders.Add(1); n > 3 {
					t.Error("reader capacity exceeded")
				}
				if activeWriters.Load() != 0 {
					t.Error("reader overlapped writer")
				}
				activeReaders.Add(-1)
			} else {
				if n := activeWriters.Add(1); n != 1 {
					t.Error("writers overlapped")
				}
				if activeReaders.Load() != 0 {
					t.Error("writer overlapped reader")
				}
				activeWriters.Add(-1)
			}
		}()
	}
	close(start)
	wg.Wait()
	// Cancellation while capacity is occupied must not poison later admission.
	release, err := gate.acquire(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	aborted, abort := context.WithCancel(ctx)
	abort()
	if _, err := gate.acquire(aborted, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("writer cancel: %v", err)
	}
	release()
	release, err = gate.acquire(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

type streamingOrchestrationTools struct{ orchestrationTestTools }

func (e streamingOrchestrationTools) ToolMetadata(call providers.ToolCall) (ToolMetadata, bool) {
	return ToolMetadata{ReadOnly: true, ConcurrencySafe: true, Orchestrator: call.Name == "orchestrate"}, true
}

func TestOrphanStreamingParentClosesNestedAdmissionBeforeReconciliationUnlocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scopes := make(chan toolctx.NestedExecutor, 1)
	executor := streamingOrchestrationTools{orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			return "unadmitted child effect", nil
		}
		scope, _ := toolctx.Nested(ctx)
		scopes <- scope
		<-ctx.Done()
		return "", ctx.Err()
	}}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, RunContext: ctx})
	releaseCancel := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCancel) }) }
	defer func() { release(); runtime.Close() }()
	call := providers.ToolCall{ID: "orphan", Name: "orchestrate", Arguments: `{}`}
	if err := runtime.ObserveStreamEvent(ctx, providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}); err != nil {
		t.Fatal(err)
	}
	var scope toolctx.NestedExecutor
	select {
	case scope = <-scopes:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancelEntered := make(chan struct{})
	runtime.mu.Lock()
	run := runtime.byID[call.ID]
	originalCancel := run.cancel
	run.cancel = func() {
		close(cancelEntered)
		<-releaseCancel
		originalCancel()
	}
	runtime.mu.Unlock()
	joined := make(chan error, 1)
	go func() { _, err := runtime.ExecuteFinalCalls(ctx, nil, nil); joined <- err }()
	select {
	case <-cancelEntered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	child := make(chan error, 1)
	go func() {
		_, err := scope.Invoke(ctx, providers.ToolCall{ID: "late", Name: "read", Arguments: `{}`})
		child <- err
	}()
	// Keep cancellation deliberately pending: reconciliation must not expose an
	// unlocked, still-live orphan scope in which this late child can execute.
	select {
	case err := <-child:
		t.Fatalf("late child crossed orphan reconciliation before cancellation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-child:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late child was not canceled: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(runtime.TakeRequestContextSegments()) != 0 {
		t.Fatal("orphan child context entered the next model request")
	}
}
