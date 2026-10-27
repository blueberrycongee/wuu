package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/personalreview"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	MethodStateInspectionsList      = "state/inspections/list"
	MethodStateInspectionsReconcile = "state/inspections/reconcile"
)

type stateInspectionParams struct {
	Target         string               `json:"target"`
	ID             string               `json:"id,omitempty"`
	ExpectedOwner  string               `json:"expected_owner,omitempty"`
	Namespaces     []string             `json:"namespaces,omitempty"`
	Subject        string               `json:"subject,omitempty"`
	Scope          pluginsettings.Scope `json:"scope,omitempty"`
	Outcome        string               `json:"outcome,omitempty"`
	Reason         string               `json:"reason,omitempty"`
	WritersStopped bool                 `json:"writers_stopped,omitempty"`
}

// Recovery is a distinct operator operation, not an execution grant, approval
// or automatic retry. It records disposition without changing action receipts.
func (s *Server) handleStateInspections(ctx context.Context, req Request) error {
	caller, ok := principal.FromContext(ctx)
	if !ok || !caller.Valid() || !caller.IsOwner() {
		return s.writeResponse(req.ID, nil, errors.New("authenticated owner maintenance is required"))
	}
	var params stateInspectionParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	reconcile := req.Method == MethodStateInspectionsReconcile
	if reconcile && (!params.WritersStopped || strings.TrimSpace(params.Reason) == "") {
		return s.writeResponse(req.ID, nil, errors.New("manual reconciliation requires writers_stopped and recorded disposition evidence"))
	}
	if params.Scope == "" {
		params.Scope = pluginsettings.ScopeUser
	}
	if s.rt.UserBinding().IsUser() && params.Scope != pluginsettings.ScopeUser {
		return s.writeResponse(req.ID, nil, errors.New("user-host maintenance cannot inspect workspace state"))
	}
	state, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer state.Close()
	var consumer, service string
	switch params.Target {
	case "settings":
		consumer, service = runtime.StateServicesConsumer, pluginsettings.ServiceName
	case "sessions":
		consumer, service = runtime.SessionStateConsumer, session.ServiceName
	case "review-policy":
		if s.personalActions == nil {
			return s.writeResponse(req.ID, nil, errors.New("selected review ledger is unavailable"))
		}
		held := s.personalActions.state.Retain()
		if held == nil {
			return s.writeResponse(req.ID, nil, errors.New("selected review ledger generation is closed"))
		}
		defer held.Close()
		state = held
		consumer, service = personalActionsConsumerID, persistence.ServiceName
	default:
		return s.writeResponse(req.ID, nil, errors.New("inspection target must be settings, sessions, or review-policy"))
	}
	if state.Host == nil || state.Host.ServiceRegistry() == nil {
		return s.writeResponse(req.ID, nil, errors.New("selected inspection provider is unavailable"))
	}
	provider, ok := state.Host.ServiceRegistry().ProviderPluginIDFor(consumer, service, 1)
	if !ok {
		return s.writeResponse(req.ID, nil, errors.New("selected inspection provider is unavailable"))
	}
	resources := []access.Resource{{Kind: "state-provider", ID: provider}}
	if s.rt.UserBinding().IsUser() {
		resources = append(resources, access.Resource{Kind: "user-space", ID: s.rt.UserBinding().UserSpaceID})
	}
	if err := s.AuthorizeResource(ctx, req.Method, resources...); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.appendContextResponseScope(ctx, req.Method, resources...)
	if reconcile && s.personalActions != nil {
		select {
		case s.personalActions.effectGate <- struct{}{}:
			defer func() { <-s.personalActions.effectGate }()
		case <-ctx.Done():
			return s.writeResponse(req.ID, nil, ctx.Err())
		}
	}
	serviceScope, err := state.Host.OpenServiceScope(ctx, consumer, service)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer serviceScope.Close()
	ctx = serviceScope.Context
	// Only this authenticated maintenance ingress can attest exact provider
	// bytes; ordinary consumers cannot turn a declared storage call into repair.
	call := func(callCtx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method == "list-inspections" || method == "ListInspections" || method == "reconcile-inspection" || method == "ReconcileInspection" {
			if err := caller.Check(callCtx); err != nil {
				return nil, err
			}
			if err := s.AuthorizeResource(callCtx, req.Method, resources...); err != nil {
				return nil, err
			}
			callCtx = pluginhost.WithServiceAdmission(callCtx, consumer, service, method, pluginhost.ServiceAdmission{
				Version: 1, Principal: caller.Identity(), Authority: caller.DurableAuthority(),
				Action: req.Method, InputDigest: executionauth.RawRequestDigest(raw),
			})
		}
		result, callErr := state.Host.CallService(callCtx, consumer, service, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	}
	resolution := persistence.InspectionResolution{InspectionRequest: persistence.InspectionRequest{ID: params.ID, Namespaces: params.Namespaces}, ExpectedOwner: params.ExpectedOwner, Outcome: params.Outcome, Reason: params.Reason}
	var result any
	var topology persistence.InspectionTopology
	switch params.Target {
	case "settings":
		settings := pluginsettings.NewClient(call)
		if !reconcile {
			result, err = settings.ListInspections(ctx, params.Scope)
			break
		}
		topology, err = settings.InspectionAuthority(ctx)
		if err == nil {
			err = requireSingleHostInspection(topology)
		}
		if err == nil {
			result, err = settings.ReconcileInspection(ctx, pluginsettings.InspectionResolution{InspectionRequest: pluginsettings.InspectionRequest{ReadRequest: pluginsettings.ReadRequest{Subject: params.Subject, Scope: params.Scope}, ID: params.ID}, ExpectedOwner: params.ExpectedOwner, Outcome: params.Outcome, Reason: params.Reason})
		}
	case "sessions":
		sessions := session.NewClient(ctx, call)
		if !reconcile {
			result, err = sessions.ListInspections(ctx)
			break
		}
		topology, err = sessions.InspectionAuthority(ctx)
		if err == nil {
			err = requireSingleHostInspection(topology)
		}
		if err == nil {
			result, err = sessions.ReconcileInspection(ctx, resolution)
		}
	case "review-policy":
		store, storeErr := personalreview.NewDocumentStore(persistence.NewClient(call))
		if storeErr != nil {
			return s.writeResponse(req.ID, nil, storeErr)
		}
		defer store.Close()
		if !reconcile {
			result, err = store.ListPolicyInspections(ctx)
			break
		}
		topology, err = store.PolicyInspectionAuthority(ctx)
		if err == nil {
			err = requireSingleHostInspection(topology)
		}
		if err == nil {
			result, err = store.ReconcilePolicyInspection(ctx, resolution)
		}
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, map[string]any{"target": params.Target, "provider": provider, "manual_reconciliation_only": true, "result": result}, nil)
}

func requireSingleHostInspection(authority persistence.InspectionTopology) error {
	if authority.Version != 1 || authority.StorageTopology != persistence.SingleHost || authority.ExecutionTopology != persistence.SingleHost {
		return errors.New("inspection recovery requires provider-supported exclusive writer proof; use offline provider maintenance for shared or multi-host authority")
	}
	return nil
}
