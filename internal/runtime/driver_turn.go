package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

type driverTurnInvoker struct{ parent *kernelHostServices }

func (i *driverTurnInvoker) ID() string                { return i.parent.ID() }
func (i *driverTurnInvoker) Status() pluginhost.Status { return i.parent.Status() }

func (i *driverTurnInvoker) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	var scope pluginprotocol.DriverTurnScope
	if err := json.Unmarshal(params.Params, &scope); err != nil {
		return nil, serviceError("invalid_request", "invalid driver turn scope")
	}
	gateway, hostErr := i.parent.driverGateways.authorizedGateway(params.Caller, scope.ExecutionID)
	if hostErr != nil {
		return nil, hostErr
	}
	turn, ok := gateway.(loopdriver.TurnGateway)
	if !ok {
		return nil, serviceError("service_unavailable", "execution does not expose independent turn operations")
	}
	switch params.Method {
	case "read":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		request, err := turn.ReadTurn(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(modelwire.DriverRequestToWire(request))
	case "context":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		current, err := turn.ReadContext(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(driverContextToWire(current))
	case "replace_context":
		var input pluginprotocol.DriverTurnReplaceContextParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		current, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: input.Revision, Messages: modelwire.DriverMessagesFromWire(input.Messages)})
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(driverContextToWire(current))
	case "model":
		var input pluginprotocol.DriverTurnModelParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		request, err := modelwire.ApplyDriverRequest(input.Request)
		if err != nil {
			return nil, serviceError("invalid_request", err.Error())
		}
		round, err := turn.CallModel(ctx, request)
		if err != nil {
			return nil, err
		}
		result := pluginprotocol.DriverTurnModelResult{Messages: modelwire.DriverMessagesToWire(round.Messages)}
		if round.Usage != nil {
			usage := modelwire.TokenUsageToWire(*round.Usage)
			result.Usage = &usage
		}
		return marshalServiceResult(result)
	case "tools":
		var input pluginprotocol.DriverTurnToolsParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		batch := loopdriver.ToolBatch{ID: input.BatchID, Messages: modelwire.DriverMessagesFromWire(input.Messages)}
		for _, call := range input.Calls {
			batch.Calls = append(batch.Calls, modelwire.ToolCallFromWire(call))
		}
		messages, err := turn.CallTools(ctx, batch)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnToolsResult{Messages: modelwire.DriverMessagesToWire(messages)})
	case "complete":
		var input pluginprotocol.DriverTurnCompleteParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		receipt, err := turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: input.Content, Messages: modelwire.DriverMessagesFromWire(input.Messages)})
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnCompleteResult{ReceiptID: receipt.ID})
	default:
		return nil, serviceError("method_not_found", "unknown driver.turn method")
	}
}

func driverContextToWire(current loopdriver.TurnContext) pluginprotocol.DriverTurnContextResult {
	return pluginprotocol.DriverTurnContextResult{Messages: modelwire.DriverMessagesToWire(current.Messages), Revision: current.Revision, ArchiveHead: current.ArchiveHead, CanReplace: current.CanReplace}
}
