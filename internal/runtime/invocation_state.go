package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type invocationStateKey struct{}
type invocationStateRetainer func() (*State, error)
type modelInvocationOwner struct {
	cancel           context.CancelFunc
	stopScope        func() bool
	binding          *State
	receipts         *providers.ModelReceiptScope
	settlement       *providers.ModelReceiptScope
	ownsReceipts     bool
	endParentReceipt func(error)
	completionCtx    context.Context
	cancelCompletion context.CancelCauseFunc
}

// WithInvocationState borrows a live capture for host dispatch. Asynchronous
// callers must retain the handle before launching work and close it afterward.
func WithInvocationState(ctx context.Context, binding *State) context.Context {
	return context.WithValue(ctx, invocationStateKey{}, invocationStateRetainer(func() (*State, error) {
		if retained := binding.Retain(); retained != nil {
			return retained, nil
		}
		return nil, serviceError("generation_closed", "invocation state is no longer available")
	}))
}

// HasInvocationState distinguishes a missing capture from a present capture
// whose generation has closed. Callers must never replace a failed capture with
// a newer authority merely because retention failed.
func HasInvocationState(ctx context.Context) bool {
	_, ok := ctx.Value(invocationStateKey{}).(invocationStateRetainer)
	return ok
}

// RetainInvocationState acquires the authority supplied by this invocation.
// It never selects a current Session or opens state by directory. Close the
// returned handle after all synchronous and asynchronous consumers finish.
func RetainInvocationState(ctx context.Context) (*State, error) {
	retain, ok := ctx.Value(invocationStateKey{}).(invocationStateRetainer)
	if !ok {
		return nil, serviceError("service_unavailable", "invocation has no session state")
	}
	return retain()
}

// RetainProductInvocationState routes session lifecycle operations to the
// durable authority imported by a product composition. Model and tool calls
// retain the invoking composition's own generation through RetainInvocationState.
func RetainProductInvocationState(ctx context.Context) (*State, error) {
	binding, err := RetainInvocationState(ctx)
	for err == nil && binding.Generation != nil && binding.Generation.productParent != nil {
		parent := binding.Generation.productParent
		next, nextErr := parent.retainState()
		_ = binding.Close()
		binding, err = next, nextErr
	}
	return binding, err
}

// bindStateGeneration binds one graph before effectful lifecycle callbacks.
// Its Session owner may be attached after construction, without changing graphs.
func (k *kernelHostServices) bindStateGeneration(generation *PluginGeneration, owner *Session) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stateGeneration, k.stateOwner = generation, owner
	if registry := generation.host.ServiceRegistry(); registry != nil {
		registry.SetGraphGeneration(generation.id)
	}
	generation.beginModelReceipts = k.beginModelReceipts
}

func (k *kernelHostServices) retainState() (*State, error) {
	k.mu.RLock()
	generation := k.stateGeneration
	k.mu.RUnlock()
	if generation == nil || generation.state == nil {
		return nil, serviceError("service_unavailable", "session state is unavailable during generation prepare")
	}
	for refs := generation.refs.Load(); ; refs = generation.refs.Load() {
		if refs <= 0 {
			return nil, serviceError("generation_closed", "invocation generation has no live owner")
		}
		if generation.refs.CompareAndSwap(refs, refs+1) {
			break
		}
	}
	close := func() error {
		// An activation capture can outlive construction and must use the final
		// Session's retirement bookkeeping for this same generation.
		k.mu.RLock()
		owner := k.stateOwner
		k.mu.RUnlock()
		if owner != nil {
			return owner.releasePluginGeneration(generation)
		}
		if generation.Release() {
			return generation.closeErr
		}
		return nil
	}
	state := generation.state
	state.mu.Lock()
	documents, sessions, journal, active := state.documents, state.sessions, state.journal, state.status == pluginhost.StateActive
	state.mu.Unlock()
	if !active || documents == nil || sessions == nil {
		_ = close()
		return nil, serviceError("service_unavailable", "selected generation has no session state")
	}
	generation.revokedMu.Lock()
	prompt := generation.systemPrompt
	generation.revokedMu.Unlock()
	return &State{SystemPrompt: prompt, Documents: documents, Sessions: sessions, Generation: generation, Host: generation.host, InferenceJournalRuntime: journal, models: captureGenerationStateModels(generation), close: close}, nil
}

