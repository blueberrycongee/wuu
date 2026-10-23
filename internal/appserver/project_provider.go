package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const (
	ProjectCoordinationService  = "project.coordination"
	ProjectCoordinationModule   = "wuu.project-coordination"
	ProjectCoordinationConsumer = "wuu.project-client"
)

// ProjectCoordinationRequest is the version-one business command. Session and
// work commands retain the existing public project request vocabulary. Domain
// identities never grant execution authority; the service graph authenticates
// and cancels the invoking consumer independently.
type ProjectCoordinationRequest struct {
	ActorID     string                      `json:"actor_id,omitempty"`
	CallID      string                      `json:"call_id,omitempty"`
	ProjectID   string                      `json:"project_id,omitempty"`
	SessionID   string                      `json:"session_id,omitempty"`
	Action      string                      `json:"action,omitempty"`
	Details     bool                        `json:"details,omitempty"`
	Session     tools.ProjectSessionRequest `json:"session,omitempty"`
	Work        tools.ProjectWorkRequest    `json:"work,omitempty"`
	Selection   session.RuntimeSelection    `json:"selection,omitempty"`
	EngineID    agentengine.EngineID        `json:"engine_id,omitempty"`
	Start       ThreadStartParams           `json:"start,omitempty"`
	Turn        Turn                        `json:"turn,omitempty"`
	CompactOnly bool                        `json:"compact_only,omitempty"`
	Message     providers.ChatMessage       `json:"message,omitempty"`
	ClientID    string                      `json:"client_id,omitempty"`
	Cause       string                      `json:"cause,omitempty"`
	Content     string                      `json:"content,omitempty"`
	Wake        bool                        `json:"wake,omitempty"`
}

// ProjectCoordinationProvider owns default coordination policy and scheduling.
// Server supplies managed-session execution/transport mechanisms; it does not
// select this implementation or create a replacement when this module stops.
// The store belongs to this generation and remains available through cleanup.
type ProjectCoordinationProvider struct {
	*Server
	registry *pluginhost.ServiceRegistry
	ctx      context.Context
	store    *session.Store
	owner    *ProjectCoordinationProvider
	lifetime *projectCoordinationLifetime
}

// Invocation views share lifetime/queue ownership while their store context
// carries the admitted execution. Background work uses the owner's context.
type projectCoordinationLifetime struct {
	onClose                func()
	ctx                    context.Context
	cancel                 context.CancelFunc
	closed                 atomic.Bool
	active                 atomic.Bool
	backgroundMu           sync.Mutex
	backgroundWG           sync.WaitGroup
	schedulerStarted       bool
	schedulerReady         chan struct{}
	projectCreateMu        sync.Mutex
	projectInboxMu         sync.Mutex
	projectInboxDrains     map[string]*projectInboxDrain
	projectCapacityRetries map[string]map[string]projectCapacityRetry
	projectInboxAfterFunc  func(time.Duration, func()) func()
}

