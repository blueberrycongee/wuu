package appserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const federationReceiptNamespace = "session.federation.operations.v1"

// SessionFederationConfig is an execution ceiling, not an access grant. The
// selected resource policy must separately grant each action to the verified
// principal. An empty model/tool allow-list disables create/send admission.
// This host executes its own sessions; shared storage is not execution failover.
type SessionFederationConfig = config.SessionFederationConfig

// SessionFederation is shared by the ServerOption across connections. The
// durable receipt fences process restart; the active table only distinguishes
// current work from a previously admitted operation with an unknown outcome.
type SessionFederation struct {
	config     SessionFederationConfig
	fromConfig bool
	operations *federationOperations
}

type federationOperations struct {
	mu     sync.Mutex
	active map[string]bool
}

func WithSessionFederation(input SessionFederationConfig) ServerOption {
	raw, _ := json.Marshal(input)
	var copied SessionFederationConfig
	_ = json.Unmarshal(raw, &copied)
	copied.Enabled = true
	if copied.StorageHost == "" {
		copied.StorageHost = copied.HostID
	}
	federation := &SessionFederation{config: copied, operations: &federationOperations{active: make(map[string]bool)}}
	return func(s *Server) error {
		if err := copied.Validate(); err != nil {
			return err
		}
		s.sessionFederation = federation
		return nil
	}
}

