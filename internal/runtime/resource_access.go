package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
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
			if r.Delegation != nil {
				provider, ok := input.Registry.ProviderPluginIDFor(access.NativeModule, executionauth.ServiceName, 1)
				if !ok || provider != r.Delegation.Provider {
					return access.Decision{Reason: "execution-provider-unavailable"}, nil
				}
				var scope executionauth.Scope
				if err := json.Unmarshal(r.Delegation.Scope, &scope); err != nil {
					return access.Decision{Reason: "invalid-execution-scope"}, nil
				}
				client := executionauth.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
					result, err := input.Registry.Call(ctx, access.NativeModule, pluginhost.ServiceCallParams{Service: executionauth.ServiceName, Method: method, Params: raw})
					if err != nil {
						return nil, err
					}
					return result, nil
				})
				validation, err := client.Validate(ctx, executionauth.ValidateRequest{Reference: executionauth.Reference{Provider: r.Delegation.Provider, ID: r.Delegation.Grant}, Scope: scope, Action: r.Action, Resources: r.Resources})
				if err != nil {
					return access.Decision{}, err
				}
				return access.Decision{Allow: validation.Valid && validation.Principal == r.Principal, Reason: "bounded-execution-grant"}, nil
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
		return access.NewService(access.NativeModule, policy, pluginhost.ServiceRequirement{Name: "session.sharing", MajorVersion: 1}, pluginhost.ServiceRequirement{Name: executionauth.ServiceName, MajorVersion: 1}), nil
	}}
}

// AuthorizeResource always resolves the currently active composition. It does not
// retain a positive decision across calls, policy replacement or revocation.
func (s *Session) AuthorizeResource(ctx context.Context, r access.Request) (access.Decision, error) {
	if proof := access.DelegationFromContext(ctx); proof != nil {
		caller, ok := principal.FromContext(ctx)
		if !ok || caller.Identity() != r.Principal || caller.IsOwner() {
			return access.Decision{}, errors.New("delegated resource principal mismatch")
		}
		if err := caller.Check(ctx); err != nil {
			return access.Decision{}, err
		}
		r.Owner = false
		r.Delegation = proof
	}
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
