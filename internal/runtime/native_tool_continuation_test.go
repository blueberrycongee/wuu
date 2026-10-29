package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func TestNativeToolContinuationSingleUse(t *testing.T) {
	var calls atomic.Int32
	continuation := newNativeToolContinuation(context.Background(), func(context.Context) (toolresult.Result, error) {
		calls.Add(1)
		return toolresult.FromText("native-result"), nil
	})
	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := continuation.invoke(context.Background())
			if err == nil && result.TextProjection() == "native-result" {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), successes.Load())
	require.Equal(t, int32(1), calls.Load())
}

func TestNativeToolContinuationFailureAndCancellation(t *testing.T) {
	var calls atomic.Int32
	continuation := newNativeToolContinuation(context.Background(), func(context.Context) (toolresult.Result, error) {
		calls.Add(1)
		return toolresult.Result{}, errors.New("native failure")
	})
	_, err := continuation.invoke(context.Background())
	require.ErrorContains(t, err, "native failure")
	_, err = continuation.invoke(context.Background())
	require.Error(t, err)
	require.Equal(t, int32(1), calls.Load())
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := newNativeToolContinuation(ctx, func(context.Context) (toolresult.Result, error) { calls.Add(1); return toolresult.Result{}, nil })
	cancel()
	_, err = cancelled.invoke(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, int32(1), calls.Load())
}

func TestNativeToolContinuationRequiresOwnedToolScope(t *testing.T) {
	invoker := &nativeToolContinuationInvoker{parent: newKernelHostServices(nil, nil)}
	c := newNativeToolContinuation(context.Background(), func(context.Context) (toolresult.Result, error) { return toolresult.FromText("original"), nil })
	defer c.close()
	tracker := pluginhost.NewExecutionTracker()
	parentCtx := context.WithValue(context.Background(), nativeToolContinuationKey{}, c)
	parentCtx = pluginhost.WithNativeToolContinuationBinding(parentCtx, "owner")
	input := pluginhost.ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "call"}
	id := tracker.BeginTool("owner", parentCtx, input)
	defer tracker.End(id)
	scopeValue, scopeErr := tracker.ResolveTool("owner", id)
	require.Nil(t, scopeErr)
	scope := &scopeValue

	otherTracker := pluginhost.NewExecutionTracker()
	collisionID := otherTracker.BeginTool("owner", scope.Context, input)
	defer otherTracker.End(collisionID)
	require.Equal(t, id, collisionID)
	collision, scopeErr := otherTracker.ResolveTool("owner", collisionID)
	require.Nil(t, scopeErr)
	childID := tracker.BeginTool("owner", scope.Context, pluginhost.ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "child"})
	defer tracker.End(childID)
	child, scopeErr := tracker.ResolveTool("owner", childID)
	require.Nil(t, scopeErr)
	otherID := tracker.BeginTool("other", scope.Context, pluginhost.ToolExecuteInput{ThreadID: "thread", TurnID: "turn", CallID: "other"})
	defer tracker.End(otherID)
	other, scopeErr := tracker.ResolveTool("other", otherID)
	require.Nil(t, scopeErr)
	p := pluginhost.ServiceInvokeParams{Method: pluginhost.KernelServiceMethod, Caller: "other", Params: json.RawMessage(`{}`), CallerScope: scope}
	_, err := invoker.InvokeService(context.Background(), p)
	require.Error(t, err)
	p.Caller = "owner"
	p.CallerScope = &collision
	_, err = invoker.InvokeService(context.Background(), p)
	require.Error(t, err)
	p.Caller = "owner"
	p.CallerScope = &child
	_, err = invoker.InvokeService(context.Background(), p)
	require.Error(t, err)
	p.Caller = "other"
	p.CallerScope = &other
	_, err = invoker.InvokeService(context.Background(), p)
	require.Error(t, err)
	p.Caller = "owner"
	p.CallerScope = scope
	p.Params = json.RawMessage(`{"name":"grep"}`)
	_, err = invoker.InvokeService(context.Background(), p)
	require.Error(t, err)
	p.Params = json.RawMessage(`{}`)
	data, err := invoker.InvokeService(context.Background(), p)
	require.NoError(t, err)
	require.Contains(t, string(data), "original")
	_, err = invoker.InvokeService(context.Background(), p)
	require.Error(t, err)
}

func TestNativeToolContinuationRetirementCancelsInFlight(t *testing.T) {
	entered := make(chan struct{})
	c := newNativeToolContinuation(context.Background(), func(ctx context.Context) (toolresult.Result, error) {
		close(entered)
		<-ctx.Done()
		return toolresult.Result{}, ctx.Err()
	})
	result := make(chan error, 1)
	go func() { _, err := c.invoke(context.Background()); result <- err }()
	<-entered
	c.close()
	require.ErrorIs(t, <-result, context.Canceled)
	_, err := c.invoke(context.Background())
	require.Error(t, err)
}

func TestNativeToolContinuationDiscardsLateResult(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := newNativeToolContinuation(context.Background(), func(context.Context) (toolresult.Result, error) {
		close(entered)
		<-release
		return toolresult.FromText("late"), nil
	})
	done := make(chan error, 1)
	go func() { _, err := c.invoke(context.Background()); done <- err }()
	<-entered
	c.close()
	close(release)
	require.ErrorIs(t, <-done, context.Canceled)
}
