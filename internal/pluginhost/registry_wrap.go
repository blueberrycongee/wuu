package pluginhost

import (
	"errors"
	"fmt"
)

// WrapServiceInvoker decorates the selected provider before activation. Call it
// after all registrations. Declarations, identity, dispatch, execution scopes,
// status checks and lifecycle remain those of the original service provider.
func (r *ServiceRegistry) WrapServiceInvoker(service string, major int, wrap func(ServiceInvoker) ServiceInvoker) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open || r.active {
		return errors.New("service invokers can only be wrapped before activation")
	}
	key := serviceKey{name: service, major: major}
	provider, ok := r.providers[key]
	if !ok {
		return fmt.Errorf("no selected provider for %s@%d", service, major)
	}
	wrapped := wrap(provider.invoker)
	if wrapped == nil || wrapped.ID() != provider.pluginID {
		return errors.New("service wrapper must retain the provider identity")
	}
	provider.invoker = wrapped
	r.providers[key] = provider
	return nil
}
