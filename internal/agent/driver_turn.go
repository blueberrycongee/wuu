package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// streamTurnGateway owns execution resources, not the driver's algorithm. The
// gate also makes close wait for accepted calls before releasing their tools.
type streamTurnGateway struct {
	ctx             context.Context
	cancel          context.CancelFunc
	gate            chan struct{}
	runner          *StreamRunner
	input           loopdriver.PersistedInput
	baseHistory     []providers.ChatMessage
	onEvent         StreamCallback
	client          providers.StreamClient
	ready           bool
	closed          bool
	complete        bool
	legacy          bool
	frozen          ToolSurfaceFreezer
	step            int
	batches         map[string]bool
	produced        []providers.ChatMessage
	durable         []providers.ChatMessage
	archiveHead     int
	usage           providers.TokenUsage
	contextTokens   int
	postToolContext []ContextSegment
	completed       LoopResult
}

func newStreamTurnGateway(ctx context.Context, runner *StreamRunner, input loopdriver.PersistedInput, onEvent StreamCallback) *streamTurnGateway {
	ctx, cancel := context.WithCancel(ctx)
	gateway := &streamTurnGateway{ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1), runner: runner, input: input, onEvent: onEvent, batches: make(map[string]bool)}
	gateway.gate <- struct{}{}
	return gateway
}

func (g *streamTurnGateway) enter(ctx context.Context) (context.Context, func(), error) {
	ctx, leave, err := g.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	if g.complete || g.legacy {
		leave()
		return nil, nil, errors.New("driver turn is complete or uses the legacy loop gateway")
	}
	return ctx, leave, nil
}

func (g *streamTurnGateway) acquire(ctx context.Context) (context.Context, func(), error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-g.ctx.Done():
		return nil, nil, g.ctx.Err()
	case <-g.gate:
	}
	if err := g.ctx.Err(); err != nil {
		g.gate <- struct{}{}
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		g.gate <- struct{}{}
		return nil, nil, err
	}
	// Preserve caller cancellation and the execution's identity/context values.
	callCtx, cancel := context.WithCancel(g.ctx)
	stop := context.AfterFunc(ctx, cancel)
	return callCtx, func() { stop(); cancel(); g.gate <- struct{}{} }, nil
}

func (g *streamTurnGateway) prepare(ctx context.Context) error {
	if g.ready {
		return nil
	}
	recovered, err := g.runner.pendingToolResultMessages(ctx, g.input.Messages)
	if err != nil {
		return fmt.Errorf("recover pending tool results: %w", err)
	}
	g.baseHistory = filterDurableHistory(providers.CloneChatMessages(g.input.Messages))
	g.input.Messages = append(providers.CloneChatMessages(g.baseHistory), recovered...)
	g.appendMessages(recovered...)
	g.contextTokens = estimateMessages(g.input.Messages)
	g.client, _ = g.runner.providerConnectionSnapshot()
	if freezer, ok := g.runner.Tools.(ToolSurfaceFreezer); ok {
		freezer.FreezeToolSurface()
		g.frozen = freezer
	}
	g.ready = true
	return nil
}

func (g *streamTurnGateway) ReadTurn(ctx context.Context) (providers.ChatRequest, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return providers.ChatRequest{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return providers.ChatRequest{}, err
	}
	r := g.runner
	model := strings.TrimSpace(r.APIModel)
	if model == "" {
		model = r.Model
	}
	return providers.ChatRequest{
		Provider: r.ProviderName, Model: model,
		Messages: providers.CloneChatMessages(g.input.Messages), Tools: toolDefinitions(r.Tools),
		Temperature: r.Temperature, Effort: r.Effort,
		ProviderOptions:             providers.CloneRequestObject(r.ProviderOptions),
		MediaInput:                  providers.ResolveMediaInput(g.client, r.MediaInput, r.ProviderOptions),
		NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
		ForceToolName:               r.ForceToolFirstStep,
		CacheHint:                   &providers.CacheHint{PromptCacheKey: r.PromptCacheKey},
	}, nil
}

