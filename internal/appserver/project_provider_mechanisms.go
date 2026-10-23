package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const ManagedSessionService = "host.managed-session"
const ManagedSessionModule = "wuu.managed-sessions"

// ManagedSessionCreate exposes the existing host creation mechanism to native
// and process coordinators. Explicit identity and the initial inbox message are
// committed together; the provider owns role policy and the message's content.
type ManagedSessionCreate struct {
	pluginhost.SessionCreateParams
	SessionID                string                `json:"session_id,omitempty"`
	Owner                    string                `json:"owner"`
	Source                   string                `json:"source"`
	Role                     string                `json:"role,omitempty"`
	SharedWorkspaceSessionID string                `json:"shared_workspace_session_id,omitempty"`
	InitialInput             *session.InboxMessage `json:"initial_input,omitempty"`
}
type ManagedSessionSubmit struct {
	SessionID  string                `json:"session_id"`
	Message    providers.ChatMessage `json:"message"`
	Control    *session.Control      `json:"control,omitempty"`
	AllowStart bool                  `json:"allow_start"`
}
type ManagedSessionSubmission struct {
	Admitted     bool `json:"admitted"`
	CapacityFull bool `json:"capacity_full"`
}

// MechanismFactory belongs in every app-server composition that supports
// managed sessions. Disabling coordination does not remove ordinary session
// control, while graph edges retain these mechanisms through provider cleanup.
func (b *ProjectCoordinatorBridge) MechanismFactory() runtime.NativeProviderFactory {
	return runtime.NativeProviderFactory{ID: ManagedSessionModule, DefaultEnabled: true, Build: func(input runtime.NativeProviderContext) (pluginhost.Client, error) {
		if input.Registry == nil || input.RetainState == nil {
			return nil, errors.New("managed sessions require a captured service graph and state factory")
		}
		return &managedSessionMechanisms{bridge: b, registry: input.Registry, retainState: input.RetainState, store: projectProviderStore(input.Registry, ManagedSessionModule)}, nil
	}}
}

type managedSessionMechanisms struct {
	bridge      *ProjectCoordinatorBridge
	registry    *pluginhost.ServiceRegistry
	retainState func() (*runtime.State, error)
	store       *session.Store
	active      atomic.Bool
	closed      atomic.Bool
}