// The long-poll inspect must not block cancellation/control on the serial RPC
// connection. Task lifetime remains on the execution host after disconnect.
func (s *Server) handleSessionFederation(ctx context.Context, req Request) error {
	var request pluginhost.SessionRouteRequest
	if err := decodeParams(req.Params, &request); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := request.Validate(); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	f, err := s.currentSessionFederation()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if f == nil {
		return s.writeResponse(req.ID, nil, errors.New("session federation is disabled on this host"))
	}
	ctx, err = federationAuthorizationContext(ctx, request, f.config)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	resource := access.Resource{Kind: "session", ID: request.Locator.SessionID}
	if request.Operation == "create" || request.Operation == "list" {
		resource = access.Resource{Kind: "workspace", ID: s.rt.RootDir}
	}
	if err := s.RegisterAuthorizedResponse(ctx, req, "session/federation."+request.Operation, true, resource); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if !s.startBackground(func() {
		result, err := s.dispatchSessionFederation(ctx, request)
		if err == nil && request.Operation == "list" && result.Error == nil {
			var list pluginhost.SessionListResult
			if err = json.Unmarshal(result.Result, &list); err == nil {
				resources := make([]access.Resource, 0, len(list.Sessions))
				for _, item := range list.Sessions {
					resources = append(resources, access.Resource{Kind: "session", ID: item.SessionID})
				}
				if len(resources) > 0 {
					err = s.AppendAuthorizedResponseGuard(req, "session/federation.inspect", true, resources...)
				}
			}
		}
		_ = s.writeResponse(req.ID, result, err)
	}) {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	return nil
}

type federationReceipt struct {
	Signature string                        `json:"signature"`
	Result    pluginhost.SessionRouteResult `json:"result"`
}

func federationDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// The active operation table survives user configuration refreshes. Each
// request captures an immutable ceiling; disabling the receiver denies new work.
func (s *Server) currentSessionFederation() (*SessionFederation, error) {
	s.mu.Lock()
	current := s.sessionFederation
	s.mu.Unlock()
	if current != nil && !current.fromConfig {
		return current, nil
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return nil, fmt.Errorf("session federation configuration unavailable: %w", err)
	}
	if cfg.SessionFederation == nil || !cfg.SessionFederation.Enabled {
		return nil, nil
	}
	copied := *cfg.SessionFederation
	if err := copied.Validate(); err != nil {
		return nil, err
	}
	if copied.StorageHost == "" {
		copied.StorageHost = copied.HostID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current = s.sessionFederation
	if current != nil && (!current.fromConfig || federationDigest(current.config) == federationDigest(copied)) {
		return current, nil
	}
	operations := &federationOperations{active: make(map[string]bool)}
	if current != nil {
		operations = current.operations
	}
	current = &SessionFederation{config: copied, fromConfig: true, operations: operations}
	s.sessionFederation = current
	return current, nil
}

func (s *Server) dispatchSessionFederation(ctx context.Context, request pluginhost.SessionRouteRequest) (pluginhost.SessionRouteResult, error) {
	empty := pluginhost.SessionRouteResult{}
	f, configErr := s.currentSessionFederation()
	if configErr != nil {
		return empty, configErr
	}
	if f == nil {
		return empty, &pluginhost.HostServiceError{Code: "session_transport_disabled", Message: "session federation is disabled on this host"}
	}
	if err := request.Validate(); err != nil {
		return empty, err
	}
	if request.Locator.ExecutionHost != f.config.HostID {
		return empty, errors.New("session locator targets another execution host")
	}
	if request.Locator.StorageHost != "" && request.Locator.StorageHost != f.config.StorageHost {
		return empty, errors.New("locator cannot change storage authority")
	}
	verified, ok := principal.FromContext(ctx)
	if !ok {
		verified = s.ConnectionPrincipal()
	}
	if !verified.Valid() {
		return empty, errors.New("authenticated federation principal is required")
	}
	ctx = principal.WithContext(ctx, verified)
	var attributeErr error
	ctx, attributeErr = federationAuthorizationContext(ctx, request, f.config)
	if attributeErr != nil {
		return empty, attributeErr
	}
	resource := access.Resource{Kind: "session", ID: request.Locator.SessionID}
	if request.Operation == "create" || request.Operation == "list" {
		resource = access.Resource{Kind: "workspace", ID: s.rt.RootDir}
	}
	if err := s.AuthorizeDelegatedResource(ctx, "session/federation."+request.Operation, resource); err != nil {
		return empty, err
	}
	owner := "federation-" + federationDigest(verified.Identity())
	binding, err := s.rt.RetainState()
	if err != nil {
		return empty, err
	}
	defer binding.Close()
	ctx = runtime.WithInvocationState(ctx, binding)
	execute, err := s.prepareFederationOperation(ctx, binding, owner, request, f.config)
	if err != nil {
		return empty, err
	}
	return s.executeSessionRouteOperation(ctx, binding, federationReceiptNamespace, f.config.HostID, f.config.StorageHost, owner, request, f.operations, execute, func() error {
		if err := s.AuthorizeDelegatedResource(ctx, "session/federation."+request.Operation, resource); err != nil {
			return err
		}
		current, err := s.currentSessionFederation()
		if err != nil {
			return err
		}
		if current == nil || federationDigest(current.config) != federationDigest(f.config) {
			return errors.New("session federation configuration changed before execution")
		}
		return nil
	})
}

// Shared durable admission fences transport-specific policies without conflating
// their identities or receipt namespaces. Reads remain live observations.
func (s *Server) executeSessionRouteOperation(ctx context.Context, binding *runtime.State, namespace, hostID, storageHost, owner string, request pluginhost.SessionRouteRequest, operations *federationOperations, execute func() (any, error), recheck func() error) (pluginhost.SessionRouteResult, error) {
	empty := pluginhost.SessionRouteResult{}
	// Reads are live and never reuse a stale observation receipt.
	if request.Operation == "inspect" || request.Operation == "list" {
		result, err := execute()
		if err != nil {
			return empty, err
		}
		raw, err := json.Marshal(result)
		locator := request.Locator
		locator.StorageHost = storageHost
		return pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: locator, Status: "completed", Result: raw}, err
	}
	key := federationDigest([]string{hostID, owner, request.OperationID})
	intent := request
	intent.Locator.DisplayHost = ""
	intent.Locator.StorageHost = storageHost
	signature := federationDigest(intent)
	var receipt federationReceipt
	admitted := false
	err := persistence.Update(ctx, binding.Documents, func(tx *persistence.Tx) error {
		admitted = false
		found, err := tx.Get(namespace, key, &receipt)
		if err != nil {
			return err
		}
		if found {
			if receipt.Signature != signature {
				return &pluginhost.HostServiceError{Code: "session_operation_conflict", Message: "operation_id already identifies different intent"}
			}
			return nil
		}
		receipt = federationReceipt{Signature: signature, Result: pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: intent.Locator, Status: "unknown"}}
		admitted = true
		return tx.Put(namespace, key, receipt)
	})
	if err != nil {
		return empty, err
	}
	if !admitted {
		operations.mu.Lock()
		active := operations.active[key]
		operations.mu.Unlock()
		if active && receipt.Result.Status == "unknown" {
			receipt.Result.Status = "pending"
		}
		receipt.Result.Locator.DisplayHost = request.Locator.DisplayHost
		return receipt.Result, nil
	}
	operations.mu.Lock()
	if operations.active == nil {
		operations.active = make(map[string]bool)
	}
	operations.active[key] = true
	operations.mu.Unlock()
	defer func() { operations.mu.Lock(); delete(operations.active, key); operations.mu.Unlock() }()
	// Admission can block on a remote store. Recheck current authority after
	// that durability barrier and before starting any session side effect.
	var value any
	actionErr := recheck()
	notExecuted := actionErr != nil
	if actionErr == nil {
		value, actionErr = execute()
	}
	receipt.Result.Status = "completed"
	if actionErr != nil {
		receipt.Result.Status = "unknown"
		if notExecuted {
			receipt.Result.Status = "completed"
		}
		receipt.Result.Error = &pluginhost.SessionRouteError{Code: "session_operation_unconfirmed", Message: "operation outcome is unconfirmed; inspect before issuing new work"}
		if notExecuted {
			receipt.Result.Error = &pluginhost.SessionRouteError{Code: "session_operation_not_executed", Message: "operation was not executed because its authority changed"}
		}
	} else {
		receipt.Result.Result, err = json.Marshal(value)
		if err != nil {
			return empty, err
		}
		if created, ok := value.(pluginhost.SessionCreateResult); ok {
			receipt.Result.Locator.SessionID = created.SessionID
		}
	}
	receipt.Result.Locator.StorageHost = storageHost
	// Cancellation during response delivery does not erase an admitted operation.
	// A failed final commit leaves unknown; no automatic repetition of the effect.
	receiptCtx, cancelReceipt := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelReceipt()
	err = persistence.Update(receiptCtx, binding.Documents, func(tx *persistence.Tx) error {
		var current federationReceipt
		found, err := tx.Get(namespace, key, &current)
		if err != nil {
			return err
		}
		if !found || current.Signature != signature {
			return errors.New("federation operation receipt changed")
		}
		return tx.Put(namespace, key, receipt)
	})
	if err != nil {
		return pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: request.Locator, Status: "unknown", Error: &pluginhost.SessionRouteError{Code: "session_receipt_unconfirmed", Message: "operation may have completed; inspect the session before issuing new work"}}, nil
	}
	receipt.Result.Locator.DisplayHost = request.Locator.DisplayHost
	return receipt.Result, nil
}

