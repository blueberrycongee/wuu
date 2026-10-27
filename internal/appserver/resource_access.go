package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/principal"
)

type authorizedScope struct {
	action    string
	resources []access.Resource
	delegated bool
}

type authorizedRequest struct {
	generation uint64
	extra      []authorizedScope
	ctx        context.Context
	action     string
	resources  []access.Resource
	delegated  bool
}
type connectionAccess struct {
	principal principal.Verified
	policy    access.Policy
	mu        sync.Mutex
	pending   map[string]authorizedRequest
	sequence  uint64
}

// WithPrincipal binds trusted transport provenance. Network embeddings must use
// this or principal.WithContext; a missing network identity must not use stdio's
// explicit local-owner default. No app-server JSON method changes this binding.
func WithPrincipal(identity principal.Verified) ServerOption {
	return func(s *Server) error {
		if !identity.Valid() {
			return errors.New("verified connection principal required")
		}
		s.access.principal = identity
		return nil
	}
}

// WithAccessPolicy is an explicit embedding policy selection, not a fallback.
// The ordinary product uses the selected resource.authorization service.
func WithAccessPolicy(policy access.Policy) ServerOption {
	return func(s *Server) error {
		if policy == nil {
			return errors.New("resource policy required")
		}
		s.access.policy = policy
		return nil
	}
}
func (s *Server) ConnectionPrincipal() principal.Verified { return s.access.principal }
func (s *Server) Principal(ctx context.Context) principal.Verified {
	if p, ok := principal.FromContext(ctx); ok {
		return p
	}
	return s.ConnectionPrincipal()
}
func (s *Server) AuthorizeResource(ctx context.Context, action string, resources ...access.Resource) error {
	return s.authorizeResource(ctx, action, false, resources...)
}

// Delegated operations never borrow a paired controller's ambient owner grant.
func (s *Server) AuthorizeDelegatedResource(ctx context.Context, action string, resources ...access.Resource) error {
	return s.authorizeResource(ctx, action, true, resources...)
}
func (s *Server) authorizeResource(ctx context.Context, action string, delegated bool, resources ...access.Resource) error {
	caller := s.Principal(ctx)
	if !caller.Valid() {
		return &access.Denied{Action: action, Reason: "unauthenticated"}
	}
	request := access.Request{Principal: caller.Identity(), Owner: caller.IsOwner() && !delegated, Action: action, Resources: resources, Attributes: access.AttributesFromContext(ctx)}
	deadline := time.Now().Add(5 * time.Second)
	if expiry := caller.ExpiresAt(); !expiry.IsZero() && expiry.Before(deadline) {
		deadline = expiry
	}
	checkCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := caller.Check(checkCtx); err != nil {
		return &access.Denied{Action: action, Reason: "authentication-expired-or-revoked"}
	}
	if err := access.Check(checkCtx, s.access.policy, request); err != nil {
		return err
	}
	// Authentication can expire or be revoked while the policy provider awaits.
	if err := caller.Check(checkCtx); err != nil || !caller.Valid() {
		return &access.Denied{Action: action, Reason: "authentication-expired-or-revoked"}
	}
	return nil
}
func (s *Server) authorizeRequest(ctx context.Context, req Request) error {
	if !s.Principal(ctx).IsOwner() && !delegatedResourceActions[req.Method] {
		return &access.Denied{Action: req.Method, Reason: "delegated-action-unsupported"}
	}
	// Federation's validated operation envelope is authorized by its handler,
	// with owner authority removed before any source/receipt/provider access.
	if req.Method == "session/federation" {
		return nil
	}
	resources, err := s.requestResources(req)
	if err != nil {
		return &access.Denied{Action: req.Method, Reason: "resource-invalid"}
	}
	return s.AuthorizeResource(ctx, req.Method, resources...)
}
func (s *Server) trackAuthorizedRequest(ctx context.Context, req Request) error {
	if len(req.ID) == 0 {
		return nil
	}
	resources, err := s.requestResources(req)
	// The federation handler checks the operation before and after dispatch.
	if req.Method == "session/federation" {
		return nil
	}
	if err != nil {
		return err
	}
	if resourceMethods[req.Method] == "workspace" || req.Method == MethodThreadStart {
		if err := s.AuthorizeResource(ctx, req.Method, resources...); err != nil {
			return err
		}
	}
	s.access.mu.Lock()
	defer s.access.mu.Unlock()
	if s.access.pending == nil {
		s.access.pending = make(map[string]authorizedRequest)
	}
	if _, exists := s.access.pending[string(req.ID)]; exists {
		return errors.New("request id is already in flight")
	}
	if len(s.access.pending) >= 4096 {
		return errors.New("too many pending authorized requests")
	}
	s.access.sequence++
	s.access.pending[string(req.ID)] = authorizedRequest{generation: s.access.sequence, ctx: ctx, action: req.Method, resources: resources}
	return nil
}

