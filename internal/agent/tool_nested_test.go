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

type resolvingOrchestrationTools struct {
	orchestrationTestTools
	resolve func(context.Context, providers.ToolCall) (ToolMetadata, bool, error)
}

func (e resolvingOrchestrationTools) ToolMetadataContext(ctx context.Context, call providers.ToolCall) (ToolMetadata, bool, error) {
	return e.resolve(ctx, call)
}

func TestNestedMetadataAdmissionDrainsAfterParentReturns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	resolved := make(chan providers.ToolCall, 1)
	childDone := make(chan error, 1)
	var unblock sync.Once
	releaseResolution := func() { unblock.Do(func() { close(release) }) }
	defer releaseResolution()
	var effects atomic.Int32
	executor := resolvingOrchestrationTools{
		resolve: func(ctx context.Context, call providers.ToolCall) (ToolMetadata, bool, error) {
			if call.Name == "orchestrate" {
				return ToolMetadata{Orchestrator: true, ConcurrencySafe: true}, true, nil
			}
			resolved <- call
			close(entered)
			// The transport is still physically active after observing owner
			// cancellation, so Close must retain this callback until it returns.
			<-release
			return ToolMetadata{}, false, ctx.Err()
		},
		orchestrationTestTools: orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
			if call.Name != "orchestrate" {
				effects.Add(1)
				return "unexpected effect", nil
			}
			scope, _ := toolctx.Nested(ctx)
			go func() {
				_, err := scope.Invoke(context.Background(), providers.ToolCall{ID: "local", Name: "read", Arguments: `{}`})
				childDone <- err
			}()
			select {
			case <-entered:
				return "parent finished", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}},
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "parent finished" {
		t.Fatalf("parent failed before child admission: %+v %v", messages, err)
	}
	call := <-resolved
	if !strings.HasPrefix(call.ID, "nested-") {
		t.Errorf("metadata observed caller-local identity %q", call.ID)
	}
	closed := make(chan struct{})
	go func() { runtime.Close(); close(closed) }()
	select {
	case <-closed:
		t.Error("Close abandoned a nested metadata callback after its parent returned")
	case <-time.After(50 * time.Millisecond):
	}
	releaseResolution()
	select {
	case err := <-childDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("child admission after owner completion: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("child resolution did not drain")
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Close did not finish after nested admission drained")
	}
	if effects.Load() != 0 {
		t.Fatalf("canceled nested admission performed %d effects", effects.Load())
	}
}

func TestNestedRepeatUsesAdmittedMetadataAndHostIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resolutions, effects atomic.Int32
	var resolvedID, executedID string
	executor := resolvingOrchestrationTools{
		resolve: func(_ context.Context, call providers.ToolCall) (ToolMetadata, bool, error) {
			if call.Name == "orchestrate" {
				return ToolMetadata{Orchestrator: true, ConcurrencySafe: true}, true, nil
			}
			if resolutions.Add(1) != 1 {
				return ToolMetadata{}, false, errors.New("provider resolution is unavailable after admission")
			}
			resolvedID = call.ID
			return ToolMetadata{ReadOnly: true, ConcurrencySafe: true}, true, nil
		},
		orchestrationTestTools: orchestrationTestTools{run: func(ctx context.Context, call providers.ToolCall) (string, error) {
			if call.Name != "orchestrate" {
				effects.Add(1)
				executedID = call.ID
				return "recorded child result", nil
			}
			scope, _ := toolctx.Nested(ctx)
			child := providers.ToolCall{ID: "local", Name: "read", Arguments: `{}`}
			first, err := scope.Invoke(ctx, child)
			if err != nil {
				return "", err
			}
			second, err := scope.Invoke(ctx, child)
			if err != nil {
				return "", err
			}
			if first.TextProjection() != second.TextProjection() {
				return "", errors.New("admitted child outcome changed")
			}
			return second.TextProjection(), nil
		}},
	}
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: executor})
	defer runtime.Close()
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "parent", Name: "orchestrate", Arguments: `{}`}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "recorded child result" {
		t.Errorf("repeat did not join admitted result: %+v %v", messages, err)
	}
	if resolutions.Load() != 1 || effects.Load() != 1 {
		t.Errorf("repeat resolved or executed again: resolutions=%d effects=%d", resolutions.Load(), effects.Load())
	}
	if resolvedID != executedID || !strings.HasPrefix(resolvedID, "nested-") {
		t.Errorf("resolution/execution identity mismatch: resolved=%q executed=%q", resolvedID, executedID)
	}
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