func (g *streamTurnGateway) CallModel(ctx context.Context, request providers.ChatRequest) (loopdriver.ModelRound, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.ModelRound{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.ModelRound{}, err
	}
	if g.client == nil {
		return loopdriver.ModelRound{}, errors.New("model client is unavailable")
	}
	if strings.TrimSpace(request.Model) == "" {
		return loopdriver.ModelRound{}, errors.New("model is required")
	}
	if request.Provider != g.runner.ProviderName {
		return loopdriver.ModelRound{}, errors.New("model request must use the turn's bound provider")
	}
	request.Messages = providers.CloneChatMessages(request.Messages)
	request.ProviderOptions = providers.CloneRequestObject(request.ProviderOptions)
	request.Operation = providers.InferenceOperation{}
	request.Execution = nil
	request.Attempt = providers.InferenceAttempt{}
	request.StepIndex = g.step
	g.step++
	round := loopdriver.ModelRound{}
	appendInput := func(messages []providers.ChatMessage) {
		messages = filterDurableHistory(messages)
		request.Messages = append(request.Messages, messages...)
		round.Messages = append(round.Messages, messages...)
		g.appendMessages(messages...)
	}
	if g.runner.BeforeStep != nil {
		appendInput(g.runner.BeforeStep())
	}
	if preparer, ok := g.runner.Tools.(ModelStepPreparer); ok {
		messages, err := preparer.PrepareModelStep(ctx, providers.CloneChatMessages(request.Messages))
		if err != nil {
			return round, err
		}
		appendInput(messages)
	}
	if g.runner.BeforeModelStep != nil {
		messages, err := g.runner.BeforeModelStep(ctx, request.StepIndex, providers.CloneChatMessages(request.Messages))
		if err != nil {
			return round, err
		}
		appendInput(messages)
	}
	// Accepted user input must be durable before a model can act on it.
	for _, message := range round.Messages {
		if isUndispatchedSteeringMessage(message) && g.runner.ArchiveHistory != nil {
			archived, err := g.runner.ArchiveHistory(ctx, providers.CloneChatMessages(g.durable))
			if err != nil {
				g.produced = slices.DeleteFunc(g.produced, isUndispatchedSteeringMessage)
				g.durable = slices.DeleteFunc(g.durable, isUndispatchedSteeringMessage)
				round.Messages = slices.DeleteFunc(round.Messages, isUndispatchedSteeringMessage)
				return round, err
			}
			g.archiveHead = archived.HeadSeq
			g.produced = applyArchivedHistorySeqs(g.produced, g.durable, archived.Seqs)
			request.Messages = applyArchivedHistorySeqs(request.Messages, g.durable, archived.Seqs)
			round.Messages = applyArchivedHistorySeqs(round.Messages, g.durable, archived.Seqs)
			g.durable = nil
			break
		}
	}
	segments := append(requestContextSegments(g.runner.BeforeRequestContext), g.postToolContext...)
	request.Messages = assembleModelRequest(request.Messages, segments).Messages
	if g.runner.BeforeRequest != nil {
		if err := g.runner.BeforeRequest(ctx, &request); err != nil {
			return round, err
		}
	}
	if err := validateTransformedRequest(request, "", false); err != nil {
		return round, err
	}
	ctx = providers.WithInferenceJournal(ctx, g.runner.InferenceJournal)
	// No executor is attached here: only an explicit CallTools can cause effects.
	step := &streamStep{client: g.client, onEvent: g.onEvent}
	g.contextTokens = estimateOutboundRequestTokens(request)
	result, err := step.Execute(ctx, request)
	if result.Usage != nil {
		g.usage.InputTokens += result.Usage.InputTokens
		g.usage.OutputTokens += result.Usage.OutputTokens
		g.usage.CacheCreationTokens += result.Usage.CacheCreationTokens
		g.usage.CacheReadTokens += result.Usage.CacheReadTokens
		if g.runner.OnUsage != nil {
			g.runner.OnUsage(result.Usage.InputTokens, result.Usage.OutputTokens)
		}
		if g.runner.OnTokenUsage != nil {
			g.runner.OnTokenUsage(*result.Usage)
		}
	}
	round.Usage = result.Usage
	assistant := assistantMessageFromStep(result)
	if result.Usage != nil {
		g.contextTokens = result.Usage.TotalContextTokens()
	} else {
		g.contextTokens += estimateMessages([]providers.ChatMessage{assistant})
	}
	if shouldPersistAssistantMessage(assistant) {
		g.appendMessages(assistant)
		round.Messages = append(round.Messages, assistant)
	}
	if err == nil {
		err = providers.ValidateAssistantToolCalls(assistant.ToolCalls)
	}
	return round, err
}

func (g *streamTurnGateway) CallTools(ctx context.Context, batch loopdriver.ToolBatch) ([]providers.ChatMessage, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(batch.ID) == "" {
		return nil, errors.New("tool batch id is required")
	}
	if g.batches[batch.ID] {
		return nil, fmt.Errorf("tool batch %q was already submitted; resolve its effects before retrying", batch.ID)
	}
	if g.runner.Tools == nil {
		return nil, errors.New("tool executor is unavailable")
	}
	if err := providers.ValidateAssistantToolCalls(batch.Calls); err != nil {
		return nil, err
	}
	g.batches[batch.ID] = true
	ctx = ContextWithHistory(ctx, providers.CloneChatMessages(batch.Messages))
	if g.runner.ToolWaitInterrupt != nil {
		ctx = toolctx.WithWaitInterrupt(ctx, g.runner.ToolWaitInterrupt())
	}
	execution := loopdriver.ExecutionContextFromContext(g.ctx)
	runtime := NewTurnToolRuntime(ToolRuntimeConfig{Executor: g.runner.Tools, Ledger: g.runner.ToolLedger, OperationID: execution.ExecutionID + "/" + batch.ID, StepIndex: g.step, RunContext: ctx})
	defer func() {
		runtime.Cancel()
		// Cancellation may return before an executor has settled its result.
		// Keep the tool surface and its providers alive through that cleanup.
		runtime.mu.Lock()
		runs := append([]*toolRun(nil), runtime.runs...)
		runtime.mu.Unlock()
		for _, run := range runs {
			run.mu.Lock()
			started := run.state != toolRunQueued
			run.mu.Unlock()
			if started {
				<-run.done
			}
		}
	}()
	runtime.SetResultCallback(func(call providers.ToolCall, result toolresult.Result) {
		if g.onEvent != nil {
			call = enrichToolCallDisplay(g.runner.Tools, call)
			g.onEvent(providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call, ToolResult: truncateLog(result.TextProjection(), 2000), ToolResultDetail: resultPointer(result)})
		}
	})
	messages, err := runtime.ExecuteFinalCalls(ctx, batch.Calls, nil, g.runner.OnToolBatchRejected)
	g.postToolContext = append(g.postToolContext, runtime.TakeRequestContextSegments()...)
	g.appendMessages(messages...)
	g.contextTokens += estimateMessages(messages)
	return providers.CloneChatMessages(messages), err
}

