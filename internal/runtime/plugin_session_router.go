package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// PluginSessionCreateHandler is installed by the live app-server. The plugin ID
// is supplied by the generation-bound host-service dispatcher, never by the
// plugin request payload.
type PluginSessionCreateHandler func(context.Context, string, pluginhost.SessionCreateParams) (pluginhost.SessionCreateResult, error)
type PluginSessionSendHandler func(context.Context, string, pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error)
type PluginSessionListHandler func(context.Context, string, pluginhost.SessionListParams) (pluginhost.SessionListResult, error)
type PluginSessionCancelHandler func(context.Context, string, pluginhost.SessionCancelParams) (pluginhost.SessionCancelResult, error)
type PluginSessionInspectHandler func(context.Context, string, pluginhost.SessionInspectParams) (pluginhost.SessionInspectResult, error)
type PluginSessionControlHandler func(context.Context, string, pluginhost.SessionControlParams) (pluginhost.SessionControlResult, error)
type PluginWorkspaceStatusHandler func(context.Context, string, pluginhost.WorkspaceStatusParams) (pluginhost.WorkspaceStatusResult, error)
type PluginWorkspaceApplyHandler func(context.Context, string, pluginhost.WorkspaceApplyParams) (pluginhost.WorkspaceApplyResult, error)
type PluginWorkspaceDiscardHandler func(context.Context, string, pluginhost.WorkspaceDiscardParams) (pluginhost.WorkspaceDiscardResult, error)

// PluginSessionRouter lets plugin processes outlive individual app-server
// bindings without coupling runtime construction to an app-server package.
type PluginSessionRouter struct {
	mu               sync.RWMutex
	create           PluginSessionCreateHandler
	send             PluginSessionSendHandler
	list             PluginSessionListHandler
	cancel           PluginSessionCancelHandler
	inspect          PluginSessionInspectHandler
	control          PluginSessionControlHandler
	workspaceStatus  PluginWorkspaceStatusHandler
	workspaceApply   PluginWorkspaceApplyHandler
	workspaceDiscard PluginWorkspaceDiscardHandler
	binding          uint64
}

func NewPluginSessionRouter() *PluginSessionRouter { return &PluginSessionRouter{} }

// Bind replaces the live handler and returns an idempotent unbind closure.
// The closure only clears the handler it installed, so closing an older server
// cannot disconnect a newer binding.
func (r *PluginSessionRouter) Bind(create PluginSessionCreateHandler, send PluginSessionSendHandler, list PluginSessionListHandler, cancel PluginSessionCancelHandler) func() {
	return r.BindExtended(create, send, list, cancel, nil, nil, nil, nil)
}

// BindExtended binds the complete public Session and Workspace host surface.
// Bind remains as the four-method compatibility seam for embedders.
func (r *PluginSessionRouter) BindExtended(create PluginSessionCreateHandler, send PluginSessionSendHandler, list PluginSessionListHandler, cancel PluginSessionCancelHandler, inspect PluginSessionInspectHandler, workspaceStatus PluginWorkspaceStatusHandler, workspaceApply PluginWorkspaceApplyHandler, workspaceDiscard PluginWorkspaceDiscardHandler, controls ...PluginSessionControlHandler) func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	r.binding++
	binding := r.binding
	r.create = create
	r.send = send
	r.list = list
	r.cancel = cancel
	r.inspect = inspect
	r.control = nil
	if len(controls) > 0 {
		r.control = controls[0]
	}
	r.workspaceStatus = workspaceStatus
	r.workspaceApply = workspaceApply
	r.workspaceDiscard = workspaceDiscard
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.binding == binding {
				r.create = nil
				r.send = nil
				r.list = nil
				r.cancel = nil
				r.inspect = nil
				r.control = nil
				r.workspaceStatus = nil
				r.workspaceApply = nil
				r.workspaceDiscard = nil
			}
			r.mu.Unlock()
		})
	}
}

func (r *PluginSessionRouter) Control(ctx context.Context, pluginID string, params pluginhost.SessionControlParams) (pluginhost.SessionControlResult, error) {
	if r == nil {
		return pluginhost.SessionControlResult{}, errors.New("session service unavailable")
	}
	r.mu.RLock()
	handler := r.control
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.SessionControlResult{}, errors.New("session control unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) List(ctx context.Context, pluginID string, params pluginhost.SessionListParams) (pluginhost.SessionListResult, error) {
	if r == nil {
		return pluginhost.SessionListResult{}, errors.New("session service is unavailable")
	}
	r.mu.RLock()
	handler := r.list
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.SessionListResult{}, errors.New("session service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) Cancel(ctx context.Context, pluginID string, params pluginhost.SessionCancelParams) (pluginhost.SessionCancelResult, error) {
	if r == nil {
		return pluginhost.SessionCancelResult{}, errors.New("session service is unavailable")
	}
	r.mu.RLock()
	handler := r.cancel
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.SessionCancelResult{}, errors.New("session service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) Inspect(ctx context.Context, pluginID string, params pluginhost.SessionInspectParams) (pluginhost.SessionInspectResult, error) {
	if r == nil {
		return pluginhost.SessionInspectResult{}, errors.New("session service is unavailable")
	}
	r.mu.RLock()
	handler := r.inspect
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.SessionInspectResult{}, errors.New("session service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) WorkspaceStatus(ctx context.Context, pluginID string, params pluginhost.WorkspaceStatusParams) (pluginhost.WorkspaceStatusResult, error) {
	if r == nil {
		return pluginhost.WorkspaceStatusResult{}, errors.New("workspace service is unavailable")
	}
	r.mu.RLock()
	handler := r.workspaceStatus
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.WorkspaceStatusResult{}, errors.New("workspace service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) WorkspaceApply(ctx context.Context, pluginID string, params pluginhost.WorkspaceApplyParams) (pluginhost.WorkspaceApplyResult, error) {
	if r == nil {
		return pluginhost.WorkspaceApplyResult{}, errors.New("workspace service is unavailable")
	}
	r.mu.RLock()
	handler := r.workspaceApply
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.WorkspaceApplyResult{}, errors.New("workspace service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) WorkspaceDiscard(ctx context.Context, pluginID string, params pluginhost.WorkspaceDiscardParams) (pluginhost.WorkspaceDiscardResult, error) {
	if r == nil {
		return pluginhost.WorkspaceDiscardResult{}, errors.New("workspace service is unavailable")
	}
	r.mu.RLock()
	handler := r.workspaceDiscard
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.WorkspaceDiscardResult{}, errors.New("workspace service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) Create(ctx context.Context, pluginID string, params pluginhost.SessionCreateParams) (pluginhost.SessionCreateResult, error) {
	if r == nil {
		return pluginhost.SessionCreateResult{}, errors.New("session service is unavailable")
	}
	r.mu.RLock()
	handler := r.create
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.SessionCreateResult{}, errors.New("session service is unavailable")
	}
	return handler(ctx, pluginID, params)
}

func (r *PluginSessionRouter) Send(ctx context.Context, pluginID string, params pluginhost.SessionSendParams) (pluginhost.SessionSendResult, error) {
	if r == nil {
		return pluginhost.SessionSendResult{}, errors.New("session service is unavailable")
	}
	r.mu.RLock()
	handler := r.send
	r.mu.RUnlock()
	if handler == nil {
		return pluginhost.SessionSendResult{}, errors.New("session service is unavailable")
	}
	return handler(ctx, pluginID, params)
}
