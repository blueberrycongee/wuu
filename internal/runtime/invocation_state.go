package runtime

import (
	"context"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type invocationStateKey struct{}
type invocationStateRetainer func() (*State, error)

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

// bindStateGeneration binds one graph before effectful lifecycle callbacks.
// Its Session owner may be attached after construction, without changing graphs.
func (k *kernelHostServices) bindStateGeneration(generation *PluginGeneration, owner *Session) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stateGeneration, k.stateOwner = generation, owner
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

// Keep the invocation's own reference outside the service callback lock. A
// callback may release its last client handle while publication drops the graph
// owner; shutting down the kernel inside that callback would lock against itself.
func (k *kernelHostServices) captureInvocationState(ctx context.Context) (context.Context, func()) {
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
	return ctx, func() {
		mu.Lock()
		closed = true
		held := binding
		mu.Unlock()
		if held != nil {
			_ = held.Close()
		}
	}
}