func (g *streamTurnGateway) CompleteTurn(ctx context.Context, result loopdriver.TurnResult) (loopdriver.ModelLoopReceipt, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.ModelLoopReceipt{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.ModelLoopReceipt{}, err
	}
	messages := filterDurableHistory(providers.CloneChatMessages(result.Messages))
	transcript := append(providers.CloneChatMessages(g.baseHistory), g.produced...)
	transcript = append(transcript, messages...)
	if err := providers.ValidateToolCallHistory(transcript); err != nil {
		return loopdriver.ModelLoopReceipt{}, err
	}
	g.appendMessages(messages...)
	g.completed = g.snapshot()
	g.completed.Content = result.Content
	g.complete = true
	if g.runner.AfterTurn != nil {
		g.runner.AfterTurn(ctx, g.runner, transcript, g.completed)
	}
	return loopdriver.ModelLoopReceipt{ID: "driver-turn"}, nil
}

func (g *streamTurnGateway) appendMessages(messages ...providers.ChatMessage) {
	g.produced = append(g.produced, providers.CloneChatMessages(messages)...)
	g.durable = append(g.durable, providers.CloneChatMessages(messages)...)
	for _, message := range messages {
		if g.onEvent != nil && !message.Hidden && !isNonDurableHistoryMessage(message) && !isInternalContextHistoryMessage(message) {
			g.onEvent(providers.StreamEvent{Type: providers.EventMessage, Message: &message})
		}
	}
}

func (g *streamTurnGateway) snapshot() LoopResult {
	result := LoopResult{
		NewMessages:            providers.CloneChatMessages(g.produced),
		DurableNewMessages:     providers.CloneChatMessages(g.durable),
		DurableMessagesTracked: true,
		HistoryArchiveHeadSeq:  g.archiveHead,
		InputTokens:            g.usage.InputTokens, OutputTokens: g.usage.OutputTokens,
		CacheCreationTokens: g.usage.CacheCreationTokens, CacheReadTokens: g.usage.CacheReadTokens,
		ContextTokens: g.contextTokens,
	}
	for i := len(g.produced) - 1; i >= 0; i-- {
		message := g.produced[i]
		if message.Role == "assistant" {
			result.Content, result.FinishReason, result.StopReason, result.Truncated = message.Content, message.FinishReason, message.StopReason, message.Truncated
			break
		}
	}
	return result
}

func (g *streamTurnGateway) result(id string) (LoopResult, bool) {
	<-g.gate
	defer func() { g.gate <- struct{}{} }()
	if g.complete && id == "driver-turn" {
		return g.completed, true
	}
	if id == "" && g.ready {
		return g.snapshot(), true
	}
	return LoopResult{}, false
}

func (g *streamTurnGateway) close() {
	g.cancel()
	<-g.gate
	defer func() { g.gate <- struct{}{} }()
	if !g.closed {
		g.closed = true
		if g.frozen != nil {
			g.frozen.UnfreezeToolSurface()
		}
	}
}

func assistantMessageFromStep(result StepResult) providers.ChatMessage {
	finish := result.FinishReason
	if finish == "" {
		finish = providers.NormalizeFinishReason(result.StopReason, result.Truncated, len(result.ToolCalls) > 0)
	}
	return providers.ChatMessage{Role: "assistant", Content: result.Content, Images: result.Images, ProviderItems: result.ProviderItems, Phase: result.Phase, ProviderItemID: result.ProviderItemID, ProviderItemProvider: result.ProviderItemProvider, ProviderItemModel: result.ProviderItemModel, ReasoningContent: result.ReasoningContent, ReasoningBlocks: cloneReasoningBlocks(result.ReasoningBlocks), ToolCalls: result.ToolCalls, FinishReason: finish, StopReason: result.StopReason, Truncated: result.Truncated}
}
