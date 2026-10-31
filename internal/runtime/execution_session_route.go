package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
)

func validateExecutionRouteBinding(binding *executionauth.SessionRouteBinding) error {
	if binding == nil || binding.Locator.Version != 1 || strings.TrimSpace(binding.Locator.ExecutionHost) == "" || strings.TrimSpace(binding.Locator.SessionID) == "" || len(binding.Operations) == 0 || len(binding.Operations) > 3 {
		return errors.New("existing session route binding is incomplete")
	}
	seen := map[string]bool{}
	for _, operation := range binding.Operations {
		if !slices.Contains([]string{"send", "inspect", "cancel"}, operation) || seen[operation] {
			return errors.New("existing session binding permits only distinct send, inspect and cancel operations")
		}
		seen[operation] = true
	}
	return nil
}
func executionRouteResource(locator pluginhost.SessionLocatorV1) access.Resource {
	// Display/storage hints cannot create another authority for the same target.
	return access.Resource{Kind: "session-route", ID: executionauth.RequestDigest([]string{locator.ExecutionHost, locator.SessionID})}
}
func sameExecutionTarget(a, b pluginhost.SessionLocatorV1) bool {
	return a.Version == b.Version && a.ExecutionHost == b.ExecutionHost && a.SessionID == b.SessionID && a.StorageHost == b.StorageHost
}

func validateExecutionSessionRoute(binding *executionauth.SessionRouteBinding, runID string, request pluginhost.SessionRouteRequest) error {
	if err := validateExecutionRouteBinding(binding); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if !sameExecutionTarget(binding.Locator, request.Locator) || !slices.Contains(binding.Operations, request.Operation) || runID == "" {
		return errors.New("execution authorization does not cover this session route")
	}
	switch request.Operation {
	case "send":
		var p pluginhost.SessionSendParams
		if err := decodeServiceParams(request.Params, &p); err != nil {
			return err
		}
		if request.OperationID != runID || p.RequestID != runID || p.SessionID != binding.Locator.SessionID || p.IfRunning != "" && p.IfRunning != "queue" || p.ReplyToTurnID != "" || p.UserInputRequestID != "" || p.ResumeAccepted || p.ResumeReceiptID != "" || p.ExecutionAuthorization != nil || p.ControlRevision != 0 || p.Relation != nil {
			return errors.New("routed execution send requires its exact nonhuman queued request")
		}
	case "inspect":
		var p pluginhost.SessionInspectParams
		if err := decodeServiceParams(request.Params, &p); err != nil {
			return err
		}
		if p.RequestID != runID || p.SessionID != binding.Locator.SessionID || p.ExecutionAuthorization != nil || p.TurnID != "" {
			return errors.New("routed inspection requires its exact request")
		}
	case "cancel":
		var p pluginhost.SessionCancelParams
		if err := decodeServiceParams(request.Params, &p); err != nil {
			return err
		}
		if p.SessionID != binding.Locator.SessionID || p.ExecutionAuthorization != nil || p.ControlRevision != 0 || (p.TurnID == "") == (p.QueueID == "") {
			return errors.New("routed cancellation requires one exact turn or queue")
		}
	default:
		return errors.New("unsupported existing session operation")
	}
	return nil
}

// A controller's owned conversation remains the grant anchor. The target stays
// owned by its original runtime; neither routing nor authority claims adopt it.
func (s *Session) routeExecutionSession(ctx context.Context, state *State, item pluginpkg.Plugin, client *executionauth.Client, intent ExecutionAuthorizationIntent) (json.RawMessage, error) {
	if intent.Reference == nil || intent.Route == nil {
		return nil, errors.New("session route requires an exact execution reference and request")
	}
	grant, err := client.Inspect(ctx, executionauth.InspectRequest{Reference: *intent.Reference, Consumer: item.SubjectID})
	if err != nil {
		return nil, err
	}
	if grant.Scope.Kind != "exact-run" {
		return nil, errors.New("session route requires an exact run")
	}
	request := *intent.Route
	action := "session/route/" + request.Operation
	lineage, err := s.resolveExecutionLineage(ctx, state, item, grant.Scope.Authority, ExecutionAuthorizationIntent{Kind: grant.Scope.Kind, TaskID: grant.Scope.TaskID, SessionID: grant.Scope.SessionID, RequestID: grant.Scope.RunID, Action: action, Reference: intent.Reference}, nil)
	if err != nil {
		return nil, err
	}
	if err = validateExecutionSessionRoute(lineage.Route, grant.Scope.RunID, request); err != nil {
		return nil, err
	}
	resources := append(grantResources(grant.Scope), executionRouteResource(request.Locator))
	ctx, err = s.RestoreExecutionAuthorization(ctx, item.ID, *intent.Reference, grant.Scope.SessionID, action, resources...)
	if err != nil {
		return nil, err
	}
	caller, ok := principal.FromContext(ctx)
	if !ok {
		return nil, errors.New("routed execution principal unavailable")
	}
	if err = s.checkGrantAdmission(ctx, caller, action, resources); err != nil {
		return nil, err
	}
	if s.PluginSessionRouter == nil {
		return nil, errors.New("session transport unavailable")
	}
	if request.Operation == "cancel" {
		// The controller may have several requests in this target. An exact ID alone
		// is insufficient: correlate it to this run before invoking receiver cancel.
		var cancel pluginhost.SessionCancelParams
		if err = json.Unmarshal(request.Params, &cancel); err != nil {
			return nil, err
		}
		params, _ := json.Marshal(pluginhost.SessionInspectParams{SessionID: request.Locator.SessionID, RequestID: grant.Scope.RunID})
		inspected, inspectErr := s.PluginSessionRouter.Route(ctx, item.ID, pluginhost.SessionRouteRequest{Version: 1, Operation: "inspect", OperationID: "cancel-inspect-" + executionauth.RequestDigest(request.OperationID), Locator: request.Locator, Params: params})
		if inspectErr != nil {
			return nil, inspectErr
		}
		if inspected.Status != "completed" || inspected.Error != nil {
			return nil, errors.New("cannot confirm the execution receipt for cancellation")
		}
		var result pluginhost.SessionInspectResult
		if err = json.Unmarshal(inspected.Result, &result); err != nil {
			return nil, err
		}
		turn := result.Turn
		if result.Session.SessionID != request.Locator.SessionID || turn == nil || turn.RequestID != grant.Scope.RunID || cancel.TurnID != "" && cancel.TurnID != turn.TurnID || cancel.QueueID != "" && cancel.QueueID != turn.QueueID {
			return nil, errors.New("cancellation does not identify this execution receipt")
		}
	}
	// Inspection or transport preparation may yield. The restored principal's
	// check revalidates current task revision, grant expiry and revocation.
	if err = caller.Check(ctx); err != nil {
		return nil, err
	}
	result, err := s.PluginSessionRouter.Route(ctx, item.ID, request)
	if err != nil {
		return nil, err
	}
	if err = caller.Check(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
