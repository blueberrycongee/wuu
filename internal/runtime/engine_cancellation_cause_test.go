package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

func TestEngineGatewayCallerCancellationCause(t *testing.T) {
	for _, cause := range []error{pluginhost.ErrExecutionShutdown, context.Canceled} {
		entered := make(chan struct{})
		observed := make(chan error, 1)
		runner := &agent.StreamRunner{LoopDriver: agent.DefaultDriver{}, BeginModelReceipts: func(ctx context.Context) (context.Context, func() error, error) {
			close(entered)
			<-ctx.Done()
			observed <- context.Cause(ctx)
			return ctx, nil, ctx.Err()
		}}
		table := newEngineGatewayTable()
		id, remove := table.register(&engineSessionGateway{owner: "engine", runCtx: context.Background(), runtime: &ThreadRuntime{StreamRunner: runner}})
		raw, _ := json.Marshal(pluginprotocol.EngineGatewayParams{GatewayID: id})
		caller, cancel := context.WithCancelCause(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = table.InvokeService(caller, pluginhost.ServiceInvokeParams{Caller: "engine", Method: "run_loop", Params: raw})
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("loop not admitted")
		}
		cancel(cause)
		select {
		case got := <-observed:
			if got != cause {
				t.Fatalf("got %v, want %v", got, cause)
			}
		case <-time.After(time.Second):
			t.Fatal("loop not canceled")
		}
		<-done
		remove()
	}
}
