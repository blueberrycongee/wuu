package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/sessionshare"
)

// SessionSharing returns only the selected provider. A missing/disabled provider
// disables sharing; generationless embedding must explicitly assemble a graph.
func (s *State) SessionSharing() *sessionshare.Client {
	if s.Host == nil {
		return nil
	}
	registry := s.Host.ServiceRegistry()
	if registry == nil || !registry.HasProviderFor(sessionshare.ConsumerModule, sessionshare.ServiceName, 1) {
		return nil
	}
	return sessionshare.NewClient(boundServiceCall(s.Host, sessionshare.ConsumerModule, sessionshare.ServiceName))
}
func newNativeSessionSharing(registry *pluginhost.ServiceRegistry) *sessionshare.Service {
	documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := registry.Call(ctx, sessionshare.NativeModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return sessionshare.NewService(documents)
}

// These factories are optional independent capabilities, not dependencies of
// agent execution. Minimal compositions can omit both and keep ordinary reads.
func sessionSharingProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: sessionshare.NativeModule, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		return newNativeSessionSharing(input.Registry), nil
	}}
}
func sessionSharingConsumerFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: sessionshare.ConsumerModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &runtimeServiceConsumer{id: sessionshare.ConsumerModule, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: sessionshare.ServiceName, MajorVersion: 1}}}, nil
	}}
}
