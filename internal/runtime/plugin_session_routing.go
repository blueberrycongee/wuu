package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Route preserves the local session implementation and routes explicit remote
// locators only through the caller's selected, declared transport provider.
func (r *PluginSessionRouter) Route(ctx context.Context, pluginID string, request pluginhost.SessionRouteRequest) (pluginhost.SessionRouteResult, error) {
	if err := request.Validate(); err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	if request.Locator.ExecutionHost != "local" {
		binding, err := RetainInvocationState(ctx)
		if err != nil {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("session transport unavailable: %w", err)
		}
		defer binding.Close()
		if binding.Host == nil || binding.Host.ServiceRegistry() == nil {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("session transport unavailable")
		}
		raw, err := json.Marshal(request)
		if err != nil {
			return pluginhost.SessionRouteResult{}, err
		}
		output, callErr := binding.Host.CallService(ctx, pluginID, pluginhost.SessionTransportService, "dispatch", raw)
		if callErr != nil {
			return pluginhost.SessionRouteResult{}, callErr
		}
		var result pluginhost.SessionRouteResult
		invalid := json.Unmarshal(output, &result) != nil || result.Version != 1 || result.Locator.Version != 1 || result.OperationID != request.OperationID || result.Locator.ExecutionHost != request.Locator.ExecutionHost
		invalid = invalid || (result.Status != "completed" && result.Status != "pending" && result.Status != "unknown")
		invalid = invalid || (request.Operation != "create" && result.Locator.SessionID != request.Locator.SessionID)
		invalid = invalid || (request.Operation == "create" && result.Status == "completed" && result.Error == nil && result.Locator.SessionID == "")
		if invalid {
			return pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: request.Locator, Status: "unknown", Error: &pluginhost.SessionRouteError{Code: "session_invalid_receipt", Message: "selected transport returned an invalid receipt; reconcile before issuing new work"}}, nil
		}
		return result, nil
	}
	if request.Locator.StorageHost != "" && request.Locator.StorageHost != "local" {
		return pluginhost.SessionRouteResult{}, fmt.Errorf("storage placement cannot be changed by a session locator")
	}
	var result any
	var err error
	switch request.Operation {
	case "create":
		var p pluginhost.SessionCreateParams
		if err = decodeServiceParams(request.Params, &p); err != nil {
			break
		}
		if p.RequestID != "" && p.RequestID != request.OperationID {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("request_id must equal operation_id")
		}
		p.RequestID = request.OperationID
		var created pluginhost.SessionCreateResult
		created, err = r.Create(ctx, pluginID, p)
		result = created
		request.Locator.SessionID = created.SessionID
	case "send":
		var p pluginhost.SessionSendParams
		if err = decodeServiceParams(request.Params, &p); err != nil {
			break
		}
		if p.RequestID != "" && p.RequestID != request.OperationID {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("request_id must equal operation_id")
		}
		p.RequestID = request.OperationID
		if p.SessionID != request.Locator.SessionID {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("session_id must match locator")
		}
		result, err = r.Send(ctx, pluginID, p)
	case "inspect":
		var p pluginhost.SessionInspectParams
		if err = decodeServiceParams(request.Params, &p); err != nil {
			break
		}
		if p.SessionID != request.Locator.SessionID {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("session_id must match locator")
		}
		result, err = r.Inspect(ctx, pluginID, p)
	case "cancel":
		var p pluginhost.SessionCancelParams
		if err = decodeServiceParams(request.Params, &p); err != nil {
			break
		}
		if p.SessionID != request.Locator.SessionID || (p.TurnID == "" && p.QueueID == "") {
			return pluginhost.SessionRouteResult{}, fmt.Errorf("cancel requires matching session and exact turn_id or queue_id")
		}
		result, err = r.Cancel(ctx, pluginID, p)
	case "list":
		var p pluginhost.SessionListParams
		if err = decodeServiceParams(request.Params, &p); err != nil {
			break
		}
		result, err = r.List(ctx, pluginID, p)
	}
	if err != nil {
		return pluginhost.SessionRouteResult{}, err
	}
	raw, err := json.Marshal(result)
	return pluginhost.SessionRouteResult{Version: 1, OperationID: request.OperationID, Locator: request.Locator, Status: "completed", Result: raw}, err
}
