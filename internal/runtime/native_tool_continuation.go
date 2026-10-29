package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// nativeToolContinuation owns one invocation of the original native tool.
// The callback is constructed by the host and cannot be supplied over RPC.
type nativeToolContinuation struct {
	mu     sync.Mutex
	used   bool
	ctx    context.Context
	cancel context.CancelFunc
	run    func(context.Context) (toolresult.Result, error)
}

func newNativeToolContinuation(ctx context.Context, run func(context.Context) (toolresult.Result, error)) *nativeToolContinuation {
	owned, cancel := context.WithCancel(ctx)
	return &nativeToolContinuation{ctx: owned, cancel: cancel, run: run}
}
func (c *nativeToolContinuation) close() { c.cancel() }
func (c *nativeToolContinuation) invoke(ctx context.Context) (toolresult.Result, error) {
	c.mu.Lock()
	if c.used {
		c.mu.Unlock()
		return toolresult.Result{}, errors.New("native tool continuation already used")
	}
	c.used = true
	c.mu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	callCtx, cancel := context.WithCancel(c.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	result, err := c.run(callCtx)
	if cause := callCtx.Err(); cause != nil {
		return toolresult.Result{}, cause
	}
	return result, err
}

type nativeToolContinuationKey struct{}

type nativeToolContinuationInvoker struct{ parent *kernelHostServices }

func (i *nativeToolContinuationInvoker) ID() string                { return i.parent.ID() }
func (i *nativeToolContinuationInvoker) Status() pluginhost.Status { return i.parent.Status() }
func (i *nativeToolContinuationInvoker) InvokeService(ctx context.Context, p pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if p.Method != pluginhost.KernelServiceMethod {
		return nil, serviceError("method_not_found", "unknown native continuation method")
	}
	if err := decodeServiceParams(p.Params, &struct{}{}); err != nil {
		return nil, err
	}
	scope, failure := p.CallerExecution(true)
	if failure != nil {
		return nil, failure
	}
	if !pluginhost.OwnsNativeToolContinuation(scope) {
		return nil, serviceError("invalid_execution_scope", "native continuation owner mismatch")
	}
	c, ok := scope.Context.Value(nativeToolContinuationKey{}).(*nativeToolContinuation)
	if !ok || c == nil {
		return nil, serviceError("invalid_execution_scope", "native continuation unavailable")
	}
	result, err := c.invoke(ctx)
	if err != nil {
		return nil, err
	}
	return marshalServiceResult(result)
}
