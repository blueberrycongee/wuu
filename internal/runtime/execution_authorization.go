package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/userspace"
)

func executionAuthorizationConsumerFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: executionauth.ConsumerModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &runtimeServiceConsumer{id: executionauth.ConsumerModule, state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: executionauth.ServiceName, MajorVersion: 1}}}, nil
	}}
}
func executionAuthorizationProviderFactory() NativeProviderFactory {
	return NativeProviderFactory{ID: executionauth.NativeModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
		documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := input.Registry.Call(ctx, executionauth.NativeModule, pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
			if err != nil {
				return nil, err
			}
			return result, nil
		})
		return executionauth.NewService(executionauth.NativeModule, documents), nil
	}}
}

// ExecutionAuthorizationIntent is a request for bounded delegation, not an
// authority claim. The host resolves scope, provider identity and execution bounds.
type ExecutionAuthorizationIntent struct {
	Route     *pluginhost.SessionRouteRequest `json:"route,omitempty"`
	Action    string                          `json:"-"`
	Service   string                          `json:"service,omitempty"`
	Method    string                          `json:"method,omitempty"`
	Params    json.RawMessage                 `json:"params,omitempty"`
	Operation string                          `json:"operation"`
	RequestID string                          `json:"request_id,omitempty"`
	// RunID is the durable business execution target. RequestID identifies only
	// this issuance attempt; legacy intents without run_id use request_id.
	RunID            string                   `json:"run_id,omitempty"`
	Kind             string                   `json:"kind,omitempty"`
	AuthorityService string                   `json:"authority_service,omitempty"`
	TaskID           string                   `json:"task_id,omitempty"`
	SessionID        string                   `json:"session_id,omitempty"`
	RequestedActions []string                 `json:"requested_actions,omitempty"`
	ExpiresAt        time.Time                `json:"expires_at,omitempty"`
	MaxDepth         int                      `json:"max_depth,omitempty"`
	Parent           *executionauth.Reference `json:"parent,omitempty"`
	Reference        *executionauth.Reference `json:"reference,omitempty"`
}