// RegisterAuthorizedResponse is for protocol handlers with operation-specific
// envelopes. It binds the response and every transport replay to the same
// delegated action and resources before the handler loads data or receipts.
func (s *Server) RegisterAuthorizedResponse(ctx context.Context, req Request, action string, delegated bool, resources ...access.Resource) error {
	if err := s.authorizeResource(ctx, action, delegated, resources...); err != nil {
		return err
	}
	if len(req.ID) == 0 {
		return errors.New("authorized operation requires a request id")
	}
	s.access.mu.Lock()
	defer s.access.mu.Unlock()
	if s.access.pending == nil {
		s.access.pending = make(map[string]authorizedRequest)
	}
	if _, exists := s.access.pending[string(req.ID)]; exists {
		return errors.New("request id is already in flight")
	}
	if len(s.access.pending) >= 4096 {
		return errors.New("too many pending authorized requests")
	}
	s.access.sequence++
	s.access.pending[string(req.ID)] = authorizedRequest{generation: s.access.sequence, ctx: ctx, action: action, resources: append([]access.Resource(nil), resources...), delegated: delegated}
	return nil
}

// AppendAuthorizedResponseGuard binds data-dependent result scopes to the original
// request. For example, a list response also requires each returned row's inspect
// action. Every scope is checked again on output and every transport replay.
func (s *Server) AppendAuthorizedResponseGuard(req Request, action string, delegated bool, resources ...access.Resource) error {
	s.access.mu.Lock()
	pending, ok := s.access.pending[string(req.ID)]
	s.access.mu.Unlock()
	if !ok {
		return errors.New("authorized request is not pending")
	}
	if err := s.authorizeResource(pending.ctx, action, delegated, resources...); err != nil {
		return err
	}
	s.access.mu.Lock()
	defer s.access.mu.Unlock()
	current, ok := s.access.pending[string(req.ID)]
	if !ok || current.generation != pending.generation {
		return errors.New("authorized request changed")
	}
	current.extra = append(current.extra, authorizedScope{action: action, delegated: delegated, resources: append([]access.Resource(nil), resources...)})
	s.access.pending[string(req.ID)] = current
	return nil
}

// requestEffectContext exposes only execution intent; message bodies, tokens,
// provider credentials and arbitrary request fields never enter policy input.
func requestEffectContext(ctx context.Context, req Request) (context.Context, error) {
	var params map[string]json.RawMessage
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &params) != nil {
		return nil, errors.New("invalid request parameters")
	}
	attributes := make(map[string]json.RawMessage)
	for _, key := range []string{"provider", "model", "permission_mode", "engine"} {
		raw, ok := params[key]
		if !ok || string(raw) == "null" {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, errors.New("invalid requested execution intent")
		}
		attributes[key] = raw
	}
	return access.WithAttributes(ctx, attributes), nil
}