// The generation lease protects storage; this finite task also lets root
// shutdown join terminal accounting without waiting on unrelated State handles.
func (k *kernelHostServices) retainModelInvocation(ctx context.Context, cancel context.CancelFunc, continuation bool) (context.Context, *State, *modelInvocationOwner, error) {
	binding, err := k.retainState()
	if err != nil {
		return ctx, nil, nil, err
	}
	ctx, endParent, err := providers.BeginModelReceipt(ctx)
	if err != nil {
		_ = binding.Close()
		return ctx, nil, nil, err
	}
	receipts := providers.ModelReceiptScopeFromContext(ctx)
	ownsReceipts := !continuation || receipts == nil
	if ownsReceipts {
		ctx, receipts = providers.NewModelReceiptScope(ctx)
	}
	ctx = WithInvocationState(ctx, binding)
	owner, err := registerModelInvocation(ctx, binding, cancel, receipts, ownsReceipts, endParent)
	if err != nil {
		return ctx, nil, nil, err
	}
	return ctx, binding, owner, nil
}

// Agent turns and standalone forks own their execution independently of any
// receipt ticket inherited from the context that scheduled them.
func (k *kernelHostServices) beginModelReceipts(ctx context.Context) (context.Context, func() error, error) {
	binding, err := k.retainState()
	if err != nil {
		return ctx, nil, err
	}
	return beginModelReceipts(ctx, binding)
}

// BeginModelReceipts owns bounded inference cleanup on this captured authority.
// Complete after the logical call ends; a timeout retains State until producers
// actually finish. The returned context preserves cancellation and other values.
func (s *State) BeginModelReceipts(ctx context.Context) (context.Context, func() error, error) {
	if s != nil && s.Generation == nil {
		return ctx, nil, serviceError("service_unavailable", "inference requires a captured generation")
	}
	binding := s.Retain()
	if binding == nil {
		return ctx, nil, serviceError("generation_closed", "inference state is no longer available")
	}
	return beginModelReceipts(ctx, binding)
}

func beginModelReceipts(ctx context.Context, binding *State) (context.Context, func() error, error) {
	ctx, cancel := context.WithCancel(ctx)
	ctx, receipts := providers.NewModelReceiptScope(ctx)
	ctx = WithInvocationState(ctx, binding)
	owner, err := registerModelInvocation(ctx, binding, cancel, receipts, true, func(error) {})
	if err != nil {
		cancel()
		return ctx, nil, err
	}
	return ctx, func() error {
		defer cancel()
		// Turn completion includes bounded receipt cleanup even on cancellation.
		// The runner separately preserves its logical body error.
		return owner.complete(ctx, nil, nil)
	}, nil
}

func registerModelInvocation(ctx context.Context, binding *State, cancel context.CancelFunc, receipts *providers.ModelReceiptScope, ownsReceipts bool, endParent func(error)) (*modelInvocationOwner, error) {
	// Terminal receipts retain admitted authority independently of logical cancellation.
	// finish revokes this context only after the actual receipt producers drain.
	completionCtx, cancelCompletion := context.WithCancelCause(context.WithoutCancel(ctx))
	// Settlement RPCs start after logical admission is sealed. Give them their own
	// tickets while preserving the admitted identity and generation context.
	completionCtx, settlement := providers.NewModelReceiptScope(completionCtx)
	owner := &modelInvocationOwner{settlement: settlement, cancel: cancel, binding: binding, receipts: receipts, ownsReceipts: ownsReceipts, endParentReceipt: endParent, completionCtx: completionCtx, cancelCompletion: cancelCompletion}
	generation := binding.Generation
	var admissionErr error
	if selected := generation.composition; selected.scope != nil {
		selected.service.mu.Lock()
		if !selected.scope.published {
			admissionErr = serviceError("composition_unavailable", "execution composition is preparing")
		}
		selected.service.mu.Unlock()
	}
	generation.modelMu.Lock()
	if generation.modelStopped {
		admissionErr = serviceError("generation_closed", "model invocation admission is closed")
	}
	if admissionErr != nil {
		generation.modelMu.Unlock()
		if ownsReceipts {
			receipts.Seal()
		}
		endParent(nil)
		settlement.Seal()
		cancelCompletion(context.Canceled)
		_ = binding.Close()
		return nil, admissionErr
	}
	if generation.modelOwners == nil {
		generation.modelOwners = make(map[*modelInvocationOwner]struct{})
	}
	// All inference paths share this boundary, including native workers and
	// direct plugin calls. Explicit disposal drains; owner retirement cancels.
	if scope := generation.composition.scope; scope != nil {
		owner.stopScope = context.AfterFunc(scope.ctx, cancel)
		if scope.ctx.Err() != nil {
			cancel()
		}
	}
	generation.modelOwners[owner] = struct{}{}
	generation.modelBodies.Add(1)
	generation.modelInvocations.Add(1)
	generation.modelMu.Unlock()
	return owner, nil
}

