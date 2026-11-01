package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/clientauthority"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalim"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const PersonalMessagingService = runtime.PersonalMessagingService
const PersonalMessagingModule = "wuu.personal-messaging"
const PersonalMessagingConsumer = runtime.PersonalMessagingConsumer

// PersonalMessagingFactory binds selected generation resources to an already
// initialized user host. No transport starts during graph preparation.
type PersonalMessagingFactory func(context.Context, runtime.NativeProviderContext, *UserHost, func() bool) (*personalim.Bridge, error)

type personalMessagingProvider struct {
	watch          sync.WaitGroup
	watchCancel    context.CancelFunc
	startErr       error
	input          runtime.NativeProviderContext
	bindings       *RuntimeBindings
	mu             sync.Mutex
	bridge         *personalim.Bridge
	active, closed bool
	calls          sync.WaitGroup
	done           chan struct{}
	closeErr       error
}

func newPersonalMessagingProvider(input runtime.NativeProviderContext, bindings *RuntimeBindings) *personalMessagingProvider {
	return &personalMessagingProvider{input: input, bindings: bindings, done: make(chan struct{})}
}
func (*personalMessagingProvider) ID() string { return PersonalMessagingModule }
func (p *personalMessagingProvider) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := pluginhost.StatePrepared
	if p.active {
		state = pluginhost.StateActive
	}
	if p.closed {
		state = pluginhost.StateStopped
	}
	if p.startErr != nil && !p.closed {
		return pluginhost.Status{ID: p.ID(), State: pluginhost.StateFailed, Error: p.startErr.Error()}
	}
	return pluginhost.Status{ID: p.ID(), State: state}
}
func (*personalMessagingProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: PersonalMessagingService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "call", InputSchema: "personal.messaging.call.input.v1", OutputSchema: "personal.messaging.call.output.v1"}}}}
}
func (*personalMessagingProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return append([]pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}, {Name: credentialstore.ServiceName, MajorVersion: 1, Required: true}, {Name: executionauth.ServiceName, MajorVersion: 1, Required: true}}, pluginhost.KernelServiceRequirements(pluginhost.KernelPersonalService, pluginhost.KernelExecutionAuthorizationService)...)
}
func (p *personalMessagingProvider) Activate(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("Personal messaging provider is closed")
	}
	p.active = true
	p.mu.Unlock()
	if p.input.UserBinding.Domain() != "personal" {
		return nil
	}
	bindings := p.bindings
	bindings.messagingMu.Lock()
	if bindings.messagingClosed {
		bindings.messagingMu.Unlock()
		return errors.New("Personal messaging host is closed")
	}
	host := bindings.messagingHost
	if host == nil {
		bindings.messagingProvider = p
		bindings.messagingMu.Unlock()
		return nil
	}
	bindings.messagingMu.Unlock()
	lifetime, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		cancel()
		return errors.New("Personal messaging provider is closed")
	}
	p.watchCancel = cancel
	p.watch.Add(1)
	p.mu.Unlock()
	// Activation precedes publication. This short-lived barrier never starts
	// network consumers against the still-published predecessor.
	go func() {
		defer p.watch.Done()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			if p.generationPublished(host) {
				err := bindings.selectPersonalMessaging(lifetime, p, host)
				if err != nil {
					p.mu.Lock()
					p.startErr = err
					p.mu.Unlock()
				}
				return
			}
			select {
			case <-lifetime.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}
func (p *personalMessagingProvider) generationPublished(host *UserHost) bool {
	snapshot := host.runtime.ExtensionSnapshot()
	return snapshot.PluginHost != nil && snapshot.PluginHost.ServiceRegistry() == p.input.Registry
}
func (p *personalMessagingProvider) published(host *UserHost) bool {
	if !p.generationPublished(host) {
		return false
	}
	selected, ok := p.input.Registry.ProviderPluginIDFor(PersonalMessagingConsumer, PersonalMessagingService, 1)
	return ok && selected == p.ID()
}
func (b *RuntimeBindings) selectPersonalMessaging(ctx context.Context, p *personalMessagingProvider, host *UserHost) error {
	if !p.published(host) {
		return nil
	}
	b.messagingMu.Lock()
	if b.messagingClosed {
		b.messagingMu.Unlock()
		return errors.New("Personal messaging host is closed")
	}
	prior := b.messagingProvider
	b.messagingProvider = p
	b.messagingMu.Unlock()
	if prior != nil && prior != p {
		if err := prior.Close(ctx); err != nil {
			return err
		}
	}
	b.messagingMu.Lock()
	current := b.messagingProvider == p
	b.messagingMu.Unlock()
	if !current || !p.published(host) {
		return nil
	}
	return p.start(ctx, host)
}
func (p *personalMessagingProvider) start(ctx context.Context, host *UserHost) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || !p.active {
		return errors.New("Personal messaging provider is unavailable")
	}
	if p.bridge != nil || p.bindings.personalMessaging == nil {
		return nil
	}
	bridge, err := p.bindings.personalMessaging(ctx, p.input, host, func() bool { return p.published(host) })
	if err != nil {
		return err
	}
	if err = bridge.Start(ctx); err != nil {
		_ = bridge.Close()
		return err
	}
	p.bridge = bridge
	return nil
}
func (p *personalMessagingProvider) Close(context.Context) error {
	p.mu.Lock()
	if p.closed {
		done := p.done
		p.mu.Unlock()
		<-done
		return p.closeErr
	}
	p.closed = true
	p.active = false
	if p.watchCancel != nil {
		p.watchCancel()
	}
	p.mu.Unlock()
	p.watch.Wait()
	p.calls.Wait()
	if p.bridge != nil {
		p.closeErr = p.bridge.Close()
	}
	close(p.done)
	return p.closeErr
}
func (p *personalMessagingProvider) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if call.Service != PersonalMessagingService || call.Method != "call" {
		return nil, errors.New("unknown Personal messaging operation")
	}
	if err := clientauthority.RequireOwner(ctx, p.input.UserBinding); err != nil {
		return nil, err
	}
	var input runtime.PersonalHostCall
	if err := json.Unmarshal(call.Params, &input); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(input.Operation, "im.") {
		return nil, errors.New("unknown Personal messaging operation")
	}
	p.mu.Lock()
	if p.closed || !p.active || p.bridge == nil {
		p.mu.Unlock()
		return nil, errors.New("Personal messaging provider is unavailable")
	}
	bridge := p.bridge
	p.calls.Add(1)
	p.mu.Unlock()
	defer p.calls.Done()
	return bridge.Call(ctx, input.Operation, input.Input)
}