// requestResources names only the resource fields understood by the selected
// method. Extra fields cannot downgrade a global method to a shared session.
// Unknown methods deliberately have no wildcard/global authorization fallback.
func requestResources(req Request) ([]access.Resource, error) {
	rule, ok := resourceMethods[req.Method]
	if !ok {
		return nil, errors.New("unknown action")
	}
	var params map[string]json.RawMessage
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, err
		}
	}
	if rule == "global" && req.Method != MethodThreadStart {
		return []access.Resource{{Kind: "global"}}, nil
	}
	var resources []access.Resource
	if req.Method == MethodThreadStart {
		resources = append(resources, access.Resource{Kind: "global"})
		rule = "session"
	}
	add := func(kind string, raw json.RawMessage) error {
		if len(raw) == 0 {
			return nil
		}
		var id string
		if json.Unmarshal(raw, &id) != nil || strings.TrimSpace(id) == "" {
			return errors.New("invalid resource id")
		}
		for _, old := range resources {
			if old.Kind == kind && old.ID == id {
				return nil
			}
		}
		resources = append(resources, access.Resource{Kind: kind, ID: id})
		return nil
	}
	fields := []string{"thread_id", "session_id", "main_thread_id", "source_thread_id", "scope_id", "project_id"}
	switch rule {
	case "session-share":
		fields = []string{"share_id"}
	case "workspace":
		fields = []string{"root", "cwd"}
	}
	for _, field := range fields {
		if err := add(rule, params[field]); err != nil {
			return nil, err
		}
	}
	if rule == "session" {
		// Starting/forking from another conversation touches both sources.
		var handoff map[string]json.RawMessage
		if raw := params["handoff"]; len(raw) > 0 && json.Unmarshal(raw, &handoff) != nil {
			return nil, errors.New("invalid handoff")
		}
		for _, field := range []string{"thread_id", "source_thread_id", "parent_session_id"} {
			if err := add("session", handoff[field]); err != nil {
				return nil, err
			}
		}
	}
	if len(resources) == 0 {
		if rule == "workspace" || req.Method == MethodThreadResume {
			return []access.Resource{{Kind: "global"}}, nil
		}
		return nil, errors.New("resource identity required")
	}
	return resources, nil
}

// Event envelopes carry authoritative thread ids at the envelope or typed
// wrapper, never inferred from arbitrary message bodies or tool output.
func notificationResources(params any) ([]access.Resource, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("invalid notification")
	}
	var resources []access.Resource
	collect := func(fields map[string]json.RawMessage, keys ...string) error {
		for _, key := range keys {
			if value, ok := fields[key]; ok {
				var id string
				if json.Unmarshal(value, &id) != nil || strings.TrimSpace(id) == "" {
					return errors.New("invalid event resource")
				}
				resources = append(resources, access.Resource{Kind: "session", ID: id})
			}
		}
		return nil
	}
	if err := collect(fields, "thread_id", "main_thread_id"); err != nil {
		return nil, err
	}
	for _, key := range []string{"thread", "run", "activity", "question", "process"} {
		if value, ok := fields[key]; ok {
			var nested map[string]json.RawMessage
			if json.Unmarshal(value, &nested) != nil {
				return nil, errors.New("invalid event wrapper")
			}
			keys := []string{"thread_id", "session_id"}
			if key == "thread" {
				keys = []string{"id"}
			}
			if err := collect(nested, keys...); err != nil {
				return nil, err
			}
		}
	}
	if len(resources) == 0 {
		return []access.Resource{{Kind: "global"}}, nil
	}
	return resources, nil
}

func (s *Server) filterAuthorizedResult(ctx context.Context, action string, result any) any {
	allowed := func(id string) bool {
		return s.AuthorizeResource(ctx, action, access.Resource{Kind: "session", ID: id}) == nil
	}
	switch value := result.(type) {
	case WorkspaceListResult:
		allowedWorkspace := func(path string) (access.Resource, bool) {
			resource, err := resolvedWorkspaceResource(path)
			return resource, err == nil && s.AuthorizeResource(ctx, action, resource) == nil
		}
		filtered := make([]WorkspaceInfo, 0, len(value.Workspaces))
		for _, workspace := range value.Workspaces {
			if resource, ok := allowedWorkspace(workspace.Path); ok {
				workspace.Path = resource.ID
				filtered = append(filtered, workspace)
			}
		}
		value.Workspaces = filtered
		if resource, ok := allowedWorkspace(value.Current); ok {
			value.Current = resource.ID
		} else {
			value.Current = ""
			value.CurrentID = ""
		}
		return value
	case ThreadListResult:
		filtered := make([]Thread, 0, len(value.Threads))
		for _, thread := range value.Threads {
			if allowed(thread.ID) {
				filtered = append(filtered, thread)
			}
		}
		value.Threads = filtered
		return value
	case ThreadSearchResult:
		filtered := make([]ThreadSearchResultItem, 0, len(value.Results))
		for _, item := range value.Results {
			if allowed(item.Thread.ID) {
				filtered = append(filtered, item)
			}
		}
		value.Results = filtered
		return value
	}
	return result
}

