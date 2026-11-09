package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type nestedExecutionTracker struct{ *pluginhost.ExecutionTracker }

func (t nestedExecutionTracker) RecordExecutionUpdate(caller string, params pluginhost.ExecutionUpdateParams) *pluginhost.HostServiceError {
	return t.RecordUpdate(caller, params)
}
func (t nestedExecutionTracker) ResolveToolExecution(caller, id string) (pluginhost.ToolExecutionScope, *pluginhost.HostServiceError) {
	return t.ResolveTool(caller, id)
}

type nestedExecutorFunc func(context.Context, providers.ToolCall) (toolresult.Result, error)

func (f nestedExecutorFunc) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	return f(ctx, call)
}

func nestedServiceParams(caller, execution string) pluginhost.ServiceInvokeParams {
	raw, _ := json.Marshal(pluginhost.InvokeToolParams{ExecutionID: execution, CallID: "child", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)})
	return pluginhost.ServiceInvokeParams{Caller: caller, Method: pluginhost.KernelServiceMethod, Params: raw}
}

func TestNestedServiceEnforcesExecutionOwnership(t *testing.T) {
	tracker := nestedExecutionTracker{pluginhost.NewExecutionTracker()}
	invoker := &nestedToolInvoker{parent: newKernelHostServices(nil, tracker)}
	var calls atomic.Int32
	ctx := toolctx.WithNestedExecutor(context.Background(), nestedExecutorFunc(func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
		calls.Add(1)
		if call.ID != "child" || call.Name != "read_file" {
			t.Errorf("lost child identity: %+v", call)
		}
		return toolresult.FromText("read result"), nil
	}))
	input := pluginhost.ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "parent"}
	id := tracker.BeginTool("workflow", ctx, input)
	assertCode := func(params pluginhost.ServiceInvokeParams, code string) {
		t.Helper()
		_, err := invoker.InvokeService(context.Background(), params)
		var serviceErr *pluginhost.HostServiceError
		if !errors.As(err, &serviceErr) || serviceErr.Code != code {
			t.Fatalf("expected %s, got %v", code, err)
		}
	}
	assertCode(nestedServiceParams("other", id), "service_not_authorized")
	leaf := tracker.BeginTool("workflow", context.Background(), input)
	assertCode(nestedServiceParams("workflow", leaf), "invalid_execution_scope")
	tracker.End(leaf)
	data, err := invoker.InvokeService(context.Background(), nestedServiceParams("workflow", id))
	var result toolresult.Result
	if err != nil || json.Unmarshal(data, &result) != nil || result.TextProjection() != "read result" {
		t.Fatalf("child result: %s, %v", data, err)
	}
	tracker.End(id)
	assertCode(nestedServiceParams("workflow", id), "execution_not_found")
	if calls.Load() != 1 {
		t.Fatalf("unauthorized delegation: %d calls", calls.Load())
	}
}

func TestNestedServiceRetirementCancelsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tracker := nestedExecutionTracker{pluginhost.NewExecutionTracker()}
	invoker := &nestedToolInvoker{parent: newKernelHostServices(nil, tracker)}
	entered := make(chan struct{})
	owner := toolctx.WithNestedExecutor(ctx, nestedExecutorFunc(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		close(entered)
		<-ctx.Done()
		return toolresult.Result{}, ctx.Err()
	}))
	id := tracker.BeginTool("workflow", owner, pluginhost.ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "parent"})
	defer tracker.End(id)
	finished := make(chan error, 1)
	go func() { _, err := invoker.InvokeService(ctx, nestedServiceParams("workflow", id)); finished <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("child never started")
	}
	tracker.CancelPlugin("workflow", errors.New("generation retired"))
	// Retirement retains the execution record until dispatch unwinds, but must
	// not allow new children through that still-resolvable execution identity.
	if _, err := invoker.InvokeService(ctx, nestedServiceParams("workflow", id)); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired execution accepted new child: %v", err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("retired child: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("child survived generation retirement")
	}
}

func TestNestedServiceRequestCancellationDoesNotRevokeOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tracker := nestedExecutionTracker{pluginhost.NewExecutionTracker()}
	invoker := &nestedToolInvoker{parent: newKernelHostServices(nil, tracker)}
	entered := make(chan struct{})
	var calls atomic.Int32
	owner := toolctx.WithNestedExecutor(ctx, nestedExecutorFunc(func(ctx context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return toolresult.Result{}, ctx.Err()
		}
		return toolresult.FromText("still live"), ctx.Err()
	}))
	id := tracker.BeginTool("workflow", owner, pluginhost.ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "parent"})
	defer tracker.End(id)
	request, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	finished := make(chan error, 1)
	go func() {
		_, err := invoker.InvokeService(request, nestedServiceParams("workflow", id))
		finished <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("child never started")
	}
	cancelRequest()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled child: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("child survived request cancellation")
	}
	params := nestedServiceParams("workflow", id)
	var next pluginhost.InvokeToolParams
	if err := json.Unmarshal(params.Params, &next); err != nil {
		t.Fatal(err)
	}
	next.CallID = "next-child"
	params.Params, _ = json.Marshal(next)
	if _, err := invoker.InvokeService(ctx, params); err != nil {
		t.Fatalf("request cancellation revoked the owning execution: %v", err)
	}
}
