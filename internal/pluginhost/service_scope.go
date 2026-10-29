package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ServiceScope retains ordinary selected-service execution participation across
// an opaque resource lease. Close follows physical cleanup, not logical cancel.
// Provider retirement cancels Context and waits for Close before disposal.
type retainedServiceScopeKey struct{}
type retainedServiceScopeAdmissionKey struct{}

func ServiceScopeFromContext(ctx context.Context) *ServiceScope {
	scope, _ := ctx.Value(retainedServiceScopeKey{}).(*ServiceScope)
	return scope
}

type ServiceScope struct {
	closed   atomic.Bool
	registry *ServiceRegistry
	consumer string
	services map[string]bool
	Context  context.Context
	once     sync.Once
	close    func() error
	err      error
}

func (s *ServiceScope) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() { s.closed.Store(true); s.err = s.close() })
	return s.err
}

// OpenServiceScope uses the same authenticated consumer, execution tracker,
// dependency graph and drain barriers as CallService. It grants no new methods
// or tool authority. Every named service must already be a declared dependency.
func (h *Host) OpenServiceScope(ctx context.Context, consumer string, services ...string) (*ServiceScope, error) {
	if h == nil || len(services) == 0 {
		return nil, errors.New("service scope requires a host and services")
	}
	h.mu.RLock()
	if h.shutdown != nil || h.serviceRegistry == nil {
		h.mu.RUnlock()
		return nil, errors.New("service host is closing")
	}
	admitted := false
	for _, client := range h.clients {
		if client.ID() == consumer && client.Status().State == StateActive {
			admitted = true
			break
		}
	}
	if !admitted {
		h.mu.RUnlock()
		return nil, fmt.Errorf("service consumer %q is not active", consumer)
	}
	registry := h.serviceRegistry
	ctx = context.WithValue(ctx, inheritedInvocationKey{}, (*nativeInvocation)(nil))
	// This is a new host-admitted consumer scope, not the enclosing callback.
	ctx = context.WithValue(ctx, nativeInvocationKey{}, struct{}{})
	ctx = context.WithValue(ctx, retainedServiceScopeAdmissionKey{}, true)
	id, root := h.executions.begin(consumer, ctx, ToolExecuteInput{}, false)
	h.mu.RUnlock()
	var releases []func() error
	closeScope := func() error {
		var err error
		for i := len(releases) - 1; i >= 0; i-- {
			err = errors.Join(err, releases[i]())
		}
		h.executions.End(id)
		return err
	}
	var held context.Context
	for _, name := range services {
		registry.mu.RLock()
		var key serviceKey
		declared := false
		for required := range registry.consumers[consumer] {
			if required.name == name {
				key = required
				declared = true
				break
			}
		}
		key = registry.providerKeyLocked(consumer, key)
		provider, found := registry.providers[key]
		registry.mu.RUnlock()
		if !declared || !found {
			_ = closeScope()
			return nil, fmt.Errorf("service scope dependency %q is unavailable", name)
		}
		scopeContext, _, release, err := registry.acquireExecution(root, id, consumer, key, provider.pluginID, true)
		if err != nil {
			_ = closeScope()
			return nil, err
		}
		held = scopeContext
		releases = append(releases, release)
	}
	names := map[string]bool{}
	for _, name := range services {
		names[name] = true
	}
	scope := &ServiceScope{registry: registry, consumer: consumer, services: names, close: closeScope}
	scope.Context = context.WithValue(held, retainedServiceScopeKey{}, scope)
	return scope, nil
}

// CallCleanup uses only the scope's captured consumer and retained service set.
// The registry validates the cleanup descriptor and keeps ordinary work closed.
func (s *ServiceScope) CallCleanup(ctx context.Context, service, method string, raw json.RawMessage) (json.RawMessage, error) {
	if s == nil || s.closed.Load() || !s.services[service] {
		return nil, errors.New("service not retained by scope")
	}
	result, err := s.registry.Call(ctx, s.consumer, ServiceCallParams{Service: service, Method: method, Params: raw, Cleanup: true})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// waitRetainedServiceScopes is the same registry execution completion barrier
// used by generation drain, limited to explicit resource scopes. Caller timeouts
// never revoke cleanup authority while a physical resource remains admitted.
func (r *ServiceRegistry) waitRetainedServiceScopes(plugin string) {
	if r == nil {
		return
	}
	for {
		r.mu.RLock()
		waiting := false
		for _, execution := range r.executions {
			if execution.retained {
				if _, ok := execution.participants[plugin]; ok {
					waiting = true
					break
				}
			}
		}
		changed := r.executionChanged
		r.mu.RUnlock()
		if !waiting {
			return
		}
		<-changed
	}
}
