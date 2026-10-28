package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type projectCallScopeKey struct{}
type projectCallScope struct {
	registration *projectServerRegistration
	state        *runtime.State
	thread       *threadState
}

// This value stays in authenticated host execution context. It is never a
// wire server identity that a process provider can forge or redirect.
func (s *Server) projectServiceContext(ctx context.Context, binding *runtime.State) context.Context {
	return context.WithValue(ctx, projectCallScopeKey{}, &projectCallScope{registration: s.projectRegistration, state: binding})
}
func projectScopeForCall(ctx context.Context, call pluginhost.ServiceInvokeParams) (*projectCallScope, error) {
	scope, _ := ctx.Value(projectCallScopeKey{}).(*projectCallScope)
	if scope == nil && call.CallerScope != nil && call.CallerScope.Context != nil {
		scope, _ = call.CallerScope.Context.Value(projectCallScopeKey{}).(*projectCallScope)
	}
	if scope == nil || scope.registration == nil || scope.state == nil {
		return nil, errors.New("project mechanism requires a captured host invocation")
	}
	return scope, nil
}
func (scope *projectCallScope) retain(registry *pluginhost.ServiceRegistry, bridge *ProjectCoordinatorBridge, allowClosed bool) (*runtime.State, error) {
	if scope.registration.bridge != bridge || scope.state.Host == nil || scope.state.Host.ServiceRegistry() != registry {
		return nil, errors.New("project invocation belongs to another graph or runtime")
	}
	if !allowClosed && !scope.registration.live() {
		return nil, errors.New("project connection has closed")
	}
	held := scope.state.Retain()
	if held == nil {
		return nil, errors.New("project invocation state has been released")
	}
	return held, nil
}