// Policies receive normalized requested effects, never prompts, credentials,
// caller-claimed identity, or arbitrary request fields.
func federationAuthorizationContext(ctx context.Context, request pluginhost.SessionRouteRequest, limits SessionFederationConfig) (context.Context, error) {
	values := map[string]any{"operation_id": request.OperationID, "execution_host": request.Locator.ExecutionHost}
	if request.Operation == "create" {
		var p pluginhost.SessionCreateParams
		if err := decodeParams(request.Params, &p); err != nil {
			return ctx, err
		}
		mode := p.PermissionMode
		if mode == "" {
			mode = config.PermissionModeReadOnly
		}
		allow := limits.ToolAllow
		deny := append([]string(nil), limits.ToolDeny...)
		if p.ToolPolicy != nil {
			if len(p.ToolPolicy.Allow) > 0 {
				allow = p.ToolPolicy.Allow
			}
			deny = append(deny, p.ToolPolicy.Deny...)
		}
		values["provider"], values["model"], values["permission_mode"], values["tool_allow"], values["tool_deny"] = p.Provider, p.Model, mode, allow, deny
	}
	attributes := make(map[string]json.RawMessage, len(values))
	for name, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			return ctx, err
		}
		attributes[name] = raw
	}
	return access.WithAttributes(ctx, attributes), nil
}