func newProjectCoordinationProvider(store *session.Store) *ProjectCoordinationProvider {
	ctx, cancel := context.WithCancel(context.Background())
	p := &ProjectCoordinationProvider{ctx: ctx, store: store.WithContext(ctx), lifetime: &projectCoordinationLifetime{ctx: ctx, cancel: cancel, schedulerReady: make(chan struct{})}}
	p.owner = p
	return p
}
func (p *ProjectCoordinationProvider) ID() string { return ProjectCoordinationModule }
func (p *ProjectCoordinationProvider) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if p.lifetime.active.Load() {
		state = pluginhost.StateActive
	}
	if p.lifetime.closed.Load() {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: p.ID(), State: state}
}
func (p *ProjectCoordinationProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{ProjectCoordinationDescriptor()}
}
func ProjectCoordinationDescriptor() pluginhost.ServiceDescriptor {
	methods := []pluginhost.ServiceMethodDescriptor{}
	for _, name := range []string{"scheduler", "session", "work", "membership", "create", "turn", "user-input", "inbox", "schedule", "recover", "instructions", "member-work", "coordinator", "enqueue", "capacity", "work-list", "tool-policy", "tool-validate", "tool-config", "admission"} {
		methods = append(methods, pluginhost.ServiceMethodDescriptor{Name: name, InputSchema: "project.coordination." + name + ".input.v1", OutputSchema: "project.coordination." + name + ".output.v1"})
	}
	return pluginhost.ServiceDescriptor{Name: ProjectCoordinationService, Version: "1.0.0", Methods: methods}
}
func (p *ProjectCoordinationProvider) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}, {Name: ManagedSessionService, MajorVersion: 1, Required: true}, {Name: pluginhost.KernelBackgroundService, MajorVersion: 1, Required: true}}
}
func (p *ProjectCoordinationProvider) Activate(ctx context.Context) error {
	if !projectAgentEnabled {
		return errProjectAgentDisabled
	}
	return p.activate(ctx)
}
func (p *ProjectCoordinationProvider) Close(ctx context.Context) error {
	p.lifetime.backgroundMu.Lock()
	p.lifetime.closed.Store(true)
	p.lifetime.active.Store(false)
	p.lifetime.backgroundMu.Unlock()
	p.stopScheduling()
	if p.lifetime.onClose != nil {
		p.lifetime.onClose()
	}
	done := make(chan struct{})
	go func() { p.lifetime.backgroundWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *ProjectCoordinationProvider) startBackground(work func()) bool {
	p.lifetime.backgroundMu.Lock()
	if !p.schedulingAllowed() || p.lifetime.closed.Load() || !p.lifetime.active.Load() || p.Server == nil || !p.selected() {
		p.lifetime.backgroundMu.Unlock()
		return false
	}
	p.lifetime.backgroundWG.Add(1)
	p.lifetime.backgroundMu.Unlock()
	go func() { defer p.lifetime.backgroundWG.Done(); work() }()
	return true
}
func (p *ProjectCoordinationProvider) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.lifetime.backgroundMu.Lock()
	if p.lifetime.closed.Load() || !p.lifetime.active.Load() || p.Server == nil {
		p.lifetime.backgroundMu.Unlock()
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "project coordination provider is stopped or unbound"}
	}
	p.lifetime.backgroundWG.Add(1)
	p.lifetime.backgroundMu.Unlock()
	defer p.lifetime.backgroundWG.Done()
	ctx = context.WithValue(ctx, projectExecutionContextKey{}, call.ExecutionID)
	operation := *p
	operation.ctx = ctx
	operation.store = p.store.WithContext(ctx)
	p = &operation

	if call.Service != ProjectCoordinationService {
		return nil, &pluginhost.HostServiceError{Code: "service_unavailable", Message: "unknown project service"}
	}
	if call.Method == "scheduler" {
		if call.Caller != p.ID() {
			return nil, &pluginhost.HostServiceError{Code: "service_not_authorized", Message: "project scheduler must be started by its owner"}
		}
		close(p.lifetime.schedulerReady)
		p.owner.startBackground(p.owner.startProjectRecovery)
		select {
		case <-ctx.Done():
		case <-p.lifetime.ctx.Done():
		}
		p.owner.stopScheduling()
		return json.RawMessage(`{}`), nil
	}
	if call.Method == "instructions" || call.Method == "tool-config" {
		var metadata session.Session
		if err := json.Unmarshal(call.Params, &metadata); err != nil {
			return nil, err
		}
		if call.Method == "tool-config" {
			policy := pluginhost.SessionToolPolicy{}
			if metadata.Source == projectSource {
				policy.Allow = []string{"session", "notes"}
			}
			return json.Marshal(policy)
		}
		return json.Marshal(defaultProjectInstructions(metadata))
	}
	if call.Method == "tool-policy" || call.Method == "tool-validate" {
		raw, err := p.invokeToolPolicy(call)
		if err != nil {
			return nil, projectServiceError(err)
		}
		return raw, nil
	}
	var request ProjectCoordinationRequest
	if err := json.Unmarshal(call.Params, &request); err != nil {
		return nil, err
	}
	var value any = struct{}{}
	var err error
	switch call.Method {
	case "admission":
		cfg := config.Config{}
		if p.rt.ConfigLoadMode != runtime.ConfigLoadFile || strings.TrimSpace(p.rt.ConfigPath) != "" {
			cfg, _, err = p.rt.LoadEffectiveConfig()
		}
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, config.ErrConfigNotFound) {
			err = nil
		}
		value = ProjectAdmissionPolicy{MaxParallel: cfg.Agent.ProjectMaxParallelValue()}
	case "session":
		value, err = p.projectSessionHandler(request.ActorID)(ctx, request.CallID, request.Session)
	case "work-list":
		_, _, _, err = p.projectActor(request.ProjectID)
		if err == nil {
			value, err = p.listProjectWork(request.ProjectID, request.Details)
		}
	case "capacity":
		value = p.owner.deferProjectCapacityRetry(request.SessionID, request.Action)
	case "work":
		value, err = p.projectWork(ctx, request.ActorID, request.CallID, request.Work)
	case "membership":
		switch request.Action {
		case "adopt":
			value, err = p.adoptProjectSession(request.ProjectID, request.SessionID)
		case "release":
			value, err = p.releaseProjectSession(request.ProjectID, request.SessionID)
		default:
			err = errors.New("action must be adopt or release")
		}
	case "create":
		var th *threadState
		th, err = p.startProjectThread(ctx, request.Selection, request.EngineID, request.Start)
		if err == nil {
			value = map[string]string{"session_id": th.ID}
		}
	case "turn", "user-input":
		var th *threadState
		th, err = p.ensureOwnedThreadLoaded(request.SessionID)
		if err == nil && th != nil {
			if call.Method == "turn" {
				p.afterProjectTurn(th, request.Turn, request.CompactOnly)
			} else {
				p.noticeProjectUserMessage(th, request.Message)
			}
		}
	case "inbox":
		p.owner.drainSessionInbox(request.SessionID)
	case "schedule":
		p.owner.kickProjectInboxDrain(request.ProjectID)
	case "recover":
		if request.ProjectID != "" {
			err = p.recoverProjectWork(request.ProjectID)
		} else {
			p.owner.recoverProjectInbox()
		}

	case "member-work":
		var work session.ProjectWork
		var found bool
		work, found, err = p.workForMember(request.ProjectID, request.SessionID)
		value = struct {
			Work  session.ProjectWork `json:"work"`
			Found bool                `json:"found"`
		}{work, found}
	case "coordinator":
		project, found := p.projectCoordinator(request.ProjectID)
		value = struct {
			Project session.Session `json:"project"`
			Found   bool            `json:"found"`
		}{project, found}
	case "enqueue":
		p.enqueueProjectInput(request.ProjectID, request.SessionID, request.ClientID, request.Cause, request.Content, request.Wake)

	default:
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "unknown project coordination method"}
	}
	if err != nil {
		return nil, projectServiceError(err)
	}

	return json.Marshal(value)
}

