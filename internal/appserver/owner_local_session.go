package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const ownerLocalSessionMethod = "session/owner-local"
const ownerLocalReceiptNamespace = "session.owner-local.operations.v1"

// Controller identity is attested by the source native gateway, not plugin
// arguments. RunID constrains effects but is deliberately not actor identity.
type ownerLocalController struct {
	Consumer      string `json:"consumer"`
	UserSpaceID   string `json:"user_space_id"`
	ProductDomain string `json:"product_domain"`
	SessionID     string `json:"session_id"`
	RunID         string `json:"run_id"`
}
type ownerLocalSessionRequest struct {
	Request    pluginhost.SessionRouteRequest `json:"request"`
	Controller *ownerLocalController          `json:"controller,omitempty"`
}

// This identity is local to the Desktop's registered runtimes, not a global
// machine identifier. Connection-generation ownership remains a separate fence.
func (s *Server) ownerLocalSessionLocator() pluginhost.SessionLocatorV1 {
	id := "desktop-local:" + federationDigest([]any{s.rt.RootDir, s.rt.SessionDir, s.rt.UserBinding()})
	return pluginhost.SessionLocatorV1{Version: 1, ExecutionHost: id, StorageHost: id}
}
func (s *Server) ownerLocalSessionContext(ctx context.Context, source principal.Verified) (context.Context, error) {
	owner := s.ConnectionPrincipal()
	if !s.trustedTransport.enabled || !owner.IsOwner() || owner.Identity() != principal.LocalOwner().Identity() || s.rt == nil || s.rt.HostInfo().Kind != runtime.HostLocal || s.rt.UserBinding().IsUser() {
		return nil, errors.New("owner-local control requires a registered private local Coding connection")
	}
	if forwarded, ok := principal.FromContext(ctx); ok && (!forwarded.IsOwner() || forwarded.Identity() != owner.Identity()) {
		return nil, errors.New("nested owner-local control forbidden")
	}
	if source.IsOwner() {
		return nil, errors.New("source transport must not forward owner authority")
	}
	constrained, err := owner.Constrain(source.Check)
	if err != nil {
		return nil, err
	}
	if err = constrained.Check(ctx); err != nil {
		return nil, err
	}
	return principal.WithContext(ctx, constrained), nil
}