func (s *Server) prepareFederationOperation(ctx context.Context, binding *runtime.State, owner string, request pluginhost.SessionRouteRequest, limits SessionFederationConfig) (func() (any, error), error) {
	// Namespace host-session request IDs across principals, including globally
	// indexed launch records. A remote principal cannot collide with local IDs.
	requestID := "federation-" + federationDigest([]string{owner, request.OperationID})
	switch request.Operation {
	case "create":
		var p pluginhost.SessionCreateParams
		if err := decodeParams(request.Params, &p); err != nil {
			return nil, err
		}
		if p.RequestID != "" && p.RequestID != request.OperationID {
			return nil, errors.New("request_id must equal operation_id")
		}
		if p.ParentSessionID != "" || p.ContextSource != "fresh" || p.Seed != nil || p.Launch != nil {
			return nil, errors.New("federation v1 supports fresh sessions only; context transfer requires a separate authorized projection")
		}
		if p.WorkspaceRoot != "" || p.WorkspaceID != "" || p.Workspace != "" && p.Workspace != "shared" {
			return nil, errors.New("federation cannot change the host workspace")
		}
		if p.ModelAlias != "" {
			return nil, errors.New("federation requires an explicit provider and model")
		}
		if p.PermissionMode == "" {
			p.PermissionMode = config.PermissionModeReadOnly
		}
		if err := s.validateFederationExecution(limits, p.Provider, p.Model, p.PermissionMode); err != nil {
			return nil, err
		}
		effective := append([]string(nil), limits.ToolAllow...)
		deny := append([]string(nil), limits.ToolDeny...)
		if p.ToolPolicy != nil {
			if len(p.ToolPolicy.Allow) > 0 {
				effective = nil
				for _, name := range p.ToolPolicy.Allow {
					if !slices.Contains(limits.ToolAllow, name) {
						return nil, errors.New("requested tool exceeds delegation ceiling")
					}
					effective = append(effective, name)
				}
			}
			deny = append(deny, p.ToolPolicy.Deny...)
		}
		p.ToolPolicy = &pluginhost.SessionToolPolicy{Allow: effective, Deny: deny}
		p.RequestID = requestID
		return func() (any, error) { return s.createPluginSession(ctx, owner, p) }, nil
	case "send":
		var p pluginhost.SessionSendParams
		if err := decodeParams(request.Params, &p); err != nil {
			return nil, err
		}
		if p.SessionID != request.Locator.SessionID {
			return nil, errors.New("session_id must match locator")
		}
		if p.RequestID != "" && p.RequestID != request.OperationID {
			return nil, errors.New("request_id must equal operation_id")
		}
		p.RequestID = requestID
		metadata, found, err := binding.Sessions.Find(p.SessionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, errors.New("session unavailable")
		}
		if err = s.validateFederationExecution(limits, metadata.Provider, metadata.Model, metadata.PermissionMode); err != nil {
			return nil, err
		}
		var policy pluginhost.SessionToolPolicy
		if json.Unmarshal([]byte(metadata.ToolPolicyJSON), &policy) != nil || len(policy.Allow) == 0 {
			return nil, errors.New("session has no bounded delegation tool policy")
		}
		for _, name := range policy.Allow {
			if !slices.Contains(limits.ToolAllow, name) && !slices.Contains(policy.Deny, name) {
				return nil, errors.New("session tool surface exceeds delegation ceiling")
			}
		}
		for _, name := range limits.ToolDeny {
			if slices.Contains(policy.Allow, name) && !slices.Contains(policy.Deny, name) {
				return nil, errors.New("session tool surface exceeds delegation deny policy")
			}
		}
		return func() (any, error) { return s.sendPluginSession(ctx, owner, p) }, nil
	case "inspect":
		var p pluginhost.SessionInspectParams
		if err := decodeParams(request.Params, &p); err != nil {
			return nil, err
		}
		if p.SessionID != request.Locator.SessionID {
			return nil, errors.New("session_id must match locator")
		}
		if p.RequestID != "" {
			p.RequestID = "federation-" + federationDigest([]string{owner, p.RequestID})
		}
		publicRequestID := ""
		var original pluginhost.SessionInspectParams
		_ = json.Unmarshal(request.Params, &original)
		publicRequestID = original.RequestID
		return func() (any, error) {
			result, err := s.inspectPluginSession(ctx, owner, p)
			if result.Turn != nil && publicRequestID != "" {
				result.Turn.RequestID = publicRequestID
			}
			return result, err
		}, nil
	case "cancel":
		var p pluginhost.SessionCancelParams
		if err := decodeParams(request.Params, &p); err != nil {
			return nil, err
		}
		if p.SessionID != request.Locator.SessionID || (p.TurnID == "" && p.QueueID == "") {
			return nil, errors.New("cancel requires matching session and exact turn_id or queue_id")
		}
		return func() (any, error) { return s.cancelPluginSession(ctx, owner, p) }, nil
	case "list":
		var p pluginhost.SessionListParams
		if err := decodeParams(request.Params, &p); err != nil {
			return nil, err
		}
		if p.Scope != "" && p.Scope != pluginhost.SessionListScopeOwned && p.Scope != pluginhost.SessionListScopeShared {
			return nil, errors.New("federation list scope must be owned or shared")
		}
		return func() (any, error) {
			result, err := s.listPluginSessions(ctx, owner, p)
			if err != nil {
				return nil, err
			}
			visible := make([]pluginhost.SessionSummary, 0, len(result.Sessions))
			for _, item := range result.Sessions {
				if s.AuthorizeDelegatedResource(ctx, "session/federation.inspect", access.Resource{Kind: "session", ID: item.SessionID}) == nil {
					visible = append(visible, item)
				}
			}
			result.Sessions = visible
			return result, nil
		}, nil
	}
	return nil, errors.New("unsupported federation operation")
}

func (s *Server) validateFederationExecution(limits SessionFederationConfig, provider, model, mode string) error {
	if len(limits.ToolAllow) == 0 || !slices.Contains(limits.Models[provider], model) {
		return errors.New("provider/model is outside delegation execution ceiling")
	}
	allowed := limits.PermissionModes
	if len(allowed) == 0 {
		allowed = []string{config.PermissionModeReadOnly}
	}
	if !slices.Contains(allowed, mode) {
		return errors.New("permission_mode is outside delegation execution ceiling")
	}
	rank := func(value string) int {
		switch config.NormalizePermissionMode(value) {
		case config.PermissionModeReadOnly:
			return 0
		case config.PermissionModeStandard:
			return 1
		default:
			return 2
		}
	}
	if rank(mode) > rank(s.rt.Permissions.Mode) {
		return errors.New("delegation cannot broaden host execution permissions")
	}
	if s.rt.PermissionModeExplicit && config.NormalizePermissionMode(s.rt.Permissions.Mode) != mode {
		return errors.New("explicit host permission override is incompatible with delegated session")
	}
	return nil
}