type ExecutionAuthorizationHandler func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (r *PluginSessionRouter) BindExecutionAuthorization(handler ExecutionAuthorizationHandler) func() {
	r.mu.Lock()
	r.executionAuthorizationBinding++
	binding := r.executionAuthorizationBinding
	r.executionAuthorization = handler
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.executionAuthorizationBinding == binding {
				r.executionAuthorization = nil
			}
		})
	}
}
func (s *pluginHostServices) executionAuthorizationService(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if s.turnRouter == nil {
		return nil, errors.New("execution authorization route is unavailable")
	}
	s.turnRouter.mu.RLock()
	handler := s.turnRouter.executionAuthorization
	s.turnRouter.mu.RUnlock()
	if handler == nil {
		return nil, errors.New("execution authorization route is unavailable")
	}
	return handler(ctx, s.pluginID, raw)
}
func (s *Session) executionState(ctx context.Context) (*State, error) {
	if HasInvocationState(ctx) {
		return RetainProductInvocationState(ctx)
	}
	return s.RetainState()
}
func executionPackage(state *State, pluginID string) (pluginpkg.Plugin, error) {
	if state == nil || state.Generation == nil || state.Host == nil {
		return pluginpkg.Plugin{}, serviceError("execution_authorization_required", "execution authority generation is unavailable")
	}
	for _, item := range state.Generation.ActivePackages() {
		if item.ID == pluginID && item.SubjectID != "" && item.Fingerprint != "" {
			return item, nil
		}
	}
	return pluginpkg.Plugin{}, serviceError("execution_authorization_required", "execution authority package is unavailable")
}
func executionClient(state *State, reference *executionauth.Reference) (*executionauth.Client, error) {
	if state == nil || state.Host == nil || state.Host.ServiceRegistry() == nil {
		return nil, serviceError("execution_authorization_required", "selected execution authorization unavailable")
	}
	provider, ok := state.Host.ServiceRegistry().ProviderPluginIDFor(executionauth.ConsumerModule, executionauth.ServiceName, 1)
	if !ok || reference != nil && provider != reference.Provider {
		return nil, serviceError("execution_authorization_required", "execution grant provider binding changed")
	}
	return executionauth.NewClient(boundServiceCall(state.Host, executionauth.ConsumerModule, executionauth.ServiceName)), nil
}
func selectedTaskAuthority(state *State, item pluginpkg.Plugin, service string) (executionauth.AuthorityBinding, error) {
	registry := state.Host.ServiceRegistry()
	provider, ok := registry.ProviderPluginIDFor(executionauth.ConsumerModule, service, 1)
	if !ok || provider != item.ID {
		return executionauth.AuthorityBinding{}, serviceError("execution_authorization_required", "task authority is not the authenticated selected provider")
	}
	descriptor, ok := registry.ServiceDescriptorFor(executionauth.ConsumerModule, service, 1)
	found := false
	if ok {
		for _, method := range descriptor.Methods {
			if method.Name == "resolve-lineage" && method.InputSchema == "execution.lineage.resolve.input.v1" && method.OutputSchema == "execution.lineage.resolve.output.v1" {
				found = true
			}
		}
	}
	if !found {
		return executionauth.AuthorityBinding{}, errors.New("selected task authority cannot resolve execution lineage")
	}
	return executionauth.AuthorityBinding{Service: service, Major: 1, Provider: item.ID, Subject: item.SubjectID, Fingerprint: item.Fingerprint}, nil
}
func (s *Session) resolveExecutionLineage(ctx context.Context, state *State, item pluginpkg.Plugin, authority executionauth.AuthorityBinding, intent ExecutionAuthorizationIntent, parent *executionauth.IssueResult) (executionauth.Lineage, error) {
	actual, err := selectedTaskAuthority(state, item, authority.Service)
	if err != nil {
		return executionauth.Lineage{}, err
	}
	if actual != authority {
		return executionauth.Lineage{}, serviceError("execution_authorization_required", "selected task authority binding changed")
	}
	runID := intent.RunID
	if runID == "" {
		runID = intent.RequestID
	}
	raw, err := json.Marshal(struct {
		Parent    *executionauth.IssueResult `json:"parent,omitempty"`
		Kind      string                     `json:"kind"`
		TaskID    string                     `json:"task_id"`
		SessionID string                     `json:"session_id,omitempty"`
		RequestID string                     `json:"request_id,omitempty"`
		Action    string                     `json:"action,omitempty"`
		Reference *executionauth.Reference   `json:"reference,omitempty"`
	}{parent, intent.Kind, intent.TaskID, intent.SessionID, runID, intent.Action, intent.Reference})
	if err != nil {
		return executionauth.Lineage{}, err
	}
	result, callErr := state.Host.CallService(ctx, executionauth.ConsumerModule, authority.Service, "resolve-lineage", raw)
	if callErr != nil {
		return executionauth.Lineage{}, callErr
	}
	var lineage executionauth.Lineage
	if err := json.Unmarshal(result, &lineage); err != nil {
		return lineage, err
	}
	if lineage.TaskID != intent.TaskID || lineage.TaskID == "" || lineage.Revision < 1 || lineage.ContextDigest == "" || intent.SessionID != "" && lineage.SessionID != intent.SessionID {
		return lineage, serviceError("execution_authorization_required", "task authority returned mismatched lineage")
	}
	metadata, found, err := state.Sessions.WithContext(ctx).Find(lineage.SessionID)
	if err != nil {
		return lineage, err
	}
	if !found || metadata.Owner != "plugin:"+item.ID || !s.UserBinding().Matches(userspace.Binding{UserSpaceID: metadata.UserSpaceID, ProductDomain: metadata.ProductDomain}) {
		return lineage, serviceError("execution_authorization_required", "task lineage has no matching host-owned session")
	}
	if lineage.Route != nil {
		if err := validateExecutionRouteBinding(lineage.Route); err != nil {
			return lineage, err
		}
		lineage.ContextDigest = executionauth.RequestDigest(struct {
			Context string                             `json:"context"`
			Route   *executionauth.SessionRouteBinding `json:"route"`
		}{lineage.ContextDigest, lineage.Route})
	}
	lineage.Authority = actual
	lineage.ModelProvider, lineage.Model, lineage.PermissionMode, err = s.executionSelection(state, metadata)
	if err != nil {
		return lineage, err
	}
	lineage.Depth = 0
	if parent != nil {
		lineage.Depth = parent.Scope.Depth + 1
	}
	return lineage, nil
}
func (s *Session) executionSelection(state *State, metadata session.Session) (string, string, string, error) {
	provider, model, mode := metadata.Provider, metadata.Model, metadata.PermissionMode
	if provider == "" || model == "" {
		models, err := state.Models()
		if err != nil {
			return "", "", "", err
		}
		if provider == "" {
			provider = models.Roles.Main.Provider
		}
		if model == "" {
			model = models.Roles.Main.Model
		}
	}
	if mode == "" || s.PermissionModeExplicit {
		models, err := state.Models()
		if err != nil {
			return "", "", "", err
		}
		mode = models.PermissionMode
	}
	if provider == "" || model == "" {
		return "", "", "", errors.New("effective task model is unavailable")
	}
	return provider, model, config.NormalizePermissionMode(mode), nil
}
func scopeForLineage(binding userspace.Binding, item pluginpkg.Plugin, kind string, lineage executionauth.Lineage, maxDepth int) executionauth.Scope {
	scope := executionauth.Scope{Kind: kind, Authority: lineage.Authority, Bounds: executionauth.Bounds{ModelProviders: []string{lineage.ModelProvider}, Models: []string{lineage.Model}, PermissionModes: []string{lineage.PermissionMode}, MaxDepth: maxDepth}, Depth: lineage.Depth, Consumer: item.SubjectID, UserSpaceID: binding.UserSpaceID, ProductDomain: binding.Domain(), TaskID: lineage.TaskID, RunID: lineage.RunID, SessionID: lineage.SessionID, ContextDigest: lineage.ContextDigest, Revision: lineage.Revision}
	if kind == "task-lineage" {
		scope.RunID = ""
		scope.SessionID = ""
	}
	return scope
}
func grantResources(scope executionauth.Scope) []access.Resource {
	resources := []access.Resource{{Kind: "user-space", ID: scope.UserSpaceID}, {Kind: "task", ID: scope.TaskID}}
	if scope.Kind == "exact-run" {
		resources = append(resources, access.Resource{Kind: "session", ID: scope.SessionID})
	}
	return resources
}
func (s *Session) checkGrantAdmission(ctx context.Context, caller principal.Verified, action string, resources []access.Resource) error {
	if err := caller.Check(ctx); err != nil {
		return err
	}
	request := access.Request{Principal: caller.Identity(), Owner: caller.IsOwner(), Action: action, Resources: resources, Delegation: access.DelegationFromContext(ctx), Attributes: access.AttributesFromContext(ctx)}
	if err := access.Check(ctx, access.PolicyFunc(s.AuthorizeResource), request); err != nil {
		return err
	}
	return caller.Check(ctx)
}