func (m *managedSessionMechanisms) ID() string { return ManagedSessionModule }
func (m *managedSessionMechanisms) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if m.active.Load() {
		state = pluginhost.StateActive
	}
	if m.closed.Load() {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: m.ID(), State: state}
}
func (m *managedSessionMechanisms) Activate(context.Context) error {
	if m.closed.Load() {
		return errors.New("managed session mechanisms stopped")
	}
	m.active.Store(true)
	return nil
}
func (m *managedSessionMechanisms) Close(context.Context) error {
	m.closed.Store(true)
	m.active.Store(false)
	return nil
}
func (m *managedSessionMechanisms) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (m *managedSessionMechanisms) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: ManagedSessionService, Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{
		{Name: "create", InputSchema: "host.managed-session.create.input.v1", OutputSchema: "host.managed-session.create.output.v1"},
		{Name: "submit", InputSchema: "host.managed-session.submit.input.v1", OutputSchema: "host.managed-session.submit.output.v1"},
		{Name: "interrupt", InputSchema: "host.managed-session.interrupt.input.v1", OutputSchema: "host.managed-session.interrupt.output.v1"},
	}}}
}
func (m *managedSessionMechanisms) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (result json.RawMessage, resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = projectServiceError(resultErr)
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.active.Load() || m.closed.Load() {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "managed session mechanisms are stopped"}
	}
	background := pluginhost.IsBackgroundExecution(ctx)
	if call.CallerScope != nil {
		background = background || pluginhost.IsBackgroundExecution(call.CallerScope.Context)
	}
	var scope *projectCallScope
	var binding *runtime.State
	var err error
	if background {
		// Autonomous callbacks borrow a connection only for this operation,
		// even when their starting provenance contained a foreground scope.
		var release func()
		scope, ctx, release, err = m.bridge.acquireBackground(ctx, m.registry, m.retainState)
		if err != nil {
			return nil, err
		}
		defer release()
		binding = scope.state
	} else {
		scope, err = projectScopeForCall(ctx, call)
		if err != nil {
			return nil, err
		}
		binding, err = scope.retain(m.registry, m.bridge, false)
		if err != nil {
			return nil, err
		}
		defer binding.Close()
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(scope.registration.ctx, cancel)
		defer stop()
		defer cancel()
	}
	s := scope.registration.server
	ctx = context.WithValue(ctx, projectCallScopeKey{}, scope)
	ctx = context.WithValue(ctx, projectExecutionContextKey{}, call.ExecutionID)
	store := m.store.WithContext(ctx)
	switch call.Method {
	case "create":
		var input ManagedSessionCreate
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		if input.Owner == "" || input.Source == "" {
			return nil, errors.New("managed session owner and source are required")
		}
		params := hostSessionCreateParamsFromPlugin(input.SessionCreateParams)
		params.ProjectRole, params.SharedWorkspaceSessionID, params.InitialInput = input.Role, input.SharedWorkspaceSessionID, input.InitialInput
		th, err := s.createHostSessionThreadWithStore(binding, store, input.Owner, input.Source, input.SessionID, params)
		if err != nil {
			return nil, err
		}
		return json.Marshal(pluginhost.SessionCreateResult{SessionID: th.ID})
	case "submit":
		var input ManagedSessionSubmit
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		metadata, found, err := store.Find(input.SessionID)
		if err != nil {
			return nil, err
		}
		if !found || metadata.ArchivedAt != nil {
			return nil, session.ErrSessionNotFound
		}
		root, workspaceID, err := s.sessionWorkspace(metadata)
		if err != nil {
			return nil, err
		}
		if !s.ownsSessionWorkspace(root, workspaceID) {
			return nil, errors.New("managed session belongs to another host workspace")
		}
		if input.Control != nil {
			if err := store.ValidateControl(*input.Control); err != nil {
				return nil, err
			}
		}
		th, err := s.ensureThreadLoaded(binding, input.SessionID)
		if err != nil {
			return nil, err
		}
		permissions, err := s.resolveThreadTurnPermissions(th, nil)
		if err != nil {
			return nil, err
		}
		snapshot := turnRuntimeSnapshot{}.withPermissions(permissions)
		snapshot.Control = input.Control
		var admitted bool
		if input.AllowStart {
			_, admitted, err = s.trySubmitSessionInput(ctx, th, input.Message, sessionIfRunningSteer, snapshot)
		} else {
			_, admitted = s.steerSessionInput(th, input.Message, snapshot)
		}
		if errors.Is(err, session.ErrProjectWorkerCapacity) {
			return json.Marshal(ManagedSessionSubmission{CapacityFull: true})
		}
		if err != nil {
			return nil, err
		}
		return json.Marshal(ManagedSessionSubmission{Admitted: admitted})
	case "interrupt":
		var input struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(call.Params, &input); err != nil {
			return nil, err
		}
		if _, found, err := store.Find(input.SessionID); err != nil {
			return nil, err
		} else if !found {
			return nil, session.ErrSessionNotFound
		}
		if err := s.interruptOwnedThreadExecution(binding, input.SessionID); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown managed session mechanism"}
	}
}
func (p *ProjectCoordinationProvider) callManagedSession(ctx context.Context, method string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if p.registry == nil {
		return errors.New("project provider has no captured service graph")
	}
	raw, serviceErr := p.registry.Call(ctx, p.ID(), pluginhost.ServiceCallParams{Service: ManagedSessionService, Method: method, Params: raw, ExecutionID: projectExecutionID(ctx)})
	if serviceErr != nil {
		switch serviceErr.Code {
		case "project_control_changed":
			return errors.Join(session.ErrControlChanged, serviceErr)
		case "project_work_changed":
			return errors.Join(session.ErrProjectWorkChanged, serviceErr)
		}
		return serviceErr
	}
	if output == nil {
		return nil
	}
	return json.Unmarshal(raw, output)
}
func (p *ProjectCoordinationProvider) createManagedSession(ctx context.Context, owner, source, id string, params hostSessionCreateParams) (*threadState, error) {
	input := ManagedSessionCreate{Owner: owner, Source: source, SessionID: id, Role: params.ProjectRole, SharedWorkspaceSessionID: params.SharedWorkspaceSessionID, InitialInput: params.InitialInput,
		SessionCreateParams: pluginhost.SessionCreateParams{Speed: params.Speed, RequestID: params.RequestID, Name: params.Name, Visibility: params.Visibility, ParentSessionID: params.ParentSessionID, ContextSource: params.ContextSource, Workspace: params.Workspace, WorkspaceID: params.WorkspaceID, WorkspaceRoot: params.WorkspaceRoot, ModelAlias: params.ModelAlias, Provider: params.Provider, Model: params.Model, Variant: params.Variant, Effort: params.Effort, PermissionMode: params.PermissionMode, Instructions: params.Instructions}}
	var result pluginhost.SessionCreateResult
	if err := p.callManagedSession(ctx, "create", input, &result); err != nil {
		return nil, err
	}
	return p.ensureThreadLoaded(p.binding, result.SessionID)
}
func (p *ProjectCoordinationProvider) interruptManagedSession(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	return p.callManagedSession(ctx, "interrupt", struct {
		SessionID string `json:"session_id"`
	}{id}, nil)
}

// Each consumer owns its client identity and dependency lifetime even though all
// clients address the same selected physical state provider.
func projectProviderStore(registry *pluginhost.ServiceRegistry, consumer string) *session.Store {
	documents := persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		raw, err := registry.Call(ctx, consumer, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: params, ExecutionID: projectExecutionID(ctx)})
		if err != nil {
			return nil, err
		}
		return raw, nil
	})
	return session.NewStore(context.Background(), documents)
}

type projectExecutionContextKey struct{}

func projectExecutionID(ctx context.Context) string {
	id, _ := ctx.Value(projectExecutionContextKey{}).(string)
	return id
}
