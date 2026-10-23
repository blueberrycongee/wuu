package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// ProjectCoordinatorBridge supplies managed-session mechanisms after Server
// construction. Each Build creates new business state; the bridge never owns
// project plans, timers, queues, persistence, or provider selection.
type ProjectCoordinatorBridge struct {
	mu      sync.Mutex
	server  *Server
	pending map[*ProjectCoordinationProvider]struct{}
}

func NewProjectCoordinatorBridge() *ProjectCoordinatorBridge {
	return &ProjectCoordinatorBridge{pending: make(map[*ProjectCoordinationProvider]struct{})}
}
func (b *ProjectCoordinatorBridge) ProviderFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: ProjectCoordinationModule, DefaultEnabled: projectAgentEnabled, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
		if input.Registry == nil {
			return nil, errors.New("project coordination requires a captured service graph")
		}
		p := newProjectCoordinationProvider(projectProviderStore(input.Registry, ProjectCoordinationModule))
		p.registry = input.Registry
		b.mu.Lock()
		server := b.server
		if server == nil {
			b.pending[p] = struct{}{}
		}
		b.mu.Unlock()
		p.lifetime.onClose = func() { b.mu.Lock(); delete(b.pending, p); b.mu.Unlock() }
		if server != nil {
			if err := p.bindServer(server); err != nil {
				return nil, err
			}
		}
		return p, nil
	}}
}
func (b *ProjectCoordinatorBridge) Bind(server *Server) error {
	b.mu.Lock()
	if b.server != nil && b.server != server {
		b.mu.Unlock()
		return errors.New("project coordinator bridge already bound")
	}
	b.server = server
	pending := b.pending
	b.pending = make(map[*ProjectCoordinationProvider]struct{})
	b.mu.Unlock()
	var result error
	for p := range pending {
		result = errors.Join(result, p.bindServer(server))
	}
	return result
}
func (p *ProjectCoordinationProvider) bindServer(server *Server) error {
	p.lifetime.backgroundMu.Lock()
	if p.lifetime.closed.Load() {
		p.lifetime.backgroundMu.Unlock()
		return nil
	}
	p.Server = server
	active := p.lifetime.active.Load()
	p.lifetime.backgroundMu.Unlock()
	if active {
		return p.startScheduler()
	}
	return nil
}

// A prepared graph can activate before the app-server binds its mechanisms.
// Autonomous recovery starts only once both halves are ready.
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
	bound := p.Server != nil
	p.lifetime.backgroundMu.Unlock()
	if bound {
		return p.startScheduler()
	}
	return nil
}

// ProjectCoordinatorConsumerFactory declares application use independently of
// whichever coordinator implementation is configured for this generation.
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

// Stopping autonomous work does not retire this provider: admitted foreground
// requests retain their generation and invocation-scoped store until release.
func (p *ProjectCoordinationProvider) stopScheduling() {
	p.lifetime.cancel()
	p.closeProjectInboxDrains()
}
