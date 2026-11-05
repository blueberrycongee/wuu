package appserver

import (
	"context"
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

type managedSessionCallScopeKey struct{}
type managedSessionCallScope struct {
	registration *managedSessionRegistration
	state        *runtime.State
}

// This value stays in authenticated host execution context. It is never a
// wire server identity that a process provider can forge or redirect.
func (s *Server) managedSessionContext(ctx context.Context, binding *runtime.State) context.Context {
	return context.WithValue(ctx, managedSessionCallScopeKey{}, &managedSessionCallScope{registration: s.managedSessionRegistration, state: binding})
}
func managedSessionScopeForCall(ctx context.Context, call pluginhost.ServiceInvokeParams) (*managedSessionCallScope, error) {
	scope, _ := ctx.Value(managedSessionCallScopeKey{}).(*managedSessionCallScope)
	if scope == nil && call.CallerScope != nil && call.CallerScope.Context != nil {
		scope, _ = call.CallerScope.Context.Value(managedSessionCallScopeKey{}).(*managedSessionCallScope)
	}
	if scope == nil || scope.registration == nil || scope.state == nil {
		return nil, errors.New("managed session mechanism requires a captured host invocation")
	}
	return scope, nil
}
func (scope *managedSessionCallScope) retain(registry *pluginhost.ServiceRegistry, bridge *managedSessionBridge, allowClosed bool) (*runtime.State, error) {
	if scope.registration.bridge != bridge || scope.state.Host == nil || scope.state.Host.ServiceRegistry() != registry {
		return nil, errors.New("managed session invocation belongs to another graph or runtime")
	}
	if !allowClosed && !scope.registration.live() {
		return nil, errors.New("managed session connection has closed")
	}
	held := scope.state.Retain()
	if held == nil {
		return nil, errors.New("managed session invocation state has been released")
	}
	return held, nil
}