type personalMessagingConsumer struct{ personalActionsConsumer }

func (*personalMessagingConsumer) ID() string { return PersonalMessagingConsumer }
func (p *personalMessagingConsumer) Status() pluginhost.Status {
	s := p.personalActionsConsumer.Status()
	s.ID = p.ID()
	return s
}
func (*personalMessagingConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: PersonalMessagingService, MajorVersion: 1}}
}

func (b *RuntimeBindings) SetPersonalMessagingFactory(factory PersonalMessagingFactory) {
	b.personalMessaging = factory
}

// BindPersonalMessagingHost opens the cold-start barrier after Personal routing
// is initialized. It does not require a renderer or another client connection.
func (b *RuntimeBindings) BindPersonalMessagingHost(ctx context.Context, host *UserHost) error {
	b.messagingMu.Lock()
	defer b.messagingMu.Unlock()
	if b.messagingClosed {
		return errors.New("Personal messaging host is closed")
	}
	if b.messagingHost != nil && b.messagingHost != host {
		return errors.New("messaging binding belongs to another user host")
	}
	b.messagingHost = host
	host.stopPersonalMessaging = b.ClosePersonalMessaging
	if b.messagingProvider != nil && b.messagingProvider.published(host) {
		return b.messagingProvider.start(ctx, host)
	}
	return nil
}
func (b *RuntimeBindings) ClosePersonalMessaging() error {
	b.messagingMu.Lock()
	b.messagingClosed = true
	provider := b.messagingProvider
	b.messagingMu.Unlock()
	if provider != nil {
		return provider.Close(context.Background())
	}
	return nil
}

func (s *Server) callPersonalMessaging(ctx context.Context, operation string, raw json.RawMessage, sessionID string) (json.RawMessage, error) {
	var state *runtime.State
	var err error
	if runtime.HasInvocationState(ctx) {
		state, err = runtime.RetainProductInvocationState(ctx)
	} else {
		state, err = s.rt.RetainState()
	}
	if err != nil {
		return nil, err
	}
	defer state.Close()
	if state.Host == nil {
		return nil, errors.New("selected Personal messaging provider is unavailable")
	}
	input, err := json.Marshal(runtime.PersonalHostCall{Operation: operation, Input: raw, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	output, callErr := state.Host.CallService(runtime.WithInvocationState(ctx, state), PersonalMessagingConsumer, PersonalMessagingService, "call", input)
	if callErr != nil {
		return nil, callErr
	}
	return output, nil
}
