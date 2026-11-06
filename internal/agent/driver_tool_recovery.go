package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
)

func (g *streamTurnGateway) toolRecoveryScope() string {
	if g.descriptor.ID == "" || g.descriptor.Version == "" || !slices.Contains(g.descriptor.Capabilities, "tool_recovery") {
		return ""
	}
	return g.descriptor.ID + "@" + g.descriptor.Version
}

// RecoverTools reattaches an admitted batch under the resumed host execution.
// The caller's JSON is only a lookup and equality claim, never effect authority.
func (g *streamTurnGateway) RecoverTools(ctx context.Context, recovery loopdriver.ToolRecovery) ([]providers.ChatMessage, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer leave()
	if !g.resumed || g.toolRecoveryScope() == "" || g.runner.ToolLedger == nil || g.runner.DriverCheckpointStore == nil || recovery.OriginalExecutionID == "" || recovery.BatchID == "" {
		return nil, toolerrors.Unknown(errors.New("exact tool recovery requires a resumed durable execution and original admission"))
	}
	if err := g.prepare(ctx); err != nil {
		return nil, err
	}
	if len(g.streamedBatches) != 0 {
		return nil, errors.New("join streaming tools before recovery")
	}
	if g.runner.Tools == nil {
		return nil, errors.New("tool executor is unavailable")
	}
	if len(recovery.Calls) == 0 || len(recovery.Calls) > toolbatch.MaxCalls {
		return nil, errors.New("invalid recovery call count")
	}
	if err := providers.ValidateAssistantToolCalls(recovery.Calls); err != nil {
		return nil, err
	}
	if err := g.runner.ToolLedger.ValidateContinuation(ctx); err != nil {
		return nil, toolerrors.Unknown(err)
	}
	policies := make([]toolledger.ReplayPolicy, len(recovery.Calls))
	for i, call := range recovery.Calls {
		policies[i] = toolReplayPolicy(g.runner.Tools, call)
	}
	admitted, err := g.runner.ToolLedger.RecoverBatch(ctx, recovery.OriginalExecutionID+"/"+recovery.BatchID, g.toolRecoveryScope(), recovery.Calls, policies)
	if err != nil {
		return nil, toolerrors.Unknown(fmt.Errorf("exact tool recovery: %w", err))
	}
	// Projection drops ledger payloads only after archiving. Reuse that precise
	// invocation's host history, not any result sharing a provider-level call ID.
	history := append(providers.CloneChatMessages(g.baseHistory), g.produced...)
	existing := make(map[string]providers.ChatMessage)
	for _, message := range history {
		if message.Role == "tool" && message.ToolInvocationID != "" {
			existing[message.ToolInvocationID] = message
		}
	}
	allSettled := true
	for i, invocation := range admitted {
		settled := invocation.State == toolledger.InvocationSucceeded || invocation.State == toolledger.InvocationFailed
		allSettled = allSettled && settled
		if settled && invocation.Projected {
			message, found := existing[invocation.ID]
			if !found || message.ToolCallID != invocation.ProviderCallID || message.Name != invocation.ToolName {
				return nil, toolerrors.Unknown(errors.New("projected original tool receipt is unavailable"))
			}
			if message.ToolResult == nil {
				return nil, toolerrors.Unknown(errors.New("projected original tool result is unavailable"))
			}
			admitted[i].Result = message.ToolResult.Clone()
		}
	}
	g.batches[recovery.BatchID] = true
	for _, call := range recovery.Calls {
		g.toolCalls[call.ID] = true
	}
	ctx = ContextWithHistory(ctx, providers.CloneChatMessages(g.activeHistory))
	if g.runner.ToolWaitInterrupt != nil {
		ctx = toolctx.WithWaitInterrupt(ctx, g.runner.ToolWaitInterrupt())
	}
	var messages []providers.ChatMessage
	if allSettled {
		for i, invocation := range admitted {
			messages = append(messages, toolResultMessage(recovery.Calls[i], invocation.ID, invocation.Result, nil))
		}
		return messages, nil
	}
	config := ToolRuntimeConfig{Gate: g.toolGate, Serial: g.serialTools, Executor: g.runner.Tools, Ledger: g.runner.ToolLedger, OperationID: recovery.OriginalExecutionID + "/" + recovery.BatchID, PreparedInvocations: admitted, StepIndex: g.step, RunContext: ctx}
	var segments []ContextSegment
	if g.runner.SelectedToolBatch != nil {
		messages, segments, err = executeSelectedToolBatch(ctx, g.runner.SelectedToolBatch, toolbatch.Request{ID: recovery.BatchID, Calls: recovery.Calls, Serial: g.serialTools, MaxParallel: g.toolGate.capacity}, config, nil)
	} else {
		runtime := NewTurnToolRuntime(config)
		messages, err = runtime.ExecuteFinalCalls(ctx, recovery.Calls, nil, g.runner.OnToolBatchRejected)
		runtime.Close()
		segments = runtime.TakeRequestContextSegments()
	}
	g.postToolContext = append(g.postToolContext, segments...)
	for _, message := range messages {
		if _, found := existing[message.ToolInvocationID]; !found {
			g.appendMessages(message)
			g.contextTokens += estimateMessages([]providers.ChatMessage{message})
		}
	}
	return providers.CloneChatMessages(messages), err
}