func (o *modelInvocationOwner) abort(cause error) {
	o.cancelCompletion(cause)
	o.settlement.Abort(cause)
	if o.ownsReceipts {
		o.receipts.Abort(cause)
	}
}

func (o *modelInvocationOwner) complete(callCtx context.Context, execution *providers.InferenceExecution, bodyErr error) error {
	if !o.ownsReceipts {
		// A continuation cannot seal or join its still-running parent. Its
		// authority release nevertheless remains behind actual receipt quiescence.
		o.endParentReceipt(nil)
		return o.finish(callCtx, execution.JournalError())
	}
	o.receipts.Seal()
	finish := func() error {
		ctx, cancel := context.WithTimeout(o.completionCtx, modelShutdownTimeout)
		defer cancel()
		err := o.receipts.CheckpointSubmissions(ctx)
		o.settlement.Seal()
		err = errors.Join(err, o.settlement.Wait(ctx))
		if err != nil {
			err = errors.Join(err, context.Cause(o.completionCtx))
			o.abort(err)
		}
		return o.finish(callCtx, err)
	}
	if bodyErr != nil {
		select {
		case <-o.receipts.Done():
		default:
			// The caller receives logical cancellation/error immediately. This
			// counted owner continues bounded accounting on its captured authority.
			go func() { _ = finish() }()
			return nil
		}
	}
	return finish()
}

func (o *modelInvocationOwner) finish(callCtx context.Context, completionErr error) error {
	if o.stopScope != nil {
		o.stopScope()
	}
	o.settlement.Seal()
	generation := o.binding.Generation
	generation.recordModelCompletionError(completionErr)
	generation.modelMu.Lock()
	delete(generation.modelOwners, o)
	generation.modelMu.Unlock()
	generation.modelBodies.Done()
	release := func() error {
		o.endParentReceipt(completionErr)
		o.cancelCompletion(context.Canceled)
		closeState := func() error {
			defer generation.modelInvocations.Done()
			err := o.binding.Close()
			generation.recordModelCompletionError(err)
			return err
		}
		if pluginhost.DeferServiceCompletion(callCtx, closeState) {
			return nil
		}
		return closeState()
	}
	select {
	case <-o.receipts.Done():
		select {
		case <-o.settlement.Done():
			return errors.Join(completionErr, release())
		default:
		}
	default:
	}
	// A deadline ends the caller's wait, not the native producer. Keep State
	// alive until real tickets finish, even if an uncooperative source lingers.
	go func() {
		<-o.receipts.Done()
		<-o.settlement.Done()
		_ = release()
	}()
	return completionErr
}

// Keep the invocation's own reference through the callback and its service
// chain. The last release must not close a registry that is still dispatching it.
func (k *kernelHostServices) captureInvocationState(ctx context.Context) (context.Context, func() error) {
	var mu sync.Mutex
	var binding *State
	var captureErr error
	var initialized, closed bool
	ctx = context.WithValue(ctx, invocationStateKey{}, invocationStateRetainer(func() (*State, error) {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return nil, serviceError("generation_closed", "invocation has completed")
		}
		if !initialized {
			binding, captureErr = k.retainState()
			initialized = true
		}
		if captureErr != nil {
			return nil, captureErr
		}
		return binding.Retain(), nil
	}))
	return ctx, func() error {
		mu.Lock()
		closed = true
		held := binding
		mu.Unlock()
		if held != nil {
			return completeInvocationState(ctx, held)
		}
		return nil
	}
}

func completeInvocationState(ctx context.Context, binding *State) error {
	if pluginhost.DeferServiceCompletion(ctx, binding.Close) {
		return nil
	}
	return binding.Close()
}
