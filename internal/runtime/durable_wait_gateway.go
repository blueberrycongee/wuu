package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/durablewait"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/principal"
)

func waitCallsDigest(calls []pluginprotocol.ModelToolCallV1) string {
	raw, _ := json.Marshal(calls)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Admission here attests a host-owned driver invocation, not a human decision or
// permission grant. The ordinary tool authorization path still runs afterward.
func (k *kernelHostServices) callDurableWait(ctx context.Context, method string, request durablewait.Request) (json.RawMessage, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	proof := pluginhost.ServiceAdmission{Version: 1, Principal: principal.Identity{Kind: "agent", Issuer: "wuu:driver", Tenant: request.Owner.SessionID, Subject: request.Owner.PluginID}, Action: "durable-wait." + method, Authority: request.Owner.ExecutionID, InputDigest: hex.EncodeToString(sum[:])}
	ctx = pluginhost.WithServiceAdmission(ctx, k.ID(), durablewait.ServiceName, method, proof)
	result, callErr := k.registry.Call(ctx, k.ID(), pluginhost.ServiceCallParams{Service: durablewait.ServiceName, Method: method, Params: raw})
	if callErr != nil {
		return nil, callErr
	}
	return result, nil
}
func (k *kernelHostServices) registerDurableWait(ctx context.Context, caller string, gateway loopdriver.KernelGateway, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		pluginprotocol.DriverTurnScope
		durablewait.Intent
		Calls []pluginprotocol.ModelToolCallV1 `json:"calls,omitempty"`
	}
	if err := decodeServiceParams(raw, &request); err != nil {
		return nil, err
	}
	gate, ok := gateway.(loopdriver.WaitGateway)
	if !ok {
		return nil, serviceError("service_unavailable", "driver has no pre-effect wait boundary")
	}
	var result json.RawMessage
	register := func(ctx context.Context, execution loopdriver.ExecutionContext, fresh bool) error {
		owner := durablewait.Owner{PluginID: caller, SessionID: execution.SessionID, ExecutionID: execution.ExecutionID, CallID: request.ID}
		intent := request.Intent
		if intent.BatchID != "" {
			intent.CallsDigest = waitCallsDigest(request.Calls)
		} else if len(request.Calls) > 0 {
			return serviceError("invalid_request", "calls require batch_id")
		}
		var err error
		result, err = k.callDurableWait(ctx, "register", durablewait.Request{Intent: intent, Owner: owner, Fresh: fresh})
		return err
	}
	if request.BatchID == "" {
		execution := gate.WaitExecution()
		if request.Kind == "input" || request.Kind == "event" {
			var err error
			// Registration captures a position, not an answer. A plugin event can
			// establish the boundary of a new human-input wait, but only the
			// kind-specific source check in resolveDurableWait can resolve it.
			execution, err = gate.WaitInput(ctx, "event")
			if err != nil {
				return nil, err
			}
			if execution.SourceMessage == nil {
				return nil, serviceError("invalid_request", "wait requires an admitted input boundary")
			}
			request.InputBoundary = &durablewait.InputRef{SessionID: execution.SourceMessage.SessionID, Seq: execution.SourceMessage.Seq}
		}
		if err := register(ctx, execution, false); err != nil {
			return nil, err
		}
	} else {
		batch := loopdriver.ToolBatch{ID: request.BatchID}
		for _, call := range request.Calls {
			batch.Calls = append(batch.Calls, modelwire.ToolCallFromWire(call))
		}
		if err := gate.ReserveWait(ctx, batch, register); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (k *kernelHostServices) resolveDurableWait(ctx context.Context, caller string, gateway loopdriver.KernelGateway, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		pluginprotocol.DriverTurnScope
		WaitID string `json:"wait_id"`
	}
	if err := decodeServiceParams(raw, &input); err != nil {
		return nil, err
	}
	gate, ok := gateway.(loopdriver.WaitGateway)
	if !ok {
		return nil, serviceError("service_unavailable", "driver has no admitted input boundary")
	}
	execution := gate.WaitExecution()
	// Inspection is host-attested and scoped to this live driver; the provider
	// still checks the original owner before accepting resolution.
	inspect := durablewait.Request{SessionID: execution.SessionID, WaitID: input.WaitID, Owner: durablewait.Owner{PluginID: caller, SessionID: execution.SessionID, ExecutionID: execution.ExecutionID, CallID: input.WaitID}}
	recordRaw, err := k.callDurableWait(ctx, "inspect", inspect)
	if err != nil {
		return nil, err
	}
	var record durablewait.Record
	if err := json.Unmarshal(recordRaw, &record); err != nil {
		return nil, err
	}
	if record.Owner.PluginID != caller {
		return nil, serviceError("permission_denied", "wait belongs to another driver")
	}
	if record.Kind != "input" && record.Kind != "event" {
		return nil, serviceError("wait_conflict", "input cannot approve an effect")
	}
	execution, err = gate.WaitInput(ctx, record.Kind)
	if err != nil {
		return nil, err
	}
	if execution.SourceMessage == nil {
		return recordRaw, nil
	}
	inspect.Source = durablewait.InputRef{SessionID: execution.SourceMessage.SessionID, Seq: execution.SourceMessage.Seq}
	return k.callDurableWait(ctx, "resolve", inspect)
}
