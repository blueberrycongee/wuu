package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/personalcalendar"
	"github.com/blueberrycongee/wuu/internal/personalconnectors"
	"github.com/blueberrycongee/wuu/internal/personalhost"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const (
	PersonalAccountsService  = "personal.accounts"
	PersonalAccountsModule   = "wuu.personal-accounts"
	PersonalAccountsConsumer = "wuu.personal-account-client"
)

// PersonalAccountsFactory creates resources for one selected generation. It
// must return fresh resources: their credentials, OAuth attempts and cleanup
// belong to that generation, never to an embedding host's latest graph.
type PersonalAccountsFactory func(runtime.NativeProviderContext) (personalhost.Options, error)

// PersonalAccountsDescriptor uses the same verified operation vocabulary as
// personal.host. The transport resolves session identity and owner admission
// before dispatch; input fields never confer that authority.
func PersonalAccountsDescriptor() pluginhost.ServiceDescriptor {
	return pluginhost.ServiceDescriptor{Name: PersonalAccountsService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "call", InputSchema: "personal.accounts.call.input.v1", OutputSchema: "personal.accounts.call.output.v1"}}}
}

// PersonalAccountsResult preserves business failures across native/process
// providers; transport and admission failures remain service errors.
type PersonalAccountsResult struct {
	Value          json.RawMessage             `json:"value,omitempty"`
	CalendarError  *personalcalendar.Failure   `json:"calendar_error,omitempty"`
	ConnectorError *personalconnectors.Failure `json:"connector_error,omitempty"`
}

type personalAccountsScopeKey struct{}
type personalAccountsProvider struct {
	mu             sync.Mutex
	input          runtime.NativeProviderContext
	factory        PersonalAccountsFactory
	host           *personalhost.Host
	server         *Server
	active, closed bool
	calls          sync.WaitGroup
	stopping, done chan struct{}
	closeErr       error
}

func newPersonalAccountsProvider(input runtime.NativeProviderContext, factory PersonalAccountsFactory) *personalAccountsProvider {
	return &personalAccountsProvider{input: input, factory: factory, stopping: make(chan struct{}), done: make(chan struct{})}
}
func (*personalAccountsProvider) ID() string { return PersonalAccountsModule }
func (p *personalAccountsProvider) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := pluginhost.StatePrepared
	if p.active {
		state = pluginhost.StateActive
	}
	if p.closed {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: p.ID(), State: state}
}
func (*personalAccountsProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{PersonalAccountsDescriptor()}
}
func (*personalAccountsProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: credentialstore.ServiceName, MajorVersion: 1, Required: true}, {Name: PersonalCoordinationService, MajorVersion: 1}}
}
func (p *personalAccountsProvider) Activate(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.closed {
		return errors.New("Personal account provider is closed")
	}
	p.active = true
	return nil
}

// Close is the dependency-release barrier. The host may stop observing its
// shutdown deadline, but credentials must outlive every broker cleanup callback.
func (p *personalAccountsProvider) Close(_ context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.active = false
		close(p.stopping)
		go func() {
			p.calls.Wait()
			if p.host != nil {
				p.closeErr = p.host.Close()
			}
			close(p.done)
		}()
	}
	done := p.done
	p.mu.Unlock()
	<-done
	return p.closeErr
}
func (p *personalAccountsProvider) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if call.Service != PersonalAccountsService || call.Method != "call" {
		return nil, errors.New("unknown Personal account operation")
	}
	var input runtime.PersonalHostCall
	if err := json.Unmarshal(call.Params, &input); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(input.Operation, "calendar.") && !strings.HasPrefix(input.Operation, "connectors.") {
		return nil, errors.New("Personal account operation is unavailable")
	}
	server, _ := ctx.Value(personalAccountsScopeKey{}).(*Server)
	if server == nil && call.CallerScope != nil && call.CallerScope.Context != nil {
		server, _ = call.CallerScope.Context.Value(personalAccountsScopeKey{}).(*Server)
	}
	if server == nil {
		return nil, errors.New("Personal accounts require a captured host invocation")
	}
	state, err := runtime.RetainProductInvocationState(ctx)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	if state.Host == nil || state.Host.ServiceRegistry() != p.input.Registry {
		return nil, errors.New("Personal account invocation belongs to another generation")
	}
	ctx = runtime.WithInvocationState(ctx, state)
	p.mu.Lock()
	if !p.active || p.closed {
		p.mu.Unlock()
		return nil, errors.New("Personal account provider is unavailable")
	}
	if p.host == nil {
		options := personalhost.Options{}
		if p.factory != nil {
			options, err = p.factory(p.input)
		}
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		additionalAuthorization := options.Authorize
		options.Authorize = func(ctx context.Context, operation, sessionID string) error {
			if err := server.authorizePersonalOperation(ctx, operation, sessionID); err != nil {
				return err
			}
			if additionalAuthorization != nil {
				return additionalAuthorization(ctx, operation, sessionID)
			}
			return nil
		}
		if broker, ok := options.Connectors.(*personalconnectors.Broker); ok {
			broker.SetAuthorization(server.connectorSnapshot)
			broker.SetAuthorizationBoundary(server.connectorCommit)
		}
		p.host = personalhost.New(p.input.UserBinding, options)
		p.server = server
	}
	if p.server != server {
		p.mu.Unlock()
		return nil, errors.New("Personal account provider belongs to another host")
	}
	host := p.host
	p.calls.Add(1)
	p.mu.Unlock()
	defer p.calls.Done()
	value, err := host.Call(ctx, input.Operation, input.Input, input.SessionID)
	result := PersonalAccountsResult{Value: value}
	if err != nil && !errors.As(err, &result.CalendarError) && !errors.As(err, &result.ConnectorError) {
		return nil, err
	}
	return json.Marshal(result)
}

type personalAccountsConsumer struct{ personalActionsConsumer }

func (*personalAccountsConsumer) ID() string { return PersonalAccountsConsumer }
func (p *personalAccountsConsumer) Status() pluginhost.Status {
	s := p.personalActionsConsumer.Status()
	s.ID = p.ID()
	return s
}
func (*personalAccountsConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: PersonalAccountsService, MajorVersion: 1}}
}

func (s *Server) callPersonalAccounts(ctx context.Context, operation string, raw json.RawMessage, sessionID string) (json.RawMessage, error) {
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
		return nil, errors.New("selected Personal account provider is unavailable")
	}
	ctx = context.WithValue(runtime.WithInvocationState(ctx, state), personalAccountsScopeKey{}, s)
	if s.personalEffects != nil {
		ctx = s.personalEffects.Context(ctx, s, state, nil)
	}
	input, err := json.Marshal(runtime.PersonalHostCall{Operation: operation, Input: raw, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	output, callErr := state.Host.CallService(ctx, PersonalAccountsConsumer, PersonalAccountsService, "call", input)
	if callErr != nil {
		return nil, callErr
	}
	var result PersonalAccountsResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, err
	}
	if result.CalendarError != nil {
		return nil, result.CalendarError
	}
	if result.ConnectorError != nil {
		return nil, result.ConnectorError
	}
	if len(result.Value) == 0 {
		return nil, errors.New("Personal account provider returned no value")
	}
	return result.Value, nil
}
