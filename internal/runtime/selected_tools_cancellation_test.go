package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

type blockingToolResolution struct {
	*runtimeServiceConsumer
	entered    chan context.Context
	release    chan struct{}
	once       sync.Once
	executions atomic.Int32
}

func (*blockingToolResolution) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{toolservice.Descriptor()}
}
func (p *blockingToolResolution) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	switch call.Method {
	case "resolve":
		p.once.Do(func() { p.entered <- ctx })
		// Intentionally retain the physical callback after cancellation. Stop
		// must signal it without abandoning its captured provider generation.
		<-p.release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"found":true,"supports":true,"metadata":{"read_only":true,"concurrency_safe":true}}`), nil
	case "execute":
		p.executions.Add(1)
		return nil, errors.New("canceled metadata must not execute")
	default:
		return json.RawMessage(`{}`), nil
	}
}

func TestSelectedToolResolutionCancellationDrainsAdmission(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "final", true: "stream"}[streaming], func(t *testing.T) {
			provider := &blockingToolResolution{runtimeServiceConsumer: &runtimeServiceConsumer{id: "blocking.tools", state: pluginhost.StatePrepared}, entered: make(chan context.Context, 1), release: make(chan struct{})}
			consumer := &runtimeServiceConsumer{id: toolExecutionConsumer, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: toolservice.ServiceName, MajorVersion: 1, Required: true}}}
			registry, conflicts := pluginhost.BuildServiceRegistry(provider, consumer)
			host := pluginhost.New(provider, consumer)
			host.AttachServiceRegistry(registry, conflicts)
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			var release sync.Once
			unblock := func() { release.Do(func() { close(provider.release) }) }
			defer unblock()
			var initialized atomic.Int32
			executor := newSelectedToolExecutor(host, toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				initialized.Add(1)
				return toolservice.Backend{}, errors.New("native fallback forbidden")
			}))
			runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor})
			call := providers.ToolCall{ID: "blocked", Name: "external_only", Arguments: "{}"}
			finished := make(chan error, 1)
			go func() {
				if streaming {
					finished <- runtime.ObserveStreamEvent(context.Background(), providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call})
					return
				}
				_, err := runtime.ExecuteFinalCalls(context.Background(), []providers.ToolCall{call}, nil)
				finished <- err
			}()
			var resolveCtx context.Context
			select {
			case resolveCtx = <-provider.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("selected resolve did not start")
			}
			canceled := make(chan struct{})
			go func() { runtime.Cancel(); close(canceled) }()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				unblock()
				<-finished
				<-canceled
				t.Fatal("Cancel waited for the selected resolve callback")
			}
			select {
			case <-resolveCtx.Done():
			case <-time.After(time.Second):
				unblock()
				<-finished
				t.Fatal("selected resolve lost runtime cancellation")
			}
			closed := make(chan struct{})
			go func() { runtime.Close(); close(closed) }()
			select {
			case <-closed:
				t.Fatal("Close abandoned the admitted selected resolve")
			case <-time.After(20 * time.Millisecond):
			}
			unblock()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled admission = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("admission did not finish")
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("Close did not drain resolution")
			}
			if provider.executions.Load() != 0 || initialized.Load() != 0 {
				t.Fatalf("canceled admission executed or fell back: execute=%d native=%d", provider.executions.Load(), initialized.Load())
			}
		})
	}
}