// GuardedWriter is implemented by replaying transports. A buffered frame keeps
// its original authorization closure and must recheck it immediately before
// every delivery. The closure is process-local; never put it or owner facts on
// the public wire. Denial drops the complete frame, including list metadata.
type GuardedWriter interface {
	WriteAuthorized([]byte, func() error) (int, error)
}

func (s *Server) authorizeOutbound(ctx context.Context, value any) (any, func() error, bool) {
	var action string
	var delegated bool
	var extra []authorizedScope
	var resources []access.Resource
	checkCtx := ctx
	switch envelope := value.(type) {
	case Notification:
		var err error
		resources, err = notificationResources(envelope.Params)
		if err != nil {
			return nil, nil, false
		}
		if !s.Principal(ctx).IsOwner() {
			scope, known := delegatedEventScopes[envelope.Method]
			if !known || scope == "session" && (len(resources) == 0 || resources[0].Kind != "session") {
				return nil, nil, false
			}
		}
		action = "event:" + envelope.Method
	case Request:
		var err error
		resources, err = notificationResources(envelope.Params)
		if err != nil {
			return nil, nil, false
		}
		if !s.Principal(ctx).IsOwner() && (!delegatedReverseActions[envelope.Method] || len(resources) == 0 || resources[0].Kind != "session") {
			return nil, nil, false
		}
		action = "request:" + envelope.Method
	case Response:
		s.access.mu.Lock()
		pending, ok := s.access.pending[string(envelope.ID)]
		delete(s.access.pending, string(envelope.ID))
		s.access.mu.Unlock()
		if ok {
			checkCtx = pending.ctx
			action = pending.action
			resources = pending.resources
			delegated = pending.delegated
			extra = pending.extra
		}
		if ok {
			authorizationErr := s.authorizeResource(checkCtx, action, delegated, resources...)
			for _, scope := range extra {
				if authorizationErr != nil {
					break
				}
				authorizationErr = s.authorizeResource(checkCtx, scope.action, scope.delegated, scope.resources...)
			}
			if authorizationErr != nil {
				envelope.Result = nil
				envelope.Error = &ResponseError{Code: "access_denied", Message: authorizationErr.Error()}
			} else {
				envelope.Result = s.filterAuthorizedResult(checkCtx, action, envelope.Result)
			}
			// Replay must cover every row actually emitted, not merely the list action.
			switch result := envelope.Result.(type) {
			case WorkspaceListResult:
				for _, workspace := range result.Workspaces {
					resources = append(resources, access.Resource{Kind: "workspace", ID: workspace.Path})
				}
				if result.Current != "" {
					resources = append(resources, access.Resource{Kind: "workspace", ID: result.Current})
				}
			case ThreadListResult:
				for _, thread := range result.Threads {
					resources = append(resources, access.Resource{Kind: "session", ID: thread.ID})
				}
			case ThreadSearchResult:
				for _, item := range result.Results {
					resources = append(resources, access.Resource{Kind: "session", ID: item.Thread.ID})
				}
			}
		}
		value = envelope
	}
	caller := s.Principal(checkCtx)
	// Reconnect replay outlives a single request deadline, but not the connection
	// principal or current policy. Request cancellation still fences live writes.
	guardCtx := principal.WithContext(context.WithoutCancel(checkCtx), caller)
	guard := func() error {
		if !caller.Valid() {
			return &access.Denied{Action: action, Reason: "unauthenticated"}
		}
		if action == "" {
			return nil
		}
		if err := s.authorizeResource(guardCtx, action, delegated, resources...); err != nil {
			return err
		}
		for _, scope := range extra {
			if err := s.authorizeResource(guardCtx, scope.action, scope.delegated, scope.resources...); err != nil {
				return err
			}
		}
		return nil
	}
	// Denial responses are safe to deliver even though the original action is
	// denied. They contain no protected business payload, even after expiry.
	if response, ok := value.(Response); ok && response.Error != nil && response.Error.Code == "access_denied" {
		guard = func() error { return nil }
	}
	if err := guard(); err != nil {
		return nil, nil, false
	}
	return value, guard, true
}
