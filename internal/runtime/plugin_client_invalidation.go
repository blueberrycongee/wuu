package runtime

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

var clientInvalidationTopic = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

type clientInvalidationInvoker struct{ parent *kernelHostServices }

func (k *clientInvalidationInvoker) ID() string                { return k.parent.ID() }
func (k *clientInvalidationInvoker) Status() pluginhost.Status { return k.parent.Status() }
func (k *clientInvalidationInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if params.Method != "call" {
		return nil, serviceError("method_not_found", "client invalidation method is unavailable")
	}
	var input struct {
		Topic string `json:"topic"`
	}
	if err := decodeServiceParams(params.Params, &input); err != nil {
		return nil, err
	}
	if !clientInvalidationTopic.MatchString(input.Topic) {
		return nil, serviceError("invalid_params", "topic must be a 1-64 character lowercase identifier")
	}
	k.parent.mu.RLock()
	s := k.parent.services[params.Caller]
	k.parent.mu.RUnlock()
	if s == nil {
		return nil, serviceError("service_unavailable", "client invalidation owner is unavailable")
	}
	// Serialize publication with retirement: closed generations cannot emit.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.open || !s.active {
		return nil, serviceError("generation_closed", "client invalidation owner is inactive")
	}
	if s.subjectID == "" || s.fingerprint == "" {
		return nil, serviceError("generation_invalid", "client invalidation owner has no package identity")
	}
	if s.turnRouter == nil {
		return nil, serviceError("service_unavailable", "client invalidation route is unavailable")
	}
	event := pluginhost.ClientInvalidation{PluginID: s.subjectID, Fingerprint: s.fingerprint, UserSpaceID: s.binding.UserSpaceID, ProductDomain: s.binding.Domain(), Topic: input.Topic}
	if err := s.turnRouter.invalidateClient(ctx, s.ownerHost, event); err != nil {
		return nil, err
	}
	return json.RawMessage(`{}`), nil
}

// BindClientInvalidation attaches the live app-server without making runtime
// construction depend on it. Rebinding cannot be undone by an older server.
func (r *PluginSessionRouter) BindClientInvalidation(handler func(context.Context, *pluginhost.Host, pluginhost.ClientInvalidation) error) func() {
	r.mu.Lock()
	r.clientBinding++
	binding := r.clientBinding
	r.clientInvalidation = handler
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.clientBinding == binding {
				r.clientInvalidation = nil
			}
		})
	}
}
func (r *PluginSessionRouter) invalidateClient(ctx context.Context, owner *pluginhost.Host, event pluginhost.ClientInvalidation) error {
	r.mu.RLock()
	handler := r.clientInvalidation
	r.mu.RUnlock()
	if handler == nil {
		return serviceError("service_unavailable", "client invalidation route is unavailable")
	}
	return handler(ctx, owner, event)
}
