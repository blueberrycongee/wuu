package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
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
	case "settings":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		settings, err := turn.ReadSettings(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(settings)
	case "context_state":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		state, err := turn.ReadContextState(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(state)
	case "save_context_state":
		var input pluginprotocol.DriverTurnStateParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if err := turn.SaveContextState(ctx, input.State); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "observe":
		var input pluginprotocol.DriverTurnObserveParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		if err := turn.ReportTurn(ctx, input.Observation); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	case "compact":
		var input pluginprotocol.DriverTurnCompactParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		messages, err := turn.CompactContext(ctx, modelwire.DriverMessagesFromWire(input.Messages), input.Budget)
		result := pluginprotocol.DriverTurnCompactResult{Messages: modelwire.DriverMessagesToWire(messages)}
		result.Failure = loopdriver.FailureToWire(err)
		return marshalServiceResult(result)
	case "admit_input":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		admitted, err := turn.AdmitInput(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnAdmissionResult{Messages: modelwire.DriverMessagesToWire(admitted.Messages), Context: driverContextToWire(admitted.Context), Decision: admitted.Decision})
	case "request_context":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		segments, err := turn.ReadRequestContext(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnRequestContextResult{Segments: loopdriver.ContextSegmentsToWire(segments)})
	case "transform_request":
		var input pluginprotocol.DriverTurnModelParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		request, err := modelwire.DriverRequestFromWire(input.Request)
		if err != nil {
			return nil, serviceError("invalid_request", err.Error())
		}
		transformed, err := turn.TransformRequest(ctx, request)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(modelwire.DriverRequestToWire(transformed))
	case "context":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		current, err := turn.ReadContext(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(driverContextToWire(current))
	case "archive_context":
		if err := decodeServiceParams(params.Params, &scope); err != nil {
			return nil, err
		}
		archive, err := turn.ArchiveContext(ctx)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnArchiveResult{Messages: modelwire.DriverMessagesToWire(archive.Messages), Head: archive.Head})
	case "replace_context":
		var input pluginprotocol.DriverTurnReplaceContextParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		current, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: input.Revision, Messages: modelwire.DriverMessagesFromWire(input.Messages), Volatile: input.Volatile})
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(driverContextToWire(current))
	case "model", "model_with_tools":
		var input pluginprotocol.DriverTurnModelParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		request, err := modelwire.ApplyDriverRequest(input.Request)
		if err != nil {
			return nil, serviceError("invalid_request", err.Error())
		}
		var round loopdriver.ModelRound
		var runErr error
		if params.Method == "model_with_tools" {
			round, runErr = turn.CallModelWithTools(ctx, request, input.BatchID)
		} else {
			round, runErr = turn.CallModel(ctx, request)
		}
		result := pluginprotocol.DriverTurnModelResult{CompletedMessages: modelwire.DriverMessagesToWire(round.CompletedMessages), Messages: modelwire.DriverMessagesToWire(round.Messages), ToolBatchID: round.ToolBatchID, ToolWorkStarted: round.ToolWorkStarted}
		result.Failure = loopdriver.FailureToWire(runErr)
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
		receipt, err := turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: input.Content, Messages: modelwire.DriverMessagesFromWire(input.Messages), FinishReason: providers.FinishReason(input.FinishReason), StopReason: input.StopReason, Truncated: input.Truncated})
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
