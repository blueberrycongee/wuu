package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"strings"
)

// The consumer is independent of the native provider, so replacing policy never
// changes the caller's authority or introduces a second host-owned ACL.
func resourceAccessConsumerFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: access.ConsumerModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &runtimeServiceConsumer{id: access.ConsumerModule, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: access.ServiceName, MajorVersion: 1, Required: true}, {Name: principal.ServiceName, MajorVersion: 1}}}, nil
	}}
}
func resourcePolicyProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: access.NativeModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		policy := access.PolicyFunc(func(ctx context.Context, r access.Request) (access.Decision, error) {
			if r.Owner {
				return access.Decision{Allow: true}, nil
			}
			// Projection grants are a dependent service, not host exemptions. Removing
			// the selected sharing provider removes these decisions immediately.
			if strings.HasPrefix(r.Action, "session/share/") {
				raw, err := json.Marshal(r)
				if err != nil {
					return access.Decision{}, err
				}
				result, callErr := input.Registry.Call(ctx, access.NativeModule, pluginhost.ServiceCallParams{Service: "session.sharing", Method: "authorize", Params: raw})
				if callErr != nil {
					return access.Decision{}, callErr
				}
				var decision access.Decision
				if err := json.Unmarshal(result, &decision); err != nil {
					return access.Decision{}, err
				}
				return decision, nil
			}
			return access.Decision{Reason: "owner-required"}, nil
		})
		return access.NewService(access.NativeModule, policy, pluginhost.ServiceRequirement{Name: "session.sharing", MajorVersion: 1}), nil
	}}
}

// AuthorizeResource always resolves the currently active composition. It does not
// retain a positive decision across calls, policy replacement or revocation.
func (s *Session) AuthorizeResource(ctx context.Context, r access.Request) (access.Decision, error) {
	state, err := s.RetainState()
	if err != nil {
		return access.Decision{}, err
	}
	defer state.Close()
	if state.Host == nil {
		return access.Decision{}, errors.New("selected resource policy unavailable")
	}
	return access.NewClient(boundServiceCall(state.Host, access.ConsumerModule, access.ServiceName)).Authorize(ctx, r)
}

// AuthenticateTransport is an embedding seam for OIDC/SAML and other adapters.
// Only the embedding supplies credentials; no app-server method calls it with
// a self-asserted principal or an owner bit.
func (s *Session) AuthenticateTransport(ctx context.Context, credential principal.Credential) (principal.Verified, error) {
	// Revalidation resolves the current provider graph, not the generation that
	// happened to verify the initial handshake.
	adapter := principal.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		state, err := s.RetainState()
		if err != nil {
			return nil, err
		}
		defer state.Close()
		if state.Host == nil {
			return nil, errors.New("selected identity provider unavailable")
		}
		return boundServiceCall(state.Host, access.ConsumerModule, principal.ServiceName)(ctx, method, raw)
	})
	return principal.Authenticate(ctx, adapter, credential)
}
