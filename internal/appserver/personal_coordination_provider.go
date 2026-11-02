package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const PersonalCoordinationService = "personal.coordination"
const PersonalCoordinationConsumer = "wuu.personal-client"

func PersonalCoordinationDescriptor() pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"initialize", "drain", "call", "file-gate", "scope", "cancel", "inspection", "connector-snapshot", "connector-commit"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: "personal.coordination." + name + ".input.v1", OutputSchema: "personal.coordination." + name + ".output.v1"})
	}
	return pluginhost.ServiceDescriptor{Name: PersonalCoordinationService, Version: "1.0.0", Methods: methods}
}

type personalCoordinationRequest struct {
	Operation string                 `json:"operation,omitempty"`
	Input     json.RawMessage        `json:"input,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	Call      providers.ToolCall     `json:"call,omitempty"`
	Scopes    []personalreview.Scope `json:"scopes,omitempty"`
}

type personalCoordinationScopeKey struct{}
type personalCoordinationScope struct {
	server *Server
	state  *runtime.State
	kit    *tools.Toolkit
}

func personalCoordinationScopeForCall(ctx context.Context, call pluginhost.ServiceInvokeParams) (*personalCoordinationScope, error) {
	scope, _ := ctx.Value(personalCoordinationScopeKey{}).(*personalCoordinationScope)
	if scope == nil && call.CallerScope != nil && call.CallerScope.Context != nil {
		scope, _ = call.CallerScope.Context.Value(personalCoordinationScopeKey{}).(*personalCoordinationScope)
	}
	if scope == nil || scope.server == nil || scope.state == nil {
		return nil, errors.New("Personal coordination requires a captured host invocation")
	}
	return scope, nil
}

// Compatible native generations share pending admissions, not mutable service
// pointers. Each operation still carries its own immutable invocation State.
// The last provider releases the core; an older generation cannot close a core
// already borrowed by its successor.
type personalCoordinatorCore struct {
	actions *personalActions
	refs    int
	closing bool
	done    chan struct{}
	err     error
}
type personalCoordinatorBridge struct {
	mu        sync.Mutex
	core      *personalCoordinatorCore
	providers map[*personalCoordinationProvider]bool
	calls     sync.WaitGroup
	closed    bool
}

func newPersonalCoordinatorBridge() *personalCoordinatorBridge {
	return &personalCoordinatorBridge{providers: map[*personalCoordinationProvider]bool{}}
}
func (b *personalCoordinatorBridge) acquire(ctx context.Context, p *personalCoordinationProvider, scope *personalCoordinationScope) (*personalActions, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return nil, errors.New("Personal host is closed")
		}
		core := b.core
		if core != nil && core.closing {
			done := core.done
			b.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if core == nil {
			actions, err := newPersonalActionsForState(scope.server, scope.state)
			if err != nil {
				b.mu.Unlock()
				return nil, err
			}
			core = &personalCoordinatorCore{actions: actions, done: make(chan struct{})}
			b.core = core
			actions.start()
		}
		if core.actions.server != scope.server {
			b.mu.Unlock()
			return nil, errors.New("Personal coordinator belongs to another host")
		}
		if !b.providers[p] {
			b.providers[p] = true
			core.refs++
		}
		b.calls.Add(1)
		b.mu.Unlock()
		return core.actions, nil
	}
}
func (b *personalCoordinatorBridge) release(p *personalCoordinationProvider, cleanup ...context.Context) error {
	b.mu.Lock()
	if !b.providers[p] {
		b.mu.Unlock()
		return nil
	}
	delete(b.providers, p)
	core := b.core
	core.refs--
	if core.refs != 0 || core.closing {
		b.mu.Unlock()
		return nil
	}
	core.closing = true
	b.mu.Unlock()
	core.err = core.actions.close(cleanup...)
	b.mu.Lock()
	if b.core == core {
		b.core = nil
	}
	close(core.done)
	b.mu.Unlock()
	return core.err
}
func (b *personalCoordinatorBridge) Close() error {
	b.mu.Lock()
	b.closed = true
	core := b.core
	if core != nil {
		core.actions.cancel()
	}
	providers := make([]*personalCoordinationProvider, 0, len(b.providers))
	for p := range b.providers {
		providers = append(providers, p)
	}
	b.mu.Unlock()
	b.calls.Wait()
	var err error
	for _, p := range providers {
		err = errors.Join(err, p.detach(b))
	}
	if core != nil {
		<-core.done
		err = errors.Join(err, core.err)
	}
	return err
}

type personalCoordinationProvider struct {
	personalActionsConsumer
	input                runtime.NativeProviderContext
	mu                   sync.Mutex
	bridge               *personalCoordinatorBridge
	calls                sync.WaitGroup
	ctx                  context.Context
	cancel               context.CancelFunc
	closed               bool
	closeErr             error
	retirementRegistered bool
}

func newPersonalCoordinationProvider(input runtime.NativeProviderContext) *personalCoordinationProvider {
	ctx, cancel := context.WithCancel(context.Background())
	return &personalCoordinationProvider{input: input, ctx: ctx, cancel: cancel}
}
func (p *personalCoordinationProvider) Status() pluginhost.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := pluginhost.StatePrepared
	if p.active.Load() {
		state = pluginhost.StateActive
	}
	if p.closed {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: p.ID(), State: state}
}
func (p *personalCoordinationProvider) Activate(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.closed {
		return errors.New("Personal coordinator is closed")
	}
	p.active.Store(true)
	return nil
}
func (*personalCoordinationProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{PersonalCoordinationDescriptor()}
}

// A transport host can detach without retiring its still-selected generation.
// Compare the registration before clearing it, so old host cleanup cannot unbind
// a subsequent host using the same immutable provider instance.
func (p *personalCoordinationProvider) detach(bridge *personalCoordinatorBridge) error {
	p.mu.Lock()
	if p.bridge == bridge {
		p.bridge = nil
	}
	p.mu.Unlock()
	return bridge.release(p)
}

func (p *personalCoordinationProvider) Close(ctx context.Context) error {
	p.mu.Lock()
	// Releasing the core's final retained State can reenter provider retirement.
	// The first closer owns cleanup; a reentrant close must not wait on itself.
	if p.closed {
		err := p.closeErr
		p.mu.Unlock()
		return err
	}
	p.closed = true
	p.active.Store(false)
	p.cancel()
	bridge := p.bridge
	p.mu.Unlock()
	p.calls.Wait()
	var err error
	if bridge != nil {
		err = bridge.release(p, ctx)
	}
	p.mu.Lock()
	p.closeErr = err
	p.mu.Unlock()
	return err
}
func (p *personalCoordinationProvider) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (result json.RawMessage, err error) {
	defer func() {
		if errors.Is(err, personalreview.ErrConflict) {
			err = &pluginhost.HostServiceError{Code: "personal_review_conflict", Message: err.Error()}
		}
		if errors.Is(err, persistence.ErrNamespaceOwnershipUnsupported) {
			err = &pluginhost.HostServiceError{Code: "personal_namespace_unsupported", Message: err.Error()}
		}
	}()
	if call.Service != PersonalCoordinationService || call.Caller != PersonalCoordinationConsumer {
		return nil, errors.New("Personal coordination service unavailable")
	}
	scope, err := personalCoordinationScopeForCall(ctx, call)
	if err != nil {
		return nil, err
	}
	state := scope.state.Retain()
	if state == nil {
		return nil, errors.New("Personal invocation was released")
	}
	defer state.Close()
	if state.Host == nil || p.input.Registry != nil && state.Host.ServiceRegistry() != p.input.Registry {
		return nil, errors.New("Personal invocation belongs to another generation")
	}
	p.mu.Lock()
	if p.closed || !p.active.Load() {
		p.mu.Unlock()
		return nil, errors.New("Personal coordinator unavailable")
	}

	if !p.retirementRegistered {
		_, err := state.Host.ServiceRegistry().RegisterRetirementCleanup(p.ID(), p.Close)
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		p.retirementRegistered = true
	}
	if p.bridge == nil {
		p.bridge = scope.server.personalCoordination
	}
	if p.bridge != scope.server.personalCoordination {
		p.mu.Unlock()
		return nil, errors.New("Personal coordinator belongs to another runtime")
	}
	bridge := p.bridge
	p.calls.Add(1)
	p.mu.Unlock()
	defer p.calls.Done()
	ctx, cancel := context.WithCancel(runtime.WithInvocationState(ctx, state))
	stop := context.AfterFunc(p.ctx, cancel)
	defer stop()
	defer cancel()
	actions, err := bridge.acquire(ctx, p, scope)
	if err != nil {
		return nil, err
	}
	defer bridge.calls.Done()
	stopCore := context.AfterFunc(actions.ctx, cancel)
	defer stopCore()
	var input personalCoordinationRequest
	if err = json.Unmarshal(call.Params, &input); err != nil {
		return nil, err
	}
	switch call.Method {
	case "drain":
		if err := actions.drain(ctx); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "initialize":
		return json.RawMessage(`{}`), nil
	case "call":
		if strings.HasPrefix(input.Operation, "review.") {
			return actions.Call(ctx, input.Operation, input.Input, input.SessionID)
		}
		if strings.HasPrefix(input.Operation, "memory.") {
			return actions.callMemory(ctx, input.Operation, input.Input, input.SessionID)
		}
		return nil, errors.New("Personal coordination operation unavailable")
	case "file-gate":
		if scope.kit == nil || scope.kit.SessionID() != input.SessionID {
			return nil, errors.New("captured file toolkit is unavailable")
		}
		result, err := actions.gate(ctx, scope.kit, input.Call)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "connector-snapshot", "connector-commit":
		return actions.coordinateConnector(ctx, call.Method, input.Input)
	case "inspection":
		var params stateInspectionParams
		if err := decodeParams(input.Input, &params); err != nil {
			return nil, err
		}
		result, err := scope.server.inspectStateInspections(ctx, input.Operation, params, actions)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "scope":
		value, err := actions.scope(ctx, input.SessionID)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	case "cancel":
		if err := actions.engine.CancelExecution(ctx, scope.server.rt.UserBinding().UserSpaceID, input.SessionID, input.Scopes...); err != nil {
			return nil, err
		}
		actions.signal()
		return json.RawMessage(`{}`), nil
	default:
		return nil, errors.New("unknown Personal coordination method")
	}
}

type personalCoordinationConsumer struct{ personalActionsConsumer }

func NewPersonalCoordinationConsumer() pluginhost.Client { return &personalCoordinationConsumer{} }
func (*personalCoordinationConsumer) ID() string         { return PersonalCoordinationConsumer }
func (c *personalCoordinationConsumer) Status() pluginhost.Status {
	s := c.personalActionsConsumer.Status()
	s.ID = c.ID()
	return s
}
func (*personalCoordinationConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: PersonalCoordinationService, MajorVersion: 1}}
}

func (s *Server) personalCoordinationAvailable(state *runtime.State) bool {
	if s.personalCoordination == nil || state == nil || state.Host == nil {
		return false
	}
	provider, found := state.Host.ServiceRegistry().ProviderPluginIDFor(PersonalCoordinationConsumer, PersonalCoordinationService, 1)
	if !found {
		return false
	}
	consumerActive, providerActive := false, false
	for _, status := range state.Host.Statuses() {
		if status.State != pluginhost.StateActive {
			continue
		}
		consumerActive = consumerActive || status.ID == PersonalCoordinationConsumer
		providerActive = providerActive || status.ID == provider
	}
	return consumerActive && providerActive
}

func (s *Server) invokePersonalCoordination(ctx context.Context, method string, input personalCoordinationRequest, kit *tools.Toolkit) (json.RawMessage, error) {
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
	if !s.personalCoordinationAvailable(state) {
		return nil, errors.New("selected Personal coordinator is unavailable")
	}
	ctx = context.WithValue(runtime.WithInvocationState(ctx, state), personalCoordinationScopeKey{}, &personalCoordinationScope{server: s, state: state, kit: kit})
	if s.personalEffects != nil {
		ctx = s.personalEffects.Context(ctx, s, state, kit)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	result, callErr := state.Host.CallService(ctx, PersonalCoordinationConsumer, PersonalCoordinationService, method, raw)
	if callErr != nil {
		switch callErr.Code {
		case "personal_review_conflict":
			return nil, errors.Join(personalreview.ErrConflict, callErr)
		case "personal_namespace_unsupported":
			return nil, errors.Join(persistence.ErrNamespaceOwnershipUnsupported, callErr)
		}
		return nil, callErr
	}
	return result, nil
}
func (s *Server) callPersonalCoordination(ctx context.Context, operation string, input json.RawMessage, sessionID string) (json.RawMessage, error) {
	return s.invokePersonalCoordination(ctx, "call", personalCoordinationRequest{Operation: operation, Input: input, SessionID: sessionID}, nil)
}
func (s *Server) personalFileGate(ctx context.Context, kit *tools.Toolkit, call providers.ToolCall) (toolresult.Result, error) {
	sessionID := ""
	if kit != nil {
		sessionID = kit.SessionID()
	}
	raw, err := s.invokePersonalCoordination(ctx, "file-gate", personalCoordinationRequest{SessionID: sessionID, Call: call}, kit)
	if err != nil {
		return toolresult.Result{}, err
	}
	var result toolresult.Result
	err = json.Unmarshal(raw, &result)
	return result, err
}

func PersonalCoordinationConsumerFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: PersonalCoordinationConsumer, DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return NewPersonalCoordinationConsumer(), nil
	}}
}