// HandleExecutionAuthorization is invoked only by the authenticated plugin host
// gateway. No identity, task scope or provider fingerprint is accepted as proof.
func (s *Session) HandleExecutionAuthorization(ctx context.Context, pluginID string, raw json.RawMessage) (json.RawMessage, error) {
	var intent ExecutionAuthorizationIntent
	if err := decodeServiceParams(raw, &intent); err != nil {
		return nil, err
	}
	state, err := s.executionState(ctx)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	item, err := executionPackage(state, pluginID)
	if err != nil {
		return nil, err
	}
	ref := intent.Reference
	if intent.Parent != nil {
		ref = intent.Parent
	}
	client, err := executionClient(state, ref)
	if err != nil {
		return nil, err
	}
	if intent.Operation == "route-session" {
		return s.routeExecutionSession(ctx, state, item, client, intent)
	}
	if intent.Operation == "call-service" {
		if ref == nil || strings.TrimSpace(intent.Service) == "" || strings.TrimSpace(intent.Method) == "" || !json.Valid(intent.Params) {
			return nil, errors.New("authorized service call requires exact reference, target and params")
		}
		grant, err := client.Inspect(ctx, executionauth.InspectRequest{Reference: *ref, Consumer: item.SubjectID})
		if err != nil {
			return nil, err
		}
		if grant.Scope.Kind != "exact-run" {
			return nil, serviceError("execution_authorization_required", "service effects require an exact run")
		}
		action := "service/" + intent.Service + "/" + intent.Method
		ctx, err = s.RestoreExecutionAuthorization(ctx, pluginID, *ref, grant.Scope.SessionID, action, grantResources(grant.Scope)...)
		if err != nil {
			return nil, err
		}
		caller, ok := principal.FromContext(ctx)
		if !ok {
			return nil, serviceError("execution_authorization_required", "restored service principal unavailable")
		}
		if err = s.checkGrantAdmission(ctx, caller, action, grantResources(grant.Scope)); err != nil {
			return nil, err
		}
		result, callErr := state.Host.CallService(ctx, item.ID, intent.Service, intent.Method, intent.Params)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	}
	if intent.Operation == "describe" {
		result, err := client.Describe(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			executionauth.PolicyInfo
			HostFeatures []string `json:"host_features"`
		}{result, []string{"session-route.v1"}})
	}
	if intent.Operation == "inspect" {
		if ref == nil {
			return nil, errors.New("execution reference required")
		}
		result, err := client.Inspect(ctx, executionauth.InspectRequest{Reference: *ref, Consumer: item.SubjectID})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	caller, authenticated := principal.FromContext(ctx)
	if intent.Operation == "revoke" {
		if ref == nil || !authenticated {
			return nil, errors.New("authenticated revocation required")
		}
		grant, err := client.Inspect(ctx, executionauth.InspectRequest{Reference: *ref, Consumer: item.SubjectID})
		if err != nil {
			return nil, err
		}
		if err := s.checkGrantAdmission(ctx, caller, "execution/grant/revoke", grantResources(grant.Scope)); err != nil {
			return nil, err
		}
		request := executionauth.RevokeRequest{Reference: *ref, Scope: grant.Scope}
		admission := pluginhost.ServiceAdmission{Version: 1, Principal: caller.Identity(), Authority: caller.DurableAuthority(), Action: "execution/grant/revoke", InputDigest: executionauth.RequestDigest(request)}
		result, err := client.Revoke(pluginhost.WithServiceAdmission(ctx, executionauth.ConsumerModule, executionauth.ServiceName, "revoke", admission), request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if intent.Operation != "issue" || strings.TrimSpace(intent.RequestID) == "" || len(intent.RequestedActions) == 0 || len(intent.RequestedActions) > 64 || intent.SessionID == "" {
		return nil, errors.New("invalid execution issuance intent")
	}
	var parent *executionauth.IssueResult
	authorityService := intent.AuthorityService
	if intent.Parent != nil {
		grant, err := client.Inspect(ctx, executionauth.InspectRequest{Reference: *intent.Parent, Consumer: item.SubjectID})
		if err != nil {
			return nil, err
		}
		parent = &grant
		authorityService = grant.Scope.Authority.Service
		ctx, err = s.RestoreExecutionAuthorization(ctx, pluginID, *intent.Parent, "", "execution/grant/derive", grantResources(grant.Scope)...)
		if err != nil {
			return nil, err
		}
		caller, authenticated = principal.FromContext(ctx)
	}
	if !authenticated || !caller.Valid() {
		return nil, serviceError("execution_authorization_required", "authenticated execution issuance required")
	}
	if parent == nil && access.DelegationFromContext(ctx) != nil {
		return nil, serviceError("execution_authorization_required", "fresh execution admission requires live authenticated authority, not a delegated grant")
	}
	authority, err := selectedTaskAuthority(state, item, authorityService)
	if err != nil {
		return nil, err
	}
	lineage, err := s.resolveExecutionLineage(ctx, state, item, authority, intent, parent)
	if err != nil {
		return nil, err
	}
	maxDepth := intent.MaxDepth
	if parent != nil {
		maxDepth = parent.Scope.Bounds.MaxDepth
	}
	scope := scopeForLineage(s.UserBinding(), item, intent.Kind, lineage, maxDepth)
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	policy, err := client.Describe(ctx)
	if err != nil {
		return nil, err
	}
	expiry := intent.ExpiresAt
	if expiry.IsZero() {
		return nil, errors.New("persist an explicit expires_at before requesting execution authorization")
	}
	if expiry.After(time.Now().Add(time.Duration(policy.MaxLifetimeSeconds) * time.Second)) {
		return nil, errors.New("requested grant lifetime exceeds selected policy")
	}
	if parent != nil && expiry.After(parent.ExpiresAt) {
		expiry = parent.ExpiresAt
	}
	actions := slices.Clone(intent.RequestedActions)
	if scope.Kind == "task-lineage" && !slices.Contains(actions, "execution/grant/derive") {
		actions = append(actions, "execution/grant/derive")
	}
	capabilities := make([]executionauth.Capability, 0, len(actions))
	resources := grantResources(scope)
	if lineage.Route != nil {
		resources = append(resources, executionRouteResource(lineage.Route.Locator))
	}
	for _, action := range actions {
		if strings.TrimSpace(action) == "" {
			return nil, errors.New("empty execution action")
		}
		if parent == nil {
			if err := s.checkGrantAdmission(ctx, caller, action, append(slices.Clone(resources), access.Resource{Kind: "session", ID: lineage.SessionID})); err != nil {
				return nil, err
			}
		}
		capabilities = append(capabilities, executionauth.Capability{Action: action, Resources: slices.Clone(resources)})
	}
	request := executionauth.IssueRequest{RequestID: intent.RequestID, Scope: scope, Capabilities: capabilities, ExpiresAt: expiry, Parent: intent.Parent}
	admissionAction, authorityKind := "execution/grant/issue", caller.DurableAuthority()
	if parent != nil {
		admissionAction = "execution/grant/derive"
		authorityKind = "task-lineage"
	}
	if err := s.checkGrantAdmission(ctx, caller, admissionAction, grantResources(scopeForAdmission(scope, parent))); err != nil {
		return nil, err
	}
	attestation := pluginhost.ServiceAdmission{Version: 1, Principal: caller.Identity(), Authority: authorityKind, Action: admissionAction, InputDigest: executionauth.RequestDigest(request)}
	if parent != nil {
		if scope.Kind == "task-lineage" {
			lineage.RunID = ""
			lineage.SessionID = ""
			lineage.ModelProvider = ""
			lineage.Model = ""
			lineage.PermissionMode = ""
		}
		attestation.Lineage, _ = json.Marshal(lineage)
	}
	result, err := client.Issue(pluginhost.WithServiceAdmission(ctx, executionauth.ConsumerModule, executionauth.ServiceName, "issue", attestation), request)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
func scopeForAdmission(scope executionauth.Scope, parent *executionauth.IssueResult) executionauth.Scope {
	if parent != nil {
		return parent.Scope
	}
	return scope
}

// RestoreExecutionAuthorization validates an opaque grant against its original
// selected authority and current task lineage. It never recreates owner status.
func (s *Session) RestoreExecutionAuthorization(ctx context.Context, pluginID string, reference executionauth.Reference, sessionID, action string, resources ...access.Resource) (context.Context, error) {
	origin := ctx
	initialState, err := s.executionState(ctx)
	if err != nil {
		return ctx, err
	}
	defer initialState.Close()
	// Freeze the admitted graph before publishing the principal. Revalidation
	// can run concurrently, and must not silently move a grant to a newer graph.
	admittedGeneration := initialState.Generation
	validate := func(check context.Context) (executionauth.IssueResult, error) {
		source := origin
		if HasInvocationState(check) {
			// Accepted work retains its own handle after the admitting callback
			// returns. A failed supplied capture must never fall back to current.
			source = check
		}
		state, err := s.executionState(source)
		if err != nil {
			return executionauth.IssueResult{}, err
		}
		defer state.Close()
		if state.Generation != admittedGeneration {
			return executionauth.IssueResult{}, serviceError("execution_authorization_required", "execution invocation belongs to a different generation")
		}
		item, err := executionPackage(state, pluginID)
		if err != nil {
			return executionauth.IssueResult{}, err
		}
		client, err := executionClient(state, &reference)
		if err != nil {
			return executionauth.IssueResult{}, err
		}
		grant, err := client.Inspect(check, executionauth.InspectRequest{Reference: reference, Consumer: item.SubjectID})
		if err != nil {
			return grant, err
		}
		if sessionID != "" && grant.Scope.Kind == "exact-run" && grant.Scope.SessionID != sessionID {
			return grant, serviceError("execution_authorization_required", "grant belongs to a different session")
		}
		intent := ExecutionAuthorizationIntent{Kind: grant.Scope.Kind, TaskID: grant.Scope.TaskID, SessionID: grant.Scope.SessionID, RequestID: grant.Scope.RunID, Action: action, Reference: &reference}
		lineage, err := s.resolveExecutionLineage(check, state, item, grant.Scope.Authority, intent, nil)
		if err != nil {
			return grant, err
		}
		lineage.Depth = grant.Scope.Depth
		current := scopeForLineage(s.UserBinding(), item, grant.Scope.Kind, lineage, grant.Scope.Bounds.MaxDepth)
		if !current.Equal(grant.Scope) {
			return grant, serviceError("execution_authorization_required", "execution task scope or selected bounds changed")
		}
		validation, err := client.Validate(check, executionauth.ValidateRequest{Reference: reference, Scope: grant.Scope, Action: action, Resources: resources})
		if err != nil {
			return grant, err
		}
		if !validation.Valid || validation.Principal != grant.Principal {
			return grant, serviceError("execution_authorization_required", "execution grant is expired, revoked or outside scope")
		}
		return grant, nil
	}
	grant, err := validate(ctx)
	if err != nil {
		return ctx, err
	}
	caller, err := principal.Authenticate(ctx, executionGrantAuthenticator(func(check context.Context) (principal.Authentication, error) {
		if validating, _ := check.Value(executionValidationKey{}).(bool); validating {
			return principal.Authentication{}, errors.New("recursive execution authorization validation")
		}
		check = context.WithValue(check, executionValidationKey{}, true)
		// Use the caller's cancellation and retained execution context. The selected
		// business authority must still observe current control within that capture.
		currentGrant, err := validate(check)
		if err != nil {
			return principal.Authentication{}, err
		}
		if admittedGeneration == nil || admittedGeneration.refs.Load() <= 0 || currentGrant.Principal != grant.Principal {
			return principal.Authentication{}, serviceError("execution_authorization_required", "admitted generation is no longer live")
		}
		current, err := s.RetainState()
		if err != nil {
			return principal.Authentication{}, err
		}
		defer current.Close()
		selected, err := executionClient(current, &reference)
		if err != nil {
			return principal.Authentication{}, err
		}
		verdict, err := selected.Validate(check, executionauth.ValidateRequest{Reference: reference, Scope: grant.Scope, Action: action, Resources: resources})
		if err != nil {
			return principal.Authentication{}, err
		}
		if !verdict.Valid || verdict.Principal != grant.Principal {
			return principal.Authentication{}, serviceError("execution_authorization_required", "execution grant is no longer valid")
		}
		return principal.Authentication{Identity: grant.Principal, ExpiresAt: grant.ExpiresAt}, nil
	}), principal.Credential{})
	if err != nil {
		return ctx, err
	}
	ctx = principal.WithContext(ctx, caller)
	ctx = context.WithValue(ctx, executionScopeKey{}, &ExecutionAuthorizationBinding{scope: grant.Scope, valid: true})
	return access.WithDelegation(ctx, executionauth.Proof(reference, grant.Scope)), nil
}

type executionCreationKey struct{}

func ExecutionCreationResources(ctx context.Context) ([]access.Resource, bool) {
	resources, ok := ctx.Value(executionCreationKey{}).([]access.Resource)
	return slices.Clone(resources), ok && ctx.Err() == nil
}

// RestoreExecutionCreation admits one exact, provider-observed pending child.
// It does not turn a task-lineage grant into authority over arbitrary sessions.
func (s *Session) RestoreExecutionCreation(ctx context.Context, pluginID string, params pluginhost.SessionCreateParams) (context.Context, pluginhost.SessionCreateParams, error) {
	if params.ExecutionAuthorization == nil || params.AuthorizationTaskID == "" || params.RequestID == "" {
		return ctx, params, errors.New("pending child creation authorization required")
	}
	state, err := s.executionState(ctx)
	if err != nil {
		return ctx, params, err
	}
	defer state.Close()
	item, err := executionPackage(state, pluginID)
	if err != nil {
		return ctx, params, err
	}
	client, err := executionClient(state, params.ExecutionAuthorization)
	if err != nil {
		return ctx, params, err
	}
	parent, err := client.Inspect(ctx, executionauth.InspectRequest{Reference: *params.ExecutionAuthorization, Consumer: item.SubjectID})
	if err != nil {
		return ctx, params, err
	}
	if parent.Scope.Kind != "task-lineage" {
		return ctx, params, errors.New("child creation requires explicit task-lineage authority")
	}
	resources := grantResources(parent.Scope)
	ctx, err = s.RestoreExecutionAuthorization(ctx, pluginID, *params.ExecutionAuthorization, "", "user/session/create", resources...)
	if err != nil {
		return ctx, params, err
	}
	// The parent observer supplies its actual responsible session; a plugin cannot
	// redirect source history or a launch payload through an unrelated session.
	root, err := s.resolveExecutionLineage(ctx, state, item, parent.Scope.Authority, ExecutionAuthorizationIntent{Kind: parent.Scope.Kind, TaskID: parent.Scope.TaskID}, nil)
	if err != nil {
		return ctx, params, err
	}
	if params.ParentSessionID != "" && params.ParentSessionID != root.SessionID {
		return ctx, params, errors.New("creation source is outside parent task authority")
	}
	if params.Seed != nil || params.Launch != nil || (params.ContextSource != "fresh" && params.ContextSource != "fork") {
		return ctx, params, errors.New("delegated creation cannot introduce an unadmitted seed or launch")
	}
	if params.ModelAlias != "" || ((params.Provider == "") != (params.Model == "")) {
		return ctx, params, errors.New("delegated creation requires an exact model selection")
	}
	selection := session.Session{Provider: params.Provider, Model: params.Model, PermissionMode: params.PermissionMode}
	// Omission consumes the exact choice persisted at grant admission, never
	// whichever global default happens to be selected when dispatch resumes.
	if selection.Provider == "" {
		if len(parent.Scope.Bounds.ModelProviders) != 1 || len(parent.Scope.Bounds.Models) != 1 {
			return ctx, params, errors.New("delegated creation requires an exact admitted model selection")
		}
		selection.Provider, selection.Model = parent.Scope.Bounds.ModelProviders[0], parent.Scope.Bounds.Models[0]
	}
	if selection.PermissionMode == "" {
		if len(parent.Scope.Bounds.PermissionModes) != 1 {
			return ctx, params, errors.New("delegated creation requires an exact admitted permission mode")
		}
		selection.PermissionMode = parent.Scope.Bounds.PermissionModes[0]
	}
	provider, model, mode, err := s.executionSelection(state, selection)
	if err != nil {
		return ctx, params, err
	}
	if !slices.Contains(parent.Scope.Bounds.ModelProviders, provider) || !slices.Contains(parent.Scope.Bounds.Models, model) || !slices.Contains(parent.Scope.Bounds.PermissionModes, mode) || parent.Scope.Depth+1 > parent.Scope.Bounds.MaxDepth {
		return ctx, params, errors.New("child creation exceeds admitted execution bounds")
	}
	creation := params
	creation.ExecutionAuthorization = nil
	request := struct {
		Parent    executionauth.IssueResult      `json:"parent"`
		Kind      string                         `json:"kind"`
		TaskID    string                         `json:"task_id"`
		RequestID string                         `json:"request_id"`
		Creation  pluginhost.SessionCreateParams `json:"creation"`
	}{parent, "exact-run", params.AuthorizationTaskID, params.RequestID, creation}
	raw, err := json.Marshal(request)
	if err != nil {
		return ctx, params, err
	}
	result, callErr := state.Host.CallService(ctx, executionauth.ConsumerModule, parent.Scope.Authority.Service, "resolve-lineage", raw)
	if callErr != nil {
		return ctx, params, callErr
	}
	var observed struct {
		executionauth.Lineage
		Creation pluginhost.SessionCreateParams `json:"creation"`
	}
	if err = json.Unmarshal(result, &observed); err != nil {
		return ctx, params, err
	}
	want, _ := json.Marshal(creation)
	got, _ := json.Marshal(observed.Creation)
	if string(want) != string(got) || observed.TaskID != params.AuthorizationTaskID || observed.ParentTaskID != parent.Scope.TaskID || observed.ParentRevision != parent.Scope.Revision || observed.SessionID != "" || observed.RunID == "" || observed.Revision < 1 || observed.ContextDigest == "" {
		return ctx, params, errors.New("selected authority did not attest exact pending creation")
	}
	// The authority attested the original durable intent above. Carry its
	// resolved bounds into actual creation so that it cannot read defaults again.
	params.Provider, params.Model, params.PermissionMode = provider, model, mode
	return context.WithValue(ctx, executionCreationKey{}, resources), params, nil
}

type executionValidationKey struct{}
type executionGrantAuthenticator func(context.Context) (principal.Authentication, error)

func (f executionGrantAuthenticator) Authenticate(ctx context.Context, _ principal.Credential) (principal.Authentication, error) {
	return f(ctx)
}

type executionScopeKey struct{}

// ExecutionAuthorizationBinding is process-local verified admission provenance.
// Its authority fields cannot be deserialized or reconstructed by a caller.
type ExecutionAuthorizationBinding struct {
	scope executionauth.Scope
	valid bool
}

func CaptureExecutionAuthorization(ctx context.Context) *ExecutionAuthorizationBinding {
	binding, _ := ctx.Value(executionScopeKey{}).(*ExecutionAuthorizationBinding)
	if binding == nil || !binding.valid {
		return nil
	}
	return binding
}
func WithExecutionAuthorization(ctx context.Context, binding *ExecutionAuthorizationBinding) context.Context {
	if binding == nil || !binding.valid {
		return ctx
	}
	return context.WithValue(ctx, executionScopeKey{}, binding)
}

// ExecutionAuthorizationScope returns only host-validated restoration metadata.
func ExecutionAuthorizationScope(ctx context.Context) (executionauth.Scope, bool) {
	binding := CaptureExecutionAuthorization(ctx)
	if binding == nil || ctx.Err() != nil {
		return executionauth.Scope{}, false
	}
	scope := binding.scope
	scope.Bounds.ModelProviders = slices.Clone(scope.Bounds.ModelProviders)
	scope.Bounds.Models = slices.Clone(scope.Bounds.Models)
	scope.Bounds.PermissionModes = slices.Clone(scope.Bounds.PermissionModes)
	return scope, true
}
