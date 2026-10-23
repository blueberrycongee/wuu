package loopdriver

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/modelwire"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (g serviceGateway) callTurn(ctx context.Context, method string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	reply, callErr := g.router.RouteServiceCall(ctx, g.caller, pluginhost.ServiceCallParams{
		Service: pluginprotocol.DriverTurnService, Method: method, ExecutionID: g.scopeID, Params: raw,
	})
	if callErr != nil {
		return callErr
	}
	return json.Unmarshal(reply, output)
}

func (g serviceGateway) ReadTurn(ctx context.Context) (providers.ChatRequest, error) {
	var result pluginprotocol.DriverTurnReadResult
	if err := g.callTurn(ctx, "read", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result); err != nil {
		return providers.ChatRequest{}, err
	}
	return modelwire.DriverRequestFromWire(result)
}

func (g serviceGateway) ReadContext(ctx context.Context) (TurnContext, error) {
	var result pluginprotocol.DriverTurnContextResult
	if err := g.callTurn(ctx, "context", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result); err != nil {
		return TurnContext{}, err
	}
	return turnContextFromWire(result), nil
}

func (g serviceGateway) ReplaceContext(ctx context.Context, replacement ContextReplacement) (TurnContext, error) {
	var result pluginprotocol.DriverTurnContextResult
	if err := g.callTurn(ctx, "replace_context", pluginprotocol.DriverTurnReplaceContextParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Revision: replacement.Revision,
		Messages: modelwire.DriverMessagesToWire(replacement.Messages),
	}, &result); err != nil {
		return TurnContext{}, err
	}
	return turnContextFromWire(result), nil
}

func turnContextFromWire(current pluginprotocol.DriverTurnContextResult) TurnContext {
	return TurnContext{Messages: modelwire.DriverMessagesFromWire(current.Messages), Revision: current.Revision, ArchiveHead: current.ArchiveHead, CanReplace: current.CanReplace}
}

func (g serviceGateway) CallModel(ctx context.Context, request providers.ChatRequest) (ModelRound, error) {
	var result pluginprotocol.DriverTurnModelResult
	if err := g.callTurn(ctx, "model", pluginprotocol.DriverTurnModelParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Request: modelwire.DriverRequestToWire(request),
	}, &result); err != nil {
		return ModelRound{}, err
	}
	round := ModelRound{Messages: modelwire.DriverMessagesFromWire(result.Messages)}
	if result.Usage != nil {
		usage := modelwire.TokenUsageFromWire(*result.Usage)
		round.Usage = &usage
	}
	if result.Failure != nil {
		return round, &ModelRoundError{Failure: *result.Failure}
	}
	return round, nil
}

func (g serviceGateway) CallTools(ctx context.Context, batch ToolBatch) ([]providers.ChatMessage, error) {
	input := pluginprotocol.DriverTurnToolsParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, BatchID: batch.ID,
		Messages: modelwire.DriverMessagesToWire(batch.Messages),
	}
	for _, call := range batch.Calls {
		input.Calls = append(input.Calls, modelwire.ToolCallToWire(call))
	}
	var result pluginprotocol.DriverTurnToolsResult
	if err := g.callTurn(ctx, "tools", input, &result); err != nil {
		return nil, err
	}
	return modelwire.DriverMessagesFromWire(result.Messages), nil
}

func (g serviceGateway) CompleteTurn(ctx context.Context, completed TurnResult) (ModelLoopReceipt, error) {
	var result pluginprotocol.DriverTurnCompleteResult
	if err := g.callTurn(ctx, "complete", pluginprotocol.DriverTurnCompleteParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Content: completed.Content,
		Messages: modelwire.DriverMessagesToWire(completed.Messages),
	}, &result); err != nil {
		return ModelLoopReceipt{}, err
	}
	return ModelLoopReceipt{ID: result.ReceiptID}, nil
}