// The consumer is separate from the optional implementation. Removing the
// selected provider leaves calls unavailable instead of enabling a fallback.
type projectCoordinationConsumer struct {
	active atomic.Bool
	closed atomic.Bool
}

func NewProjectCoordinationConsumer() pluginhost.Client { return &projectCoordinationConsumer{} }
func (*projectCoordinationConsumer) ID() string         { return ProjectCoordinationConsumer }
func (c *projectCoordinationConsumer) Status() pluginhost.Status {
	state := pluginhost.StatePrepared
	if c.active.Load() {
		state = pluginhost.StateActive
	}
	if c.closed.Load() {
		state = pluginhost.StateStopped
	}
	return pluginhost.Status{ID: c.ID(), State: state}
}
func (*projectCoordinationConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*projectCoordinationConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: ProjectCoordinationService, MajorVersion: 1}, {Name: persistence.ServiceName, MajorVersion: 1}}
}
func (c *projectCoordinationConsumer) Activate(context.Context) error {
	if c.closed.Load() {
		return errors.New("project consumer has stopped")
	}
	c.active.Store(true)
	return nil
}
func (c *projectCoordinationConsumer) Close(context.Context) error { c.active.Store(false); return nil }

func (p *ProjectCoordinationProvider) selected() bool {
	if p.registry == nil {
		return false
	}
	id, ok := p.registry.ProviderPluginID(ProjectCoordinationService, 1)
	return ok && id == p.ID()
}

// ProjectAdmissionPolicy supplies worker capacity; the session domain enforces
// its lease/membership transaction without choosing scheduling policy.
type ProjectAdmissionPolicy struct {
	MaxParallel int `json:"max_parallel"`
}

func projectServiceError(err error) error {
	var serviceErr *pluginhost.HostServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	code := "project_request_failed"
	switch {
	case errors.Is(err, session.ErrControlChanged):
		code = "project_control_changed"
	case errors.Is(err, session.ErrProjectWorkChanged):
		code = "project_work_changed"
	case errors.Is(err, session.ErrSessionNotFound):
		code = "project_session_missing"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = "request_cancelled"
	}
	return &pluginhost.HostServiceError{Code: code, Message: err.Error()}
}

func (p *ProjectCoordinationProvider) schedulingAllowed() bool {
	if p.lifetime.ctx.Err() != nil {
		return false
	}
	select {
	case <-p.lifetime.schedulerReady:
		return true
	default:
		return false
	}
}
