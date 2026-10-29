package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/stretchr/testify/require"
)

func TestNativeToolScopeOwnerRejectsLateWorker(t *testing.T) {
	owner := newNativeToolScopeOwner()
	main := toolservice.NewScope(nil)
	worker := toolservice.NewScope(nil)
	require.NoError(t, owner.add(main))
	require.NoError(t, owner.add(worker))
	require.NoError(t, owner.close(context.Background()))
	require.Error(t, owner.add(toolservice.NewScope(nil)))
	require.NoError(t, owner.close(context.Background()))
}

type blockingSurfaceExecutor struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSurfaceExecutor) Definitions() []providers.ToolDefinition {
	close(b.entered)
	<-b.release
	return nil
}
func (*blockingSurfaceExecutor) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", nil
}

func TestTemporaryScopeRetirementRetainsGenerationUntilNativeCallDrains(t *testing.T) {
	executor := &blockingSurfaceExecutor{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(executor.release) })
	service := toolservice.NewService()
	require.NoError(t, service.Activate(context.Background()))
	scope := toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
		return toolservice.Backend{Executor: executor}, nil
	})
	ctx := toolservice.WithScope(context.Background(), scope)
	params := pluginhost.ServiceInvokeParams{Service: toolservice.ServiceName, Method: "list", Params: json.RawMessage(`{}`)}
	callDone := make(chan error, 1)
	go func() { _, err := service.InvokeService(ctx, params); callDone <- err }()
	select {
	case <-executor.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native list did not enter")
	}
	closed := make(chan struct{})
	client := &generationClient{id: "scope-drain", onClose: func() { close(closed) }}
	generation := testPluginGeneration("scope-drain", client)
	owner := testGenerationSession(t, generation)
	retired := make(chan struct{})
	go func() { owner.retireTemporaryToolScope(scope, generation); close(retired) }()
	select {
	case <-retired:
	case <-time.After(5 * time.Second):
		t.Fatal("scope retirement blocked")
	}
	_, err := service.InvokeService(ctx, params)
	require.ErrorContains(t, err, "tool scope is closed")
	owner.releasePluginGeneration(generation)
	select {
	case <-closed:
		t.Fatal("generation closed while native call was active")
	default:
	}
	releaseOnce.Do(func() { close(executor.release) })
	select {
	case err := <-callDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("native call did not finish")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not close after native call")
	}
	require.NoError(t, service.Close(context.Background()))
}
