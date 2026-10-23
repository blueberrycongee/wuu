package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
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
		return marshalServiceResult(pluginprotocol.DriverTurnReadResult{LogicalModelRequestV1: logicalModelRequest(request), Messages: driverMessagesToWire(request.Messages)})
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
		current, err := turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: input.Revision, Messages: driverMessagesFromWire(input.Messages)})
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(driverContextToWire(current))
	case "model":
		var input pluginprotocol.DriverTurnModelParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		logical := input.Request.LogicalModelRequestV1
		logical.Messages = make([]pluginhost.ModelMessageV1, 0, len(input.Request.Messages))
		for _, message := range input.Request.Messages {
			logical.Messages = append(logical.Messages, message.ModelMessageV1)
		}
		request, err := applyLogicalModelRequest(providers.ChatRequest{}, logical)
		if err != nil {
			return nil, serviceError("invalid_request", err.Error())
		}
		round, err := turn.CallModel(ctx, request)
		if err != nil {
			return nil, err
		}
		result := pluginprotocol.DriverTurnModelResult{Messages: driverMessagesToWire(round.Messages)}
		if round.Usage != nil {
			usage := modelTokenUsageToLogical(*round.Usage)
			result.Usage = &usage
		}
		return marshalServiceResult(result)
	case "tools":
		var input pluginprotocol.DriverTurnToolsParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		batch := loopdriver.ToolBatch{ID: input.BatchID, Messages: driverMessagesFromWire(input.Messages)}
		for _, call := range input.Calls {
			batch.Calls = append(batch.Calls, modelToolCallFromLogical(call))
		}
		messages, err := turn.CallTools(ctx, batch)
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnToolsResult{Messages: driverMessagesToWire(messages)})
	case "complete":
		var input pluginprotocol.DriverTurnCompleteParams
		if err := decodeServiceParams(params.Params, &input); err != nil {
			return nil, err
		}
		receipt, err := turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: input.Content, Messages: driverMessagesFromWire(input.Messages)})
		if err != nil {
			return nil, err
		}
		return marshalServiceResult(pluginprotocol.DriverTurnCompleteResult{ReceiptID: receipt.ID})
	default:
		return nil, serviceError("method_not_found", "unknown driver.turn method")
	}
}

func driverContextToWire(current loopdriver.TurnContext) pluginprotocol.DriverTurnContextResult {
	return pluginprotocol.DriverTurnContextResult{Messages: driverMessagesToWire(current.Messages), Revision: current.Revision, ArchiveHead: current.ArchiveHead, CanReplace: current.CanReplace}
}

func driverMessagesToWire(messages []providers.ChatMessage) []pluginprotocol.DriverMessageV1 {
	result := make([]pluginprotocol.DriverMessageV1, 0, len(messages))
	for _, message := range messages {
		result = append(result, pluginprotocol.DriverMessageV1{ModelMessageV1: modelChatMessageToLogical(message), ToolInvocationID: message.ToolInvocationID, ClientID: message.ClientID, Seq: message.Seq, Steered: message.Steered})
	}
	return result
}

func driverMessagesFromWire(messages []pluginprotocol.DriverMessageV1) []providers.ChatMessage {
	result := make([]providers.ChatMessage, 0, len(messages))
	for _, message := range messages {
		item := modelChatMessageFromLogical(message.ModelMessageV1)
		item.ToolInvocationID, item.ClientID, item.Seq, item.Steered = message.ToolInvocationID, message.ClientID, message.Seq, message.Steered
		result = append(result, item)
	}
	return result
}
