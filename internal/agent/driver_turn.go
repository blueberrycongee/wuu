package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// streamTurnGateway owns execution resources, not the driver's algorithm. The
// gate also makes close wait for accepted calls before releasing their tools.
type streamTurnGateway struct {
	ctx                     context.Context
	cancel                  context.CancelFunc
	gate                    chan struct{}
	runner                  *StreamRunner
	descriptor              loopdriver.Descriptor
	input                   loopdriver.PersistedInput
	baseHistory             []providers.ChatMessage
	activeHistory           []providers.ChatMessage
	revision                uint64
	historyRewritten        bool
	onEvent                 StreamCallback
	client                  providers.StreamClient
	policy                  *compactionservice.Policy
	ready                   bool
	closed                  bool
	complete                bool
	frozen                  ToolSurfaceFreezer
	step                    int
	batches                 map[string]bool
	streamedBatches         map[string]driverToolStream
	toolRuntimes            map[*TurnToolRuntime]bool
	toolCalls               map[string]bool
	produced                []providers.ChatMessage
	durable                 []providers.ChatMessage
	archiveHead             int
	usage                   providers.TokenUsage
	contextTokens           int
	turnRouting             providers.TurnRouting
	lastModelOperationID    string
	proposedContextParentID string
	proposedContextHash     string
	nextModelParentID       string
	postToolContext         []ContextSegment
	completed               LoopResult
}

func newStreamTurnGateway(ctx context.Context, runner *StreamRunner, input loopdriver.PersistedInput, onEvent StreamCallback) *streamTurnGateway {
	ctx, cancel := context.WithCancel(ctx)
	gateway := &streamTurnGateway{ctx: ctx, cancel: cancel, gate: make(chan struct{}, 1), runner: runner, input: input, onEvent: onEvent, batches: make(map[string]bool), toolCalls: make(map[string]bool)}
	gateway.gate <- struct{}{}
	return gateway
}

