package loopdriver

import (
	"context"
	"encoding/json"
	"errors"

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
		return errors.Join(callErr, ctx.Err(), context.Cause(ctx))
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

func (g serviceGateway) ReadSettings(ctx context.Context) (pluginprotocol.DriverTurnSettings, error) {
	var result pluginprotocol.DriverTurnSettings
	err := g.callTurn(ctx, "settings", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result)
	return result, err
}

func (g serviceGateway) ReadContextState(ctx context.Context) (pluginprotocol.DriverContextState, error) {
	var result pluginprotocol.DriverContextState
	err := g.callTurn(ctx, "context_state", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result)
	return result, err
}

func (g serviceGateway) SaveContextState(ctx context.Context, state pluginprotocol.DriverContextState) error {
	var result struct{}
	return g.callTurn(ctx, "save_context_state", pluginprotocol.DriverTurnStateParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, State: state}, &result)
}

func (g serviceGateway) ReportTurn(ctx context.Context, observation pluginprotocol.DriverTurnObservation) error {
	var result struct{}
	return g.callTurn(ctx, "observe", pluginprotocol.DriverTurnObserveParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Observation: observation}, &result)
}

func (g serviceGateway) CompactContext(ctx context.Context, messages []providers.ChatMessage, budget pluginprotocol.DriverCompactBudget) ([]providers.ChatMessage, error) {
	var result pluginprotocol.DriverTurnCompactResult
	err := g.callTurn(ctx, "compact", pluginprotocol.DriverTurnCompactParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Messages: modelwire.DriverMessagesToWire(messages), Budget: budget}, &result)
	if err != nil {
		return nil, err
	}
	if result.Failure != nil {
		return modelwire.DriverMessagesFromWire(result.Messages), &FailureError{Failure: *result.Failure}
	}
	return modelwire.DriverMessagesFromWire(result.Messages), nil
}

func (g serviceGateway) AdmitInput(ctx context.Context) (InputAdmission, error) {
	var result pluginprotocol.DriverTurnAdmissionResult
	if err := g.callTurn(ctx, "admit_input", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result); err != nil {
		return InputAdmission{}, err
	}
	return InputAdmission{Messages: modelwire.DriverMessagesFromWire(result.Messages), Context: turnContextFromWire(result.Context), Decision: result.Decision}, nil
}

func (g serviceGateway) ReadRequestContext(ctx context.Context) ([]ContextSegment, error) {
	var result pluginprotocol.DriverTurnRequestContextResult
	if err := g.callTurn(ctx, "request_context", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result); err != nil {
		return nil, err
	}
	return ContextSegmentsFromWire(result.Segments), nil
}

func (g serviceGateway) TransformRequest(ctx context.Context, request providers.ChatRequest) (providers.ChatRequest, error) {
	var result pluginprotocol.DriverTurnReadResult
	if err := g.callTurn(ctx, "transform_request", pluginprotocol.DriverTurnModelParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Request: modelwire.DriverRequestToWire(request),
	}, &result); err != nil {
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

func (g serviceGateway) ArchiveContext(ctx context.Context) (ContextArchive, error) {
	var result pluginprotocol.DriverTurnArchiveResult
	if err := g.callTurn(ctx, "archive_context", pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, &result); err != nil {
		return ContextArchive{}, err
	}
	return ContextArchive{Messages: modelwire.DriverMessagesFromWire(result.Messages), Head: result.Head}, nil
}

func (g serviceGateway) ReplaceContext(ctx context.Context, replacement ContextReplacement) (TurnContext, error) {
	var result pluginprotocol.DriverTurnContextResult
	if err := g.callTurn(ctx, "replace_context", pluginprotocol.DriverTurnReplaceContextParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Revision: replacement.Revision,
		Messages: modelwire.DriverMessagesToWire(replacement.Messages), Volatile: replacement.Volatile,
	}, &result); err != nil {
		return TurnContext{}, err
	}
	return turnContextFromWire(result), nil
}

func turnContextFromWire(current pluginprotocol.DriverTurnContextResult) TurnContext {
	return TurnContext{Messages: modelwire.DriverMessagesFromWire(current.Messages), Revision: current.Revision, ArchiveHead: current.ArchiveHead, CanReplace: current.CanReplace}
}

func (g serviceGateway) CallModel(ctx context.Context, request providers.ChatRequest) (ModelRound, error) {
	return g.callModel(ctx, "model", request, "")
}

func (g serviceGateway) CallModelWithTools(ctx context.Context, request providers.ChatRequest, batchID string) (ModelRound, error) {
	return g.callModel(ctx, "model_with_tools", request, batchID)
}

func (g serviceGateway) callModel(ctx context.Context, method string, request providers.ChatRequest, batchID string) (ModelRound, error) {
	var result pluginprotocol.DriverTurnModelResult
	if err := g.callTurn(ctx, method, pluginprotocol.DriverTurnModelParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Request: modelwire.DriverRequestToWire(request), BatchID: batchID,
	}, &result); err != nil {
		return ModelRound{}, err
	}
	round := ModelRound{CompletedMessages: modelwire.DriverMessagesFromWire(result.CompletedMessages), Messages: modelwire.DriverMessagesFromWire(result.Messages), ToolBatchID: result.ToolBatchID, ToolWorkStarted: result.ToolWorkStarted}
	if result.Usage != nil {
		usage := modelwire.TokenUsageFromWire(*result.Usage)
		round.Usage = &usage
	}
	if result.Failure != nil {
		return round, &FailureError{Failure: *result.Failure}
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

func (g serviceGateway) CompleteTurn(ctx context.Context, completed TurnResult) (TurnReceipt, error) {
	var result pluginprotocol.DriverTurnCompleteResult
	if err := g.callTurn(ctx, "complete", pluginprotocol.DriverTurnCompleteParams{
		DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Content: completed.Content,
		Messages: modelwire.DriverMessagesToWire(completed.Messages), FinishReason: string(completed.FinishReason), StopReason: completed.StopReason, Truncated: completed.Truncated,
	}, &result); err != nil {
		return TurnReceipt{}, err
	}
	return TurnReceipt{ID: result.ReceiptID}, nil
}

func (g serviceGateway) EvaluatePolicy(ctx context.Context, input pluginprotocol.AgentPolicyInput) (pluginprotocol.AgentPolicyDecision, error) {
	var result pluginprotocol.AgentPolicyDecision
	err := g.callTurn(ctx, "evaluate_policy", pluginprotocol.DriverTurnPolicyParams{DriverTurnScope: pluginprotocol.DriverTurnScope{ExecutionID: g.executionID}, Input: input}, &result)
	return result, err
}