// Only the trusted transport dispatcher invokes this entry; it is intentionally
// absent from the ordinary app-server/plugin RPC method table.
func (s *Server) handleOwnerLocalSession(ctx context.Context, source principal.Verified, req Request) error {
	var input ownerLocalSessionRequest
	if err := decodeParams(req.Params, &input); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	authorized, err := s.ownerLocalSessionContext(ctx, source)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	resource := access.Resource{Kind: "session", ID: input.Request.Locator.SessionID}
	if input.Request.Operation == "list" {
		resource = access.Resource{Kind: "workspace", ID: s.rt.RootDir}
	}
	if err = s.RegisterAuthorizedResponse(authorized, req, ownerLocalSessionMethod+"."+input.Request.Operation, false, resource); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if !s.startBackground(func() {
		result, err := s.dispatchOwnerLocalSession(ctx, source, input)
		_ = s.writeResponse(req.ID, result, err)
	}) {
		return s.writeResponse(req.ID, nil, errServerClosed)
	}
	return nil
}
func (s *Server) dispatchOwnerLocalSession(ctx context.Context, source principal.Verified, input ownerLocalSessionRequest) (pluginhost.SessionRouteResult, error) {
	empty := pluginhost.SessionRouteResult{}
	ctx, err := s.ownerLocalSessionContext(ctx, source)
	if err != nil {
		return empty, err
	}
	request := input.Request
	if err = request.Validate(); err != nil {
		return empty, err
	}
	locator := s.ownerLocalSessionLocator()
	if request.Locator.ExecutionHost != locator.ExecutionHost || request.Locator.StorageHost != "" && request.Locator.StorageHost != locator.StorageHost {
		return empty, errors.New("owner-local target locator differs from registered receiver")
	}
	if request.Operation != "list" && (input.Controller == nil || input.Controller.Consumer == "" || input.Controller.UserSpaceID == "" || input.Controller.ProductDomain == "" || input.Controller.SessionID == "" || input.Controller.RunID == "") {
		return empty, errors.New("attested source controller and exact run required")
	}
	actorKey := []any{source.Identity(), "catalog"}
	if c := input.Controller; c != nil {
		actorKey = []any{source.Identity(), c.Consumer, c.UserSpaceID, c.ProductDomain, c.SessionID}
	}
	actor := "desktop-controller-" + federationDigest(actorKey)
	resource := access.Resource{Kind: "session", ID: request.Locator.SessionID}
	if request.Operation == "list" {
		resource = access.Resource{Kind: "workspace", ID: s.rt.RootDir}
	}
	recheck := func() error { return s.AuthorizeResource(ctx, ownerLocalSessionMethod+"."+request.Operation, resource) }
	if err = recheck(); err != nil {
		return empty, err
	}
	state, err := s.rt.RetainState()
	if err != nil {
		return empty, err
	}
	defer state.Close()
	ctx = runtime.WithInvocationState(ctx, state)
	var execute func() (any, error)
	switch request.Operation {
	case "send":
		var p pluginhost.SessionSendParams
		if err = decodeParams(request.Params, &p); err != nil {
			return empty, err
		}
		if p.SessionID != request.Locator.SessionID || p.RequestID != request.OperationID || p.RequestID != input.Controller.RunID || p.IfRunning != "" && p.IfRunning != "queue" || p.UserInputRequestID != "" || p.ReplyToTurnID != "" || p.ControlRevision != 0 || p.ExecutionAuthorization != nil || p.ResumeAccepted || p.ResumeReceiptID != "" || p.Relation != nil {
			return empty, errors.New("owner-local send requires its exact nonhuman queued run")
		}
		execute = func() (any, error) { return s.sendPluginSession(ctx, actor, p) }
	case "inspect":
		var p pluginhost.SessionInspectParams
		if err = decodeParams(request.Params, &p); err != nil {
			return empty, err
		}
		if p.SessionID != request.Locator.SessionID || p.RequestID != input.Controller.RunID || p.TurnID != "" || p.ExecutionAuthorization != nil {
			return empty, errors.New("owner-local inspect requires exact source run")
		}
		execute = func() (any, error) { return s.inspectPluginSession(ctx, actor, p) }
	case "cancel":
		var p pluginhost.SessionCancelParams
		if err = decodeParams(request.Params, &p); err != nil {
			return empty, err
		}
		if p.SessionID != request.Locator.SessionID || (p.TurnID == "") == (p.QueueID == "") || p.ExecutionAuthorization != nil || p.ControlRevision != 0 {
			return empty, errors.New("owner-local cancel requires one exact queue or turn")
		}
		exact, err := s.inspectPluginSession(ctx, actor, pluginhost.SessionInspectParams{SessionID: p.SessionID, RequestID: input.Controller.RunID})
		if err != nil {
			return empty, err
		}
		if exact.Turn == nil || p.TurnID != "" && p.TurnID != exact.Turn.TurnID || p.QueueID != "" && p.QueueID != exact.Turn.QueueID {
			return empty, errors.New("cancel target does not belong to exact source run")
		}
		execute = func() (any, error) { return s.cancelPluginSession(ctx, actor, p) }
	case "list":
		var p pluginhost.SessionListParams
		if err = decodeParams(request.Params, &p); err != nil {
			return empty, err
		}
		if p.Scope != pluginhost.SessionListScopeShared {
			return empty, errors.New("owner-local discovery only lists shared existing sessions")
		}
		execute = func() (any, error) {
			list, err := s.listPluginSessions(ctx, actor, p)
			if err != nil {
				return nil, err
			}
			allowed := list.Sessions[:0]
			for _, item := range list.Sessions {
				if s.AuthorizeResource(ctx, ownerLocalSessionMethod+".inspect", access.Resource{Kind: "session", ID: item.SessionID}) == nil {
					allowed = append(allowed, item)
				}
			}
			list.Sessions = allowed
			return list, nil
		}
	default:
		return empty, errors.New("owner-local control supports only existing-session send, inspect, cancel and discovery")
	}
	if strings.TrimSpace(request.OperationID) == "" {
		return empty, errors.New("stable operation identity required")
	}
	return s.executeSessionRouteOperation(ctx, state, ownerLocalReceiptNamespace, locator.ExecutionHost, locator.StorageHost, actor, request, &s.ownerLocalOperations, execute, recheck)
}
