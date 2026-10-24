package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// ProjectCoordinatorBridge owns connection registrations, never coordination
// policy or state. A pass captures one registration and one graph authority.
type ProjectCoordinatorBridge struct {
	mu            sync.Mutex
	next          uint64
	registrations []*projectServerRegistration
	changed       chan struct{}
}
type projectServerRegistration struct {
	bridge *ProjectCoordinatorBridge
	server *Server
	token  uint64
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	passes sync.WaitGroup
	once   sync.Once
}

func NewProjectCoordinatorBridge() *ProjectCoordinatorBridge {
	return &ProjectCoordinatorBridge{changed: make(chan struct{})}
}
func (b *ProjectCoordinatorBridge) signalLocked() { close(b.changed); b.changed = make(chan struct{}) }
func (b *ProjectCoordinatorBridge) changeSignal() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed
}

// Bind returns an idempotent unbind for this connection only. In-flight calls
// keep their original registration; another connection never replaces it.
func (b *ProjectCoordinatorBridge) Bind(server *Server) (func(), error) {
	if server == nil || server.closed.Load() {
		return nil, errors.New("project connection is closed")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if server.projectRegistration != nil {
		return nil, errors.New("project connection is already registered")
	}
	b.next++
	ctx, cancel := context.WithCancel(context.Background())
	registration := &projectServerRegistration{bridge: b, server: server, token: b.next, ctx: ctx, cancel: cancel}
	server.projectRegistration = registration
	b.registrations = append(b.registrations, registration)
	b.signalLocked()
	return registration.Close, nil
}
func (r *projectServerRegistration) Close() {
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
		b.signalLocked()
		b.mu.Unlock()
		r.passes.Wait()
		// Recovery must also run after canceled passes remove their queue entries.
		b.mu.Lock()
		b.signalLocked()
		b.mu.Unlock()
	})
}
func (r *projectServerRegistration) live() bool {
	r.bridge.mu.Lock()
	defer r.bridge.mu.Unlock()
	return !r.closed
}

func (b *ProjectCoordinatorBridge) acquireBackground(parent context.Context, registry *pluginhost.ServiceRegistry, retain func() (*runtime.State, error)) (*projectCallScope, context.Context, func(), error) {
	b.mu.Lock()
	var registration *projectServerRegistration
	for _, candidate := range b.registrations {
		if !candidate.closed && !candidate.server.closed.Load() {
			registration = candidate
			candidate.passes.Add(1)
			break
		}
	}
	b.mu.Unlock()
	if registration == nil {
		return nil, nil, nil, errors.New("no live project connection")
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(registration.ctx, cancel)
	state, err := retain()
	if err == nil && (state == nil || state.Host == nil || state.Host.ServiceRegistry() != registry) {
		err = errors.New("project background state belongs to another graph")
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
	scope := &projectCallScope{registration: registration, state: state}
	ctx = context.WithValue(ctx, projectCallScopeKey{}, scope)
	var once sync.Once
	release := func() { once.Do(func() { stop(); cancel(); _ = state.Close(); registration.passes.Done() }) }
	return scope, ctx, release, nil
}

func (b *ProjectCoordinatorBridge) ProviderFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: ProjectCoordinationModule, DefaultEnabled: projectAgentEnabled, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
		if input.Registry == nil || input.RetainState == nil {
			return nil, errors.New("project coordination requires a captured service graph and state factory")
		}
		p := newProjectCoordinationProvider(projectProviderStore(input.Registry, ProjectCoordinationModule))
		p.registry, p.bridge, p.retainState = input.Registry, b, input.RetainState
		return p, nil
	}}
}
func (p *ProjectCoordinationProvider) activate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.lifetime.backgroundMu.Lock()
	if p.lifetime.closed.Load() {
		p.lifetime.backgroundMu.Unlock()
		return errors.New("project provider has stopped")
	}
	p.lifetime.active.Store(true)
	p.lifetime.backgroundMu.Unlock()
	return p.startScheduler()
}
func ProjectCoordinatorConsumerFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: ProjectCoordinationConsumer, DefaultEnabled: projectAgentEnabled, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return NewProjectCoordinationConsumer(), nil
	}}
}
func (p *ProjectCoordinationProvider) startScheduler() error {
	p.lifetime.backgroundMu.Lock()
	if p.lifetime.schedulerStarted || p.lifetime.closed.Load() || p.lifetime.ctx.Err() != nil || !p.selected() {
		p.lifetime.backgroundMu.Unlock()
		return nil
	}
	p.lifetime.schedulerStarted = true
	p.lifetime.backgroundMu.Unlock()
	input, _ := json.Marshal(pluginhost.BackgroundStartParams{Key: "project-scheduler", Service: ProjectCoordinationService, Method: "scheduler"})
	_, err := p.registry.Call(p.lifetime.ctx, p.ID(), pluginhost.ServiceCallParams{Service: pluginhost.KernelBackgroundService, Method: "start", Params: input})
	if err != nil {
		p.stopScheduling()
		return err
	}
	return nil
}
func (p *ProjectCoordinationProvider) runScheduler(ctx context.Context) {
	defer p.stopScheduling()
	for {
		changed := p.bridge.changeSignal()
		admitted := p.startBackground(func(operation *ProjectCoordinationProvider) { operation.startProjectRecovery() })
		if admitted {
			select {
			case <-ctx.Done():
				return
			case <-p.lifetime.ctx.Done():
				return
			case <-changed:
			}
			continue
		}
		// A prepared graph can activate before publication or before a connection
		// binds. Retry admission without borrowing the previous graph's state.
		retry := time.NewTimer(threadExecutionLeaseRetryDelay)
		select {
		case <-ctx.Done():
			retry.Stop()
			return
		case <-p.lifetime.ctx.Done():
			retry.Stop()
			return
		case <-changed:
			retry.Stop()
		case <-retry.C:
		}
	}
}
func (p *ProjectCoordinationProvider) stopScheduling() {
	p.lifetime.cancel()
	p.closeProjectInboxDrains()
}
