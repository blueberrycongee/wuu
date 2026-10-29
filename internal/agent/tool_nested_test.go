package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	const count = 12
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

type contextualMetadataTools struct {
	orchestrationTestTools
	resolve func(context.Context, providers.ToolCall) (ToolMetadata, bool, error)
}

func (e contextualMetadataTools) ToolMetadataContext(ctx context.Context, call providers.ToolCall) (ToolMetadata, bool, error) {
	if err := ctx.Err(); err != nil {
		return ToolMetadata{}, false, err
	}
	if e.resolve != nil {
		return e.resolve(ctx, call)
	}
	// A selected provider may support a tool without returning classification.
	return ToolMetadata{}, false, nil
}

func TestNestedMissingMetadataAndCanceledDuplicateWaiter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var leaves atomic.Int32
	executor := contextualMetadataTools{orchestrationTestTools: orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			leaves.Add(1)
			close(entered)
			select {
			case <-release:
				return "done", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", errors.New("no nested scope")
		}
		call = providers.ToolCall{ID: "same", Name: "read", Arguments: `{}`}
		first := make(chan error, 1)
		go func() { _, err := scope.Invoke(ctx, call); first <- err }()
		select {
		case <-entered:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		waiter, cancelWaiter := context.WithCancel(ctx)
		cancelWaiter()
		if _, err := scope.Invoke(waiter, call); !errors.Is(err, context.Canceled) {
			return "", fmt.Errorf("canceled duplicate: %v", err)
		}
		close(release)
		if err := <-first; err != nil {
			return "", err
		}
		replay, err := scope.Invoke(ctx, call)
		if err != nil {
			return "", err
		}
		return replay.TextProjection(), nil
	}}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Gate: NewToolExecutionGate(1)})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "done" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if leaves.Load() != 1 {
		t.Fatalf("leaf executions=%d, want 1", leaves.Load())
	}
}

func TestNestedMetadataLookupHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered := make(chan struct{})
	var leaves atomic.Int32
	executor := contextualMetadataTools{orchestrationTestTools: orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			leaves.Add(1)
			return "unexpected", nil
		}
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", errors.New("no nested scope")
		}
		child, cancelChild := context.WithCancel(ctx)
		defer cancelChild()
		done := make(chan error, 1)
		go func() { _, err := scope.Invoke(child, providers.ToolCall{ID: "child", Name: "read"}); done <- err }()
		select {
		case <-entered:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		cancelChild()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				return "", fmt.Errorf("metadata cancellation: %v", err)
			}
			return "canceled", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}, resolve: func(ctx context.Context, _ providers.ToolCall) (ToolMetadata, bool, error) {
		close(entered)
		<-ctx.Done()
		return ToolMetadata{}, false, ctx.Err()
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Gate: NewToolExecutionGate(1)})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "canceled" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if leaves.Load() != 0 {
		t.Fatalf("canceled lookup started %d leaf calls", leaves.Load())
	}
}

func TestNestedPendingMetadataCountsTowardCallLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 1024)
	executor := contextualMetadataTools{orchestrationTestTools: orchestrationTestTools{run: func(context.Context, providers.ToolCall) (string, error) { return "unexpected", nil }}, resolve: func(ctx context.Context, _ providers.ToolCall) (ToolMetadata, bool, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return ToolMetadata{}, false, ctx.Err()
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Gate: NewToolExecutionGate(1)})
	scope := &nestedToolScope{runtime: runtime, parent: &toolRun{}, ctx: ctx, calls: make(map[string]*toolRun)}
	var wg sync.WaitGroup
	for i := 0; i < 1024; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = scope.Invoke(ctx, providers.ToolCall{ID: fmt.Sprintf("id-%d", i), Name: "read"})
		}(i)
	}
	for i := 0; i < 1024; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata lookup did not start")
		}
	}
	_, err := scope.Invoke(ctx, providers.ToolCall{ID: "over-limit", Name: "read"})
	if err == nil || !strings.Contains(err.Error(), "limit reached") {
		t.Fatalf("overflow error=%v", err)
	}
	waiter, stopWaiter := context.WithCancel(ctx)
	stopWaiter()
	_, err = scope.Invoke(waiter, providers.ToolCall{ID: "id-0", Name: "read"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("duplicate waiter error=%v", err)
	}
	cancel()
	wg.Wait()
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if len(scope.pending) != 0 {
		t.Fatalf("pending lookups leaked: %d", len(scope.pending))
	}
}

