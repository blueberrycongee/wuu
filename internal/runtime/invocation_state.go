package runtime

import (
	"context"

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

// bindStateGeneration is called once after constructing this kernel's graph.
// The graph owner must retain it before opening effectful lifecycle callbacks.
func (k *kernelHostServices) bindStateGeneration(generation *PluginGeneration, owner *Session) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.stateGeneration, k.stateOwner = generation, owner
}

func (k *kernelHostServices) retainInvocationState() (*State, error) {
	k.mu.RLock()
	generation, owner := k.stateGeneration, k.stateOwner
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
		if owner != nil {
			owner.releasePluginGeneration(generation)
		} else {
			generation.Release()
		}
		return generation.closeErr
	}
	state := generation.state
	state.mu.Lock()
	documents, sessions, journal, active := state.documents, state.sessions, state.journal, state.status == pluginhost.StateActive
	state.mu.Unlock()
	if !active || documents == nil || sessions == nil {
		_ = close()
		return nil, serviceError("service_unavailable", "selected generation has no session state")
	}
	return &State{Documents: documents, Sessions: sessions, Generation: generation, Host: generation.host, InferenceJournalRuntime: journal, close: close}, nil
}
