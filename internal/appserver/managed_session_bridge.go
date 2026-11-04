package appserver

import (
	"context"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// managedSessionBridge owns connection registrations, never coordination
// policy or state. A pass captures one registration and one graph authority.
type managedSessionBridge struct {
	mu            sync.Mutex
	registrations []*managedSessionRegistration
}
type managedSessionRegistration struct {
	bridge *managedSessionBridge
	server *Server
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	passes sync.WaitGroup
	once   sync.Once
}

func newManagedSessionBridge() *managedSessionBridge {
	return &managedSessionBridge{}
}

// Bind returns an idempotent unbind for this connection only. In-flight calls
// keep their original registration; another connection never replaces it.
func (b *managedSessionBridge) Bind(server *Server) (func(), error) {
	if server == nil || server.closed.Load() {
		return nil, errors.New("managed session connection is closed")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if server.managedSessionRegistration != nil {
		return nil, errors.New("managed session connection is already registered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	registration := &managedSessionRegistration{bridge: b, server: server, ctx: ctx, cancel: cancel}
	server.managedSessionRegistration = registration
	b.registrations = append(b.registrations, registration)
	return registration.Close, nil
}
func (r *managedSessionRegistration) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		b := r.bridge
		b.mu.Lock()
		r.closed = true
		r.cancel()
		for i, current := range b.registrations {
			if current == r {
				b.registrations = append(b.registrations[:i], b.registrations[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
		r.passes.Wait()
	})
}
func (r *managedSessionRegistration) live() bool {
	r.bridge.mu.Lock()
	defer r.bridge.mu.Unlock()
	return !r.closed
}

func (b *managedSessionBridge) acquireBackground(parent context.Context, registry *pluginhost.ServiceRegistry, retain func() (*runtime.State, error)) (*managedSessionCallScope, context.Context, func(), error) {
	b.mu.Lock()
	var registration *managedSessionRegistration
	for _, candidate := range b.registrations {
		if !candidate.closed && !candidate.server.closed.Load() {
			registration = candidate
			candidate.passes.Add(1)
			break
		}
	}
	b.mu.Unlock()
	if registration == nil {
		return nil, nil, nil, errors.New("no live managed session connection")
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(registration.ctx, cancel)
	state, err := retain()
	if err == nil && (state == nil || state.Host == nil || state.Host.ServiceRegistry() != registry) {
		err = errors.New("managed session background state belongs to another graph")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if state != nil {
			_ = state.Close()
		}
		stop()
		cancel()
		registration.passes.Done()
		return nil, nil, nil, err
	}
	scope := &managedSessionCallScope{registration: registration, state: state}
	ctx = context.WithValue(ctx, managedSessionCallScopeKey{}, scope)
	var once sync.Once
	release := func() { once.Do(func() { stop(); cancel(); _ = state.Close(); registration.passes.Done() }) }
	return scope, ctx, release, nil
}
