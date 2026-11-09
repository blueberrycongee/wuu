package pluginhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"sync/atomic"
)

type serviceContinuationKey struct{}

// The record lives in its owning callback, not a global handle table. Ordinary
// descendants may inherit the context value but never its exact callback ID.
type serviceContinuation struct {
	registry                  *ServiceRegistry
	owner, executionID, id    string
	service, method, provider string
	major                     int
	params                    json.RawMessage
	tool                      ToolExecuteInput
	used                      atomic.Bool
}

func (r *ServiceRegistry) newServiceContinuation(selected serviceProvider, params ServiceInvokeParams, tool ToolExecuteInput) *serviceContinuation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner := selected.pluginID
	major, _ := ServiceVersionMajor(selected.descriptor.Version)
	for key, consumption := range r.consumers[owner] {
		if !consumption.runtime || key.name != params.Service || key.major != major {
			continue
		}
		provider, found := r.providers[r.providerKeyLocked(owner, key)]
		if !found || provider.pluginID == owner {
			return nil
		}
		tool.Arguments = bytes.Clone(tool.Arguments)
		return &serviceContinuation{registry: r, owner: owner, id: rand.Text(),
			service: params.Service, method: params.Method, major: key.major,
			provider: provider.pluginID, params: bytes.Clone(params.Params), tool: tool}
	}
	return nil
}

// Continue forwards the original selected invocation once, with its original
// host authority. The delegate is the owner's declared, captured same-service
// selection. Normal generation replacement retains admitted work; explicit
// revocation, cancellation and callback completion do not.
func (r *ServiceRegistry) Continue(ctx context.Context, owner string, params ServiceContinueParams) (json.RawMessage, *HostServiceError) {
	r.mu.RLock()
	tracker := r.scopeTracker
	r.mu.RUnlock()
	if tracker == nil || params.ContinuationID == "" || params.ExecutionID == "" {
		return nil, &HostServiceError{Code: "invalid_continuation", Message: "service continuation requires its owning execution"}
	}
	scope, err := tracker.ResolveTool(owner, params.ExecutionID)
	if err != nil {
		return nil, err
	}
	continuation, ok := scope.Context.Value(serviceContinuationKey{}).(*serviceContinuation)
	if !ok || continuation.registry != r || continuation.owner != owner || continuation.executionID != scope.ID || continuation.id != params.ContinuationID {
		return nil, &HostServiceError{Code: "invalid_continuation", Message: "service continuation is unavailable in this execution"}
	}
	if ctx.Err() != nil || scope.Context.Err() != nil {
		return nil, &HostServiceError{Code: "request_cancelled", Message: "service continuation execution was cancelled"}
	}
	if !continuation.used.CompareAndSwap(false, true) {
		return nil, &HostServiceError{Code: "continuation_used", Message: "service continuation can only be invoked once"}
	}
	r.mu.RLock()
	key := r.providerKeyLocked(owner, serviceKey{name: continuation.service, major: continuation.major})
	provider, found := r.providers[key]
	r.mu.RUnlock()
	if !found || provider.pluginID != continuation.provider {
		return nil, &HostServiceError{Code: "service_unavailable", Message: "selected continuation provider is unavailable"}
	}
	return r.call(ctx, owner, ServiceCallParams{Service: continuation.service, Method: continuation.method,
		ExecutionID: scope.ID, Params: bytes.Clone(continuation.params)}, &continuation.tool)
}

func (r *ServiceRegistry) RouteServiceContinue(ctx context.Context, owner string, params ServiceContinueParams) (json.RawMessage, *HostServiceError) {
	return r.Continue(ctx, owner, params)
}