func (g *streamTurnGateway) enter(ctx context.Context) (context.Context, func(), error) {
	ctx, leave, err := g.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	if g.complete {
		leave()
		return nil, nil, errors.New("driver turn is complete")
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
	if g.runner.SelectedCompaction != nil {
		policy, err := g.runner.SelectedCompaction.Describe(ctx, g.runner.compactionDescription())
		if err != nil {
			return err
		}
		g.policy = &policy
	}
	recovered, err := g.runner.pendingToolResultMessages(ctx, g.input.Messages)
	if err != nil {
		return fmt.Errorf("recover pending tool results: %w", err)
	}
	g.baseHistory = filterDurableHistory(providers.CloneChatMessages(g.input.Messages))
	g.activeHistory = providers.CloneChatMessages(g.baseHistory)
	g.revision = 1
	g.appendMessages(recovered...)
	g.contextTokens = estimateMessages(g.activeHistory)
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
	definitions, err := toolDefinitions(ctx, r.Tools)
	if err != nil {
		return providers.ChatRequest{}, err
	}
	return providers.ChatRequest{
		Provider: r.ProviderName, Model: model,
		Messages: providers.CloneChatMessages(g.activeHistory), Tools: definitions,
		Temperature: r.Temperature, Effort: r.Effort,
		ProviderOptions:             providers.CloneRequestObject(r.ProviderOptions),
		MediaInput:                  providers.ResolveMediaInput(g.client, r.MediaInput, r.ProviderOptions),
		NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
		ForceToolName:               r.ForceToolFirstStep,
		CacheHint:                   &providers.CacheHint{PromptCacheKey: r.PromptCacheKey},
	}, nil
}

type driverToolStream struct {
	runtime *TurnToolRuntime
	calls   []providers.ToolCall
}

func (g *streamTurnGateway) CallModel(ctx context.Context, request providers.ChatRequest) (loopdriver.ModelRound, error) {
	return g.callModel(ctx, request, "")
}

func (g *streamTurnGateway) CallModelWithTools(ctx context.Context, request providers.ChatRequest, batchID string) (loopdriver.ModelRound, error) {
	if strings.TrimSpace(batchID) == "" {
		return loopdriver.ModelRound{}, errors.New("streaming tool batch id is required")
	}
	return g.callModel(ctx, request, batchID)
}

func (g *streamTurnGateway) callModel(ctx context.Context, request providers.ChatRequest, batchID string) (loopdriver.ModelRound, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.ModelRound{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.ModelRound{}, err
	}
	if len(g.streamedBatches) != 0 {
		return loopdriver.ModelRound{}, errors.New("join the pending streaming tool batch before calling another model")
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
	request = cloneDriverRequest(request)
	request.StepIndex = g.step
	round := loopdriver.ModelRound{}
	if err := validateTransformedRequest(request); err != nil {
		return round, err
	}
	if batchID != "" {
		if g.batches[batchID] {
			return round, fmt.Errorf("tool batch %q was already submitted; resolve its effects before retrying", batchID)
		}
		if g.runner.Tools == nil {
			return round, errors.New("tool executor is unavailable")
		}
	}
	ctx = providers.WithInferenceJournal(ctx, g.runner.InferenceJournal)
	kind, profile := g.runner.InferenceOperationKind, g.runner.InferenceWorkloadProfile
	if kind == "" {
		kind = providers.InferenceOperationAgentRound
	}
	if profile == "" {
		profile = providers.InferenceProfileInteractive
	}
	ctx, _ = providers.BeginInferenceOperationLineage(ctx, g.nextModelParentID)
	request, err = providers.EnsureInferenceExecutionContext(ctx, request, kind, profile)
	if err != nil {
		return round, err
	}
	if g.runner.ModelInputReceiptStore != nil {
		_, sections := g.runner.systemPromptSnapshot()
		receipt := modelInputReceipt(request, loopdriver.ExecutionContextFromContext(g.ctx), g.descriptor, sections, g.historyRewritten)
		if err := g.runner.ModelInputReceiptStore.SaveModelInputReceipt(ctx, receipt); err != nil {
			return round, fmt.Errorf("persist model input receipt: %w", err)
		}
	}
	// A canceled durable write can still have committed. Keep its receipt, but
	// never start model or tool work after cancellation crossed the barrier.
	if err := ctx.Err(); err != nil {
		return round, err
	}
	// Provider routing is execution state, shared by model rounds and owned by
	// this resource rather than serialized or supplied by a driver.
	request.TurnRouting = &g.turnRouting
	// Provider call IDs belong to one assistant declaration. A later model
	// operation may reuse a provider's ID; batch IDs still fence every effect.
	g.toolCalls = make(map[string]bool)
	step := &streamStep{client: g.client, onEvent: g.onEvent, tools: g.runner.Tools}
	observed := make(map[string]providers.ToolCall)
	var runtimes []*TurnToolRuntime
	if batchID != "" {
		g.batches[batchID] = true
		round.ToolBatchID = batchID
		step.tools, step.toolLedger, step.enableStreamingToolExec = g.runner.Tools, g.runner.ToolLedger, true
		// A successful model RPC ends before its streamed tools. Their lifetime
		// belongs to the execution, while model failures explicitly cancel them.
		step.toolRunContext = g.ctx
		if g.runner.ToolWaitInterrupt != nil {
			step.toolRunContext = toolctx.WithWaitInterrupt(step.toolRunContext, g.runner.ToolWaitInterrupt())
		}
		if g.toolRuntimes == nil {
			g.toolRuntimes = make(map[*TurnToolRuntime]bool)
		}
		step.ownToolRuntime = func(runtime *TurnToolRuntime) {
			runtimes = append(runtimes, runtime)
			g.toolRuntimes[runtime] = true
		}
		step.admitStreamToolCall = func(call providers.ToolCall) error {
			if g.toolCalls[call.ID] {
				return fmt.Errorf("tool call %q was already submitted; resolve its effects before retrying", call.ID)
			}
			if err := providers.ValidateAssistantToolCalls([]providers.ToolCall{call}); err != nil {
				return err
			}
			g.toolCalls[call.ID] = true
			observed[call.ID] = call
			return nil
		}
	}
	g.lastModelOperationID = request.Operation.ID
	g.nextModelParentID = ""
	g.step++
	g.contextTokens = estimateOutboundRequestTokens(request)
	result, err := step.Execute(ctx, request)
	if err == nil {
		err = providers.ValidateAssistantToolCalls(result.ToolCalls)
	}
	if batchID != "" {
		for _, runtime := range runtimes {
			round.ToolWorkStarted = round.ToolWorkStarted || runtime.HasStartedWork()
		}
		if err == nil {
			for _, call := range result.ToolCalls {
				original, streamed := observed[call.ID]
				if (streamed && !sameToolEffect(original, call)) || (!streamed && g.toolCalls[call.ID]) {
					err = fmt.Errorf("model reused tool call %q with a different effect", call.ID)
					break
				}
			}
		}
		if err == nil && len(result.ToolCalls) > 0 {
			finalIDs := make(map[string]bool, len(result.ToolCalls))
			for _, call := range result.ToolCalls {
				finalIDs[call.ID] = true
			}
			for id := range observed {
				if !finalIDs[id] {
					err = fmt.Errorf("streamed tool call %q is missing from the completed model response", id)
					break
				}
			}
		}
		if err != nil || len(result.ToolCalls) == 0 {
			g.releaseToolRuntimes(runtimes)
			if err == nil {
				round.ToolBatchID = ""
			}
		} else {
			for _, call := range result.ToolCalls {
				g.toolCalls[call.ID] = true
			}
			if g.streamedBatches == nil {
				g.streamedBatches = make(map[string]driverToolStream)
			}
			// streamStep creates local runtimes; the service retains their physical
			// cleanup handle while drivers receive only a batch receipt.
			g.streamedBatches[batchID] = driverToolStream{runtime: result.ToolRuntime.(*TurnToolRuntime), calls: append([]providers.ToolCall(nil), result.ToolCalls...)}
			var retired []*TurnToolRuntime
			for _, runtime := range runtimes {
				if runtime != result.ToolRuntime {
					retired = append(retired, runtime)
				}
			}
			g.releaseToolRuntimes(retired)
		}
	}
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
	// Even an empty completed response carries a finish signal. Return it to
	// the algorithm without adding an empty page to the durable transcript.
	if err == nil || shouldPersistAssistantMessage(assistant) {
		round.Messages = append(round.Messages, assistant)
	}
	if shouldPersistAssistantMessage(assistant) {
		if err == nil {
			g.appendMessages(assistant)
		} else if strings.TrimSpace(result.Content) != "" || len(result.Images) > 0 {
			// Keep visible partial output, but not incomplete provider state or
			// tool declarations. The reply still exposes those for reconciliation.
			g.appendMessages(providers.ChatMessage{Role: "assistant", Content: result.Content, Images: result.Images, Phase: result.Phase})
		}
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
	streamed, joining := g.streamedBatches[batch.ID]
	if len(g.streamedBatches) != 0 && !joining {
		return nil, errors.New("join the pending streaming tool batch before submitting another batch")
	}
	if g.batches[batch.ID] && !joining {
		return nil, fmt.Errorf("tool batch %q was already submitted; resolve its effects before retrying", batch.ID)
	}
	if joining {
		if len(batch.Calls) != len(streamed.calls) {
			return nil, errors.New("streaming tool batch must join all returned calls")
		}
		for i, call := range batch.Calls {
			if !sameToolEffect(call, streamed.calls[i]) {
				return nil, errors.New("streaming tool batch differs from its model receipt")
			}
		}
		batch.Calls = streamed.calls
	}
	if g.runner.Tools == nil {
		return nil, errors.New("tool executor is unavailable")
	}
	if len(batch.Calls) == 0 {
		return nil, errors.New("tool batch must contain at least one call")
	}
	if err := providers.ValidateAssistantToolCalls(batch.Calls); err != nil {
		return nil, err
	}
	for _, call := range batch.Calls {
		if g.toolCalls[call.ID] && !joining {
			return nil, fmt.Errorf("tool call %q was already submitted; resolve its effects before retrying", call.ID)
		}
	}
	transcript := append(providers.CloneChatMessages(g.baseHistory), g.produced...)
	declared, err := pendingToolCallDeclaration(transcript, batch.Calls)
	if err != nil {
		return nil, err
	}
	g.batches[batch.ID] = true
	delete(g.streamedBatches, batch.ID)
	for _, call := range batch.Calls {
		g.toolCalls[call.ID] = true
	}
	if !declared {
		// Drivers may choose tools directly or restore intent from a checkpoint
		// before the original model response reached the session transcript.
		g.appendMessages(providers.ChatMessage{Role: "assistant", ToolCalls: batch.Calls})
	}
	ctx = ContextWithHistory(ctx, providers.CloneChatMessages(batch.Messages))
	if g.runner.ToolWaitInterrupt != nil {
		ctx = toolctx.WithWaitInterrupt(ctx, g.runner.ToolWaitInterrupt())
	}
	execution := loopdriver.ExecutionContextFromContext(g.ctx)
	runtime := streamed.runtime
	if runtime == nil {
		runtime = NewTurnToolRuntime(ToolRuntimeConfig{Executor: g.runner.Tools, Ledger: g.runner.ToolLedger, OperationID: execution.ExecutionID + "/" + batch.ID, StepIndex: g.step, RunContext: ctx})
	}
	defer g.releaseToolRuntimes([]*TurnToolRuntime{runtime})
	runtime.SetResultCallback(func(call providers.ToolCall, result toolresult.Result) {
		if g.onEvent != nil {
			call = enrichToolCallDisplay(g.runner.Tools, call)
			g.onEvent(providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call, ToolResult: truncateLog(result.TextProjection(), 2000), ToolResultDetail: resultPointer(result)})
			if update, ok := todoUpdateEventFromToolResult(call, result.TextProjection()); ok {
				g.onEvent(providers.StreamEvent{Type: providers.EventTodoUpdate, TodoUpdate: update})
			}
		}
	})
	messages, err := runtime.ExecuteFinalCalls(ctx, batch.Calls, nil, g.runner.OnToolBatchRejected)
	g.postToolContext = append(g.postToolContext, runtime.TakeRequestContextSegments()...)
	g.appendMessages(messages...)
	g.contextTokens += estimateMessages(messages)
	return providers.CloneChatMessages(messages), err
}

func (g *streamTurnGateway) CompleteTurn(ctx context.Context, result loopdriver.TurnResult) (loopdriver.TurnReceipt, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return loopdriver.TurnReceipt{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return loopdriver.TurnReceipt{}, err
	}
	if len(g.streamedBatches) != 0 {
		return loopdriver.TurnReceipt{}, errors.New("join the pending streaming tool batch before completing the turn")
	}
	if err := validateDriverMessages(result.Messages); err != nil {
		return loopdriver.TurnReceipt{}, err
	}
	messages := filterDurableHistory(providers.CloneChatMessages(result.Messages))
	transcript := append(providers.CloneChatMessages(g.activeHistory), messages...)
	if err := providers.ValidateToolCallHistory(transcript); err != nil {
		return loopdriver.TurnReceipt{}, err
	}
	g.appendMessages(messages...)
	g.completed = g.snapshot()
	g.completed.Content = result.Content
	if result.FinishReason != "" || result.StopReason != "" || result.Truncated {
		g.completed.FinishReason, g.completed.StopReason, g.completed.Truncated = result.FinishReason, result.StopReason, result.Truncated
	}
	g.complete = true
	if g.runner.AfterTurn != nil {
		g.runner.AfterTurn(ctx, g.runner, providers.CloneChatMessages(g.activeHistory), g.completed)
	}
	return loopdriver.TurnReceipt{ID: "driver-turn"}, nil
}

func (g *streamTurnGateway) appendMessages(messages ...providers.ChatMessage) {
	if len(messages) == 0 {
		return
	}
	g.revision++
	g.activeHistory = append(g.activeHistory, providers.CloneChatMessages(messages)...)
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
	if g.historyRewritten {
		result.HistoryRewritten = true
		result.NewMessages = providers.CloneChatMessages(g.activeHistory)
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
		for runtime := range g.toolRuntimes {
			runtime.Cancel()
		}
		for runtime := range g.toolRuntimes {
			runtime.Close()
		}
		g.toolRuntimes = nil
		g.streamedBatches = nil
		if g.frozen != nil {
			g.frozen.UnfreezeToolSurface()
		}
	}
}

func (g *streamTurnGateway) releaseToolRuntimes(runtimes []*TurnToolRuntime) {
	for _, runtime := range runtimes {
		runtime.Cancel()
	}
	for _, runtime := range runtimes {
		runtime.Close()
		delete(g.toolRuntimes, runtime)
	}
}

// pendingToolCallDeclaration matches the trailing assistant/tool block. A batch
// can execute a subset of pending calls, but cannot reuse a settled call or give
// an existing call ID a different meaning.
func pendingToolCallDeclaration(history []providers.ChatMessage, calls []providers.ToolCall) (bool, error) {
	settled := make(map[string]bool)
	index := len(history) - 1
	for index >= 0 && history[index].Role == "tool" {
		settled[history[index].ToolCallID] = true
		index--
	}
	if index < 0 || history[index].Role != "assistant" {
		return false, nil
	}
	matched := 0
	for _, call := range calls {
		for _, original := range history[index].ToolCalls {
			if original.ID != call.ID {
				continue
			}
			if settled[call.ID] {
				return false, fmt.Errorf("tool call %q already has a result", call.ID)
			}
			if original.Name != call.Name || original.Arguments != call.Arguments || original.Kind != call.Kind {
				return false, fmt.Errorf("tool call %q differs from its recorded declaration", call.ID)
			}
			matched++
			break
		}
	}
	if matched > 0 && matched != len(calls) {
		return false, errors.New("tool batch mixes declared and undeclared calls; submit them separately")
	}
	return matched == len(calls), nil
}

func sameToolEffect(a, b providers.ToolCall) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Arguments == b.Arguments && a.Kind == b.Kind
}

func assistantMessageFromStep(result StepResult) providers.ChatMessage {
	finish := result.FinishReason
	if finish == "" {
		finish = providers.NormalizeFinishReason(result.StopReason, result.Truncated, len(result.ToolCalls) > 0)
	}
	return providers.ChatMessage{Role: "assistant", Content: result.Content, Images: result.Images, ProviderItems: result.ProviderItems, Phase: result.Phase, ProviderItemID: result.ProviderItemID, ProviderItemProvider: result.ProviderItemProvider, ProviderItemModel: result.ProviderItemModel, ReasoningContent: result.ReasoningContent, ReasoningBlocks: cloneReasoningBlocks(result.ReasoningBlocks), ToolCalls: result.ToolCalls, FinishReason: finish, StopReason: result.StopReason, Truncated: result.Truncated}
}