func TestNestedPendingDuplicateSharesLookupAndWaiterCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var lookups, leaves atomic.Int32
	executor := contextualMetadataTools{orchestrationTestTools: orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
		if call.Name != "orchestrate" {
			leaves.Add(1)
			return "done", nil
		}
		scope, ok := toolctx.Nested(ctx)
		if !ok {
			return "", errors.New("no nested scope")
		}
		call = providers.ToolCall{ID: "same", Name: "read", Arguments: `{}`}
		first := make(chan error, 1)
		go func() { _, err := scope.Invoke(ctx, call); first <- err }()
		select {
		case <-entered:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		waiting, stopWaiting := context.WithCancel(ctx)
		duplicate := make(chan error, 1)
		go func() { _, err := scope.Invoke(waiting, call); duplicate <- err }()
		stopWaiting()
		if err := <-duplicate; !errors.Is(err, context.Canceled) {
			return "", fmt.Errorf("duplicate cancellation: %v", err)
		}
		close(release)
		if err := <-first; err != nil {
			return "", err
		}
		result, err := scope.Invoke(ctx, call)
		if err != nil {
			return "", err
		}
		return result.TextProjection(), nil
	}}, resolve: func(ctx context.Context, _ providers.ToolCall) (ToolMetadata, bool, error) {
		lookups.Add(1)
		close(entered)
		select {
		case <-release:
			return ToolMetadata{}, false, nil
		case <-ctx.Done():
			return ToolMetadata{}, false, ctx.Err()
		}
	}}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Gate: NewToolExecutionGate(1)})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "done" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if lookups.Load() != 1 || leaves.Load() != 1 {
		t.Fatalf("lookups=%d leaves=%d, want one each", lookups.Load(), leaves.Load())
	}
}

// observePendingContext signals when Invoke begins waiting on this context.
// AfterFunc is forwarded separately so only the actual wait calls Done.
type observePendingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observePendingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func (c *observePendingContext) AfterFunc(f func()) func() bool {
	return context.AfterFunc(c.Context, f)
}

func TestNestedPendingFirstCallerOwnsCancellation(t *testing.T) {
	for _, cancelOwner := range []bool{false, true} {
		name := "duplicate-canceled"
		if cancelOwner {
			name = "owner-canceled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var lookups, leaves atomic.Int32
			executor := contextualMetadataTools{orchestrationTestTools: orchestrationTestTools{run: func(context.Context, providers.ToolCall) (string, error) { leaves.Add(1); return "done", nil }}, resolve: func(ctx context.Context, _ providers.ToolCall) (ToolMetadata, bool, error) {
				lookups.Add(1)
				close(entered)
				select {
				case <-ctx.Done():
					return ToolMetadata{}, false, ctx.Err()
				case <-release:
					return ToolMetadata{}, false, nil
				}
			}}
			rt := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor, Gate: NewToolExecutionGate(1)})
			scope := &nestedToolScope{runtime: rt, parent: &toolRun{}, ctx: ctx, calls: make(map[string]*toolRun)}
			call := providers.ToolCall{ID: "same", Name: "read", Arguments: `{}`}
			firstCtx, cancelFirst := context.WithCancel(ctx)
			defer cancelFirst()
			duplicateBase, cancelDuplicate := context.WithCancel(ctx)
			defer cancelDuplicate()
			duplicateCtx := &observePendingContext{Context: duplicateBase, waiting: make(chan struct{})}
			first, duplicate := make(chan error, 1), make(chan error, 1)
			go func() { _, err := scope.Invoke(firstCtx, call); first <- err }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			go func() { _, err := scope.Invoke(duplicateCtx, call); duplicate <- err }()
			select {
			case <-duplicateCtx.waiting:
			case <-ctx.Done():
				t.Fatal("duplicate did not enter shared lookup")
			}
			if cancelOwner {
				cancelFirst()
				if err := <-first; !errors.Is(err, context.Canceled) {
					t.Fatalf("first error: %v", err)
				}
				if err := <-duplicate; !errors.Is(err, context.Canceled) {
					t.Fatalf("duplicate error: %v", err)
				}
				if leaves.Load() != 0 {
					t.Fatalf("canceled lookup executed %d leaves", leaves.Load())
				}
				scope.mu.Lock()
				pending, calls := len(scope.pending), len(scope.calls)
				scope.mu.Unlock()
				if pending != 0 || calls != 0 {
					t.Fatalf("canceled lookup retained pending=%d calls=%d", pending, calls)
				}
			} else {
				cancelDuplicate()
				if err := <-duplicate; !errors.Is(err, context.Canceled) {
					t.Fatalf("duplicate error: %v", err)
				}
				close(release)
				if err := <-first; err != nil {
					t.Fatalf("first error: %v", err)
				}
				if leaves.Load() != 1 {
					t.Fatalf("first caller executed %d leaves", leaves.Load())
				}
			}
			if lookups.Load() != 1 {
				t.Fatalf("metadata lookups=%d, want 1", lookups.Load())
			}
		})
	}
}
