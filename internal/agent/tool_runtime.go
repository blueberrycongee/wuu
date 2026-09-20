package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type toolRunState int

const (
	toolRunQueued toolRunState = iota
	toolRunRunning
	toolRunDone
)

type toolRun struct {
	call            providers.ToolCall
	order           int
	finalized       bool
	concurrencySafe bool
	streamSafe      bool
	streamStarted   bool
	invocationID    string
	parent          *toolRun
	depth           int

	mu     sync.Mutex
	state  toolRunState
	done   chan struct{}
	cancel context.CancelFunc
	result toolresult.Result
	err    error
}

// TurnToolRuntime owns tool executions for one model turn. Streaming can
// enqueue read-only tools early, and the loop later waits for those same runs
// while executing any remaining final tool calls.
type TurnToolRuntime struct {
	executor    ToolExecutor
	ledger      *toolledger.Ledger
	operationID string
	runContext  context.Context
	gate        *ToolExecutionGate

	mu       sync.Mutex
	runs     []*toolRun
	byID     map[string]*toolRun
	canceled bool

	requestContext []ContextSegment
	stepIndex      *int
	batchID        string
	ledgerErr      error
	onResultDetail func(providers.ToolCall, toolresult.Result)
}

type ToolRuntimeConfig struct {
	// Gate must be shared when multiple runtimes can have live nested work.
	Gate        *ToolExecutionGate
	Executor    ToolExecutor
	Ledger      *toolledger.Ledger
	OperationID string
	StepIndex   int
	// RunContext is the turn-scoped base context for tool execution. Streaming
	// observation happens under the stream attempt's context, which is canceled
	// as soon as the stream closes — even on success — while a stream-started
	// tool must keep running into the final batch. When set, tool executions
	// derive from RunContext and stop only through explicit Cancel or turn
	// cancellation; when nil, executions derive from the observation context.
	RunContext context.Context
}

func (r *TurnToolRuntime) SetResultCallback(callback func(providers.ToolCall, toolresult.Result)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.onResultDetail = callback
	r.mu.Unlock()
}

func NewTurnToolRuntime(config ToolRuntimeConfig) *TurnToolRuntime {
	runtime := &TurnToolRuntime{
		executor:    config.Executor,
		ledger:      config.Ledger,
		operationID: strings.TrimSpace(config.OperationID),
		runContext:  config.RunContext,
		gate:        config.Gate,
		byID:        map[string]*toolRun{},
	}
	if runtime.gate == nil {
		runtime.gate = NewToolExecutionGate(maxToolConcurrency)
	}
	runtime.SetStepIndex(config.StepIndex)
	return runtime
}

func (r *TurnToolRuntime) SetStepIndex(stepIndex int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	value := stepIndex
	r.stepIndex = &value
}

// ObserveStreamEvent records streamed tool blocks and starts safe prefix tools
// as soon as their arguments are complete.
func (r *TurnToolRuntime) ObserveStreamEvent(ctx context.Context, event providers.StreamEvent) error {
	if r == nil || r.executor == nil {
		return nil
	}
	switch event.Type {
	case providers.EventToolUseStart:
		if event.ToolCall == nil || strings.TrimSpace(event.ToolCall.ID) == "" {
			return nil
		}
		r.addStreamToolStart(event.ToolCall)
	case providers.EventToolUseDelta:
		r.appendStreamToolDelta(event.Content)
	case providers.EventToolUseEnd:
		if event.ToolCall == nil || strings.TrimSpace(event.ToolCall.ID) == "" {
			return nil
		}
		r.finalizeStreamTool(ctx, event.ToolCall)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ledgerErr
}

// Cancel stops any in-flight streaming-started work and prevents additional
// stream-prefix starts for this runtime.
func (r *TurnToolRuntime) Cancel() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.canceled = true
	runs := append([]*toolRun(nil), r.runs...)
	r.mu.Unlock()

	for _, run := range runs {
		run.mu.Lock()
		cancel := run.cancel
		run.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
}

func (r *TurnToolRuntime) ReplayDecision(ctx context.Context) (toolledger.ReplayDecision, error) {
	if r == nil || r.ledger == nil {
		return toolledger.ReplayDecision{Action: toolledger.ReplayAllow, Reason: toolledger.ReplayReasonNoInvocation}, nil
	}
	r.mu.Lock()
	batchID := r.batchID
	r.mu.Unlock()
	return r.ledger.DecideReplay(ctx, batchID)
}

func (r *TurnToolRuntime) addStreamToolStart(call *providers.ToolCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.canceled {
		return
	}
	if existing := r.byID[call.ID]; existing != nil {
		existing.call.ProviderItemID = call.ProviderItemID
		existing.call.ProviderItemProvider = call.ProviderItemProvider
		existing.call.ProviderItemModel = call.ProviderItemModel
		existing.call.Name = call.Name
		existing.call.Kind = call.Kind
		existing.concurrencySafe = toolCanRunConcurrently(r.executor, existing.call)
		existing.streamSafe = toolCanStartDuringStreaming(r.executor, existing.call)
		return
	}
	run := &toolRun{
		call: providers.ToolCall{
			ID:                   call.ID,
			ProviderItemID:       call.ProviderItemID,
			ProviderItemProvider: call.ProviderItemProvider,
			ProviderItemModel:    call.ProviderItemModel,
			Name:                 call.Name,
			Kind:                 call.Kind,
		},
		order: len(r.runs),
		done:  make(chan struct{}),
	}
	run.concurrencySafe = toolCanRunConcurrently(r.executor, run.call)
	run.streamSafe = toolCanStartDuringStreaming(r.executor, run.call)
	r.runs = append(r.runs, run)
	r.byID[call.ID] = run
}

func (r *TurnToolRuntime) appendStreamToolDelta(delta string) {
	if delta == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.canceled || len(r.runs) == 0 {
		return
	}
	r.runs[len(r.runs)-1].call.Arguments += delta
}

func (r *TurnToolRuntime) finalizeStreamTool(ctx context.Context, call *providers.ToolCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.canceled {
		return
	}
	run := r.byID[call.ID]
	if run == nil {
		run = &toolRun{
			call: providers.ToolCall{
				ID:                   call.ID,
				ProviderItemID:       call.ProviderItemID,
				ProviderItemProvider: call.ProviderItemProvider,
				ProviderItemModel:    call.ProviderItemModel,
			},
			order: len(r.runs),
			done:  make(chan struct{}),
		}
		r.runs = append(r.runs, run)
		r.byID[call.ID] = run
	}
	if call.ProviderItemID != "" {
		run.call.ProviderItemID = call.ProviderItemID
		run.call.ProviderItemProvider = call.ProviderItemProvider
		run.call.ProviderItemModel = call.ProviderItemModel
	}
	run.call.Name = call.Name
	run.call.Kind = call.Kind
	if call.Arguments != "" {
		run.call.Arguments = call.Arguments
	}
	run.finalized = run.call.Arguments != ""
	run.concurrencySafe = toolCanRunConcurrently(r.executor, run.call)
	run.streamSafe = toolCanStartDuringStreaming(r.executor, run.call)
	r.startReadyStreamPrefixLocked(ctx)
}

func (r *TurnToolRuntime) startReadyStreamPrefixLocked(ctx context.Context) {
	for _, run := range r.runs {
		if !run.finalized {
			return
		}
		if !run.streamSafe {
			return
		}
		r.startRunLocked(ctx, run, true)
	}
}

func (r *TurnToolRuntime) startRunLocked(ctx context.Context, run *toolRun, streamStarted bool) {
	run.mu.Lock()
	if run.state != toolRunQueued {
		run.mu.Unlock()
		return
	}
	run.streamStarted = run.streamStarted || streamStarted
	if err := r.prepareRunLocked(ctx, run); err != nil {
		run.state = toolRunRunning
		run.mu.Unlock()
		run.complete(toolresult.Result{}, err)
		return
	}
	if r.ledger != nil {
		if err := r.ledger.Start(ctx, run.invocationID); err != nil {
			r.ledgerErr = err
			run.state = toolRunRunning
			run.mu.Unlock()
			run.complete(toolresult.Result{}, err)
			return
		}
	}
	run.state = toolRunRunning
	base := ctx
	if streamStarted && r.runContext != nil {
		base = r.runContext
	}
	runCtx, cancel := context.WithCancel(base)
	if r.stepIndex != nil {
		runCtx = toolctx.WithStepIndex(runCtx, *r.stepIndex)
	}
	run.cancel = cancel
	call := run.call
	// Final-call registration can refresh metadata while this streamed run is
	// already starting. Use the scheduling decision made under the runtime lock.
	concurrencySafe := run.concurrencySafe
	run.mu.Unlock()

	go func() {
		finish := func(result toolresult.Result, executionErr error) {
			defer cancel()
			if executionErr != nil {
				result = toolresult.FromErrorText(errorJSON(executionErr))
			}
			if finalizer, ok := r.executor.(ToolResultFinalizer); ok {
				result = finalizer.FinalizeToolResult(call, result)
			}
			if r.ledger != nil {
				if settleErr := r.ledger.Settle(context.WithoutCancel(runCtx), run.invocationID, result); settleErr != nil {
					run.complete(toolresult.Result{}, fmt.Errorf("settle tool invocation: %w", settleErr))
					return
				}
			}
			run.complete(result, nil)
		}
		select {
		case <-runCtx.Done():
			finish(toolresult.Result{}, runCtx.Err())
			return
		default:
		}
		if toolIsOrchestrator(r.executor, call) {
			runCtx = toolctx.WithNestedExecutor(runCtx, &nestedToolScope{runtime: r, parent: run, ctx: runCtx, calls: make(map[string]*toolRun)})
		} else {
			runCtx = toolctx.WithNestedExecutor(runCtx, nil)
			release, err := r.gate.acquire(runCtx, concurrencySafe)
			if err != nil {
				finish(toolresult.Result{}, err)
				return
			}
			defer release()
		}
		select {
		case <-runCtx.Done():
			finish(toolresult.Result{}, runCtx.Err())
			return
		default:
		}
		result, err := executeToolResult(runCtx, r.executor, call)
		finish(result, err)
		if run.parent != nil {
			settled, settleErr := run.wait(context.Background())
			if settleErr == nil {
				r.notifyResult(call, settled, nil)
			}
		}
	}()
}

func (r *TurnToolRuntime) prepareRunLocked(ctx context.Context, run *toolRun) error {
	if r.ledger == nil || run.invocationID != "" {
		return nil
	}
	if r.ledgerErr != nil {
		return r.ledgerErr
	}
	// Nested calls get separate batches: the parent model batch may already
	// be finalized when JavaScript or another orchestrator submits a child.
	if run.parent != nil {
		batchID, err := r.ledger.BeginBatch(ctx, r.operationID, r.currentStepIndexLocked())
		if err != nil {
			return err
		}
		invocation, err := r.ledger.PrepareNested(ctx, batchID, run.parent.invocationID, run.call, toolledger.ReplayAtMostOnce)
		if err != nil {
			return err
		}
		run.invocationID = invocation.ID
		return r.ledger.FinalizeBatch(ctx, batchID)
	}
	if r.batchID == "" {
		batchID, err := r.ledger.BeginBatch(ctx, r.operationID, r.currentStepIndexLocked())
		if err != nil {
			r.ledgerErr = err
			return err
		}
		r.batchID = batchID
	}
	invocation, err := r.ledger.Prepare(ctx, r.batchID, run.call, toolledger.ReplayAtMostOnce)
	if err != nil {
		r.ledgerErr = err
		return err
	}
	run.invocationID = invocation.ID
	return nil
}

func (run *toolRun) complete(result toolresult.Result, err error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.state == toolRunDone {
		return
	}
	run.result = result
	run.err = err
	run.state = toolRunDone
	close(run.done)
}

func (run *toolRun) wait(ctx context.Context) (toolresult.Result, error) {
	select {
	case <-ctx.Done():
		return toolresult.Result{}, ctx.Err()
	case <-run.done:
		run.mu.Lock()
		defer run.mu.Unlock()
		return run.result, run.err
	}
}

// ExecuteFinalCalls returns tool messages in the model-requested order. Runs
// that were already started during streaming are awaited; all other calls are
// executed through the same runtime.
func (r *TurnToolRuntime) ExecuteFinalCalls(
	ctx context.Context,
	calls []providers.ToolCall,
	onResult func(providers.ToolCall, string),
	onRejected ...func(ToolBatchRejectionInfo),
) ([]providers.ChatMessage, error) {
	if r == nil {
		r = NewTurnToolRuntime(ToolRuntimeConfig{})
	}
	if msgs, rejected := r.rejectBarrierToolBatch(calls, onResult, firstToolBatchRejectionCallback(onRejected)); rejected {
		return msgs, nil
	}
	if err := r.registerFinalCalls(ctx, calls); err != nil {
		return nil, fmt.Errorf("prepare tool batch: %w", err)
	}
	batches := partitionToolCalls(r.executor, calls)
	var toolMessages []providers.ChatMessage
	for _, batch := range batches {
		batchResult := r.executeBatch(ctx, batch, onResult)
		if batchResult.err != nil {
			return nil, batchResult.err
		}
		toolMessages = append(toolMessages, batchResult.messages...)
		r.requestContext = append(r.requestContext, batchResult.requestContext...)
	}
	return toolMessages, nil
}

func (r *TurnToolRuntime) rejectBarrierToolBatch(
	calls []providers.ToolCall,
	onResult func(providers.ToolCall, string),
	onRejected func(ToolBatchRejectionInfo),
) ([]providers.ChatMessage, bool) {
	if len(calls) <= 1 {
		return nil, false
	}
	barrierName := ""
	for _, call := range calls {
		if isBarrierTool(call.Name) {
			barrierName = strings.TrimSpace(call.Name)
			break
		}
	}
	if barrierName == "" {
		return nil, false
	}
	r.Cancel()
	if onRejected != nil {
		onRejected(ToolBatchRejectionInfo{
			StepIndex:     r.currentStepIndex(),
			BarrierTool:   barrierName,
			SiblingTools:  siblingToolNames(calls, barrierName),
			ToolCallCount: len(calls),
		})
	}
	msgs := make([]providers.ChatMessage, 0, len(calls))
	for _, call := range calls {
		result := toolresult.FromErrorText(barrierToolBatchRejectionResult(call, barrierName))
		r.notifyResult(call, result, onResult)
		msgs = append(msgs, toolResultMessage(call, "", result, nil, false))
	}
	return msgs, true
}

func firstToolBatchRejectionCallback(callbacks []func(ToolBatchRejectionInfo)) func(ToolBatchRejectionInfo) {
	for _, callback := range callbacks {
		if callback != nil {
			return callback
		}
	}
	return nil
}

func (r *TurnToolRuntime) currentStepIndex() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stepIndex == nil {
		return 0
	}
	return *r.stepIndex
}

func (r *TurnToolRuntime) currentStepIndexLocked() int {
	if r.stepIndex == nil {
		return 0
	}
	return *r.stepIndex
}

func siblingToolNames(calls []providers.ToolCall, barrierName string) []string {
	out := make([]string, 0, len(calls)-1)
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" || strings.EqualFold(name, barrierName) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func barrierToolBatchRejectionResult(call providers.ToolCall, barrierName string) string {
	if isBarrierTool(call.Name) {
		name := strings.TrimSpace(call.Name)
		return errorJSON(errors.New(name + " must be called alone; re-issue it as the only tool call after reviewing sibling results"))
	}
	return errorJSON(errors.New("not executed because a barrier tool was called in the same assistant message: " + strings.TrimSpace(barrierName)))
}

func isBarrierTool(name string) bool {
	return false
}

// TakeRequestContextSegments returns request-only context produced by tool
// execution and clears it from this turn runtime.
func (r *TurnToolRuntime) TakeRequestContextSegments() []ContextSegment {
	if r == nil || len(r.requestContext) == 0 {
		return nil
	}
	out := append([]ContextSegment(nil), r.requestContext...)
	r.requestContext = nil
	return out
}

func (r *TurnToolRuntime) registerFinalCalls(ctx context.Context, calls []providers.ToolCall) error {
	r.mu.Lock()
	if r.byID == nil {
		r.byID = map[string]*toolRun{}
	}
	finalIDs := map[string]bool{}
	for _, call := range calls {
		if call.ID != "" {
			finalIDs[call.ID] = true
		}
	}
	var orphanCancels []context.CancelFunc
	for id, run := range r.byID {
		if finalIDs[id] {
			continue
		}
		delete(r.byID, id)
		run.mu.Lock()
		if run.streamStarted && run.cancel != nil {
			orphanCancels = append(orphanCancels, run.cancel)
		}
		run.mu.Unlock()
	}
	ordered := make([]*toolRun, 0, len(calls))
	seen := map[string]bool{}
	for i, call := range calls {
		var run *toolRun
		if call.ID != "" && !seen[call.ID] {
			run = r.byID[call.ID]
			seen[call.ID] = true
		}
		if run == nil {
			run = &toolRun{done: make(chan struct{})}
		}
		run.call = call
		run.order = i
		run.finalized = true
		run.concurrencySafe = toolCanRunConcurrently(r.executor, call)
		run.streamSafe = toolCanStartDuringStreaming(r.executor, call)
		if err := r.prepareRunLocked(ctx, run); err != nil {
			r.mu.Unlock()
			for _, cancel := range orphanCancels {
				cancel()
			}
			return err
		}
		ordered = append(ordered, run)
		if call.ID != "" {
			r.byID[call.ID] = run
		}
	}
	r.runs = ordered
	batchID := r.batchID
	r.mu.Unlock()

	for _, cancel := range orphanCancels {
		cancel()
	}
	if r.ledger != nil && batchID != "" {
		if err := r.ledger.FinalizeBatch(ctx, batchID); err != nil {
			return err
		}
	}
	return nil
}

func (r *TurnToolRuntime) executeBatch(
	ctx context.Context,
	batch toolBatch,
	onResult func(providers.ToolCall, string),
) toolBatchResult {
	ctxProvider, hasCtxProvider := r.executor.(ToolContextProvider)
	callCtxProvider, hasCallCtxProvider := r.executor.(ToolCallContextProvider)
	discoveryProvider, hasDiscoveryProvider := r.executor.(ToolDiscoveryProvider)

	if !batch.concurrent || len(batch.calls) == 1 {
		msgs := make([]providers.ChatMessage, 0, len(batch.calls))
		requestContext := make([]ContextSegment, 0, len(batch.calls))
		for _, call := range batch.calls {
			result, err := r.executeOrAwaitRun(ctx, call)
			if err != nil {
				return toolBatchResult{err: err}
			}
			r.notifyResult(call, result, onResult)
			msgs = append(msgs, toolResultMessage(call, r.invocationIDForCall(call), result, discoveryProvider, hasDiscoveryProvider))
			if extra := takeToolAdditionalContext(call, callCtxProvider, hasCallCtxProvider, ctxProvider, hasCtxProvider); extra != "" {
				segment := postToolAdditionalContextSegment(call.Name, extra)
				if len(segment.Messages) > 0 {
					requestContext = append(requestContext, segment)
				}
			}
		}
		return toolBatchResult{messages: msgs, requestContext: requestContext}
	}

	runs := make([]*toolRun, len(batch.calls))
	r.mu.Lock()
	for i, call := range batch.calls {
		run := r.runForCallLocked(call)
		r.startRunLocked(ctx, run, false)
		runs[i] = run
	}
	r.mu.Unlock()

	msgs := make([]providers.ChatMessage, len(batch.calls))
	requestContext := make([]ContextSegment, 0, len(batch.calls))
	for i, call := range batch.calls {
		result, err := r.awaitRunResult(ctx, runs[i])
		if err != nil {
			return toolBatchResult{err: err}
		}
		r.notifyResult(call, result, onResult)
		msgs[i] = toolResultMessage(call, runs[i].invocationID, result, discoveryProvider, hasDiscoveryProvider)
		if hasCallCtxProvider {
			if extra := callCtxProvider.TakeAdditionalContext(call); extra != "" {
				segment := postToolAdditionalContextSegment(call.Name, extra)
				if len(segment.Messages) > 0 {
					requestContext = append(requestContext, segment)
				}
			}
		}
	}
	return toolBatchResult{messages: msgs, requestContext: requestContext}
}

func takeToolAdditionalContext(
	call providers.ToolCall,
	callProvider ToolCallContextProvider,
	hasCallProvider bool,
	legacyProvider ToolContextProvider,
	hasLegacyProvider bool,
) string {
	if hasCallProvider {
		return callProvider.TakeAdditionalContext(call)
	}
	if hasLegacyProvider {
		return legacyProvider.LastAdditionalContext()
	}
	return ""
}

type toolBatchResult struct {
	messages       []providers.ChatMessage
	requestContext []ContextSegment
	err            error
}

func toolResultMessage(
	call providers.ToolCall,
	invocationID string,
	result toolresult.Result,
	provider ToolDiscoveryProvider,
	ok bool,
) providers.ChatMessage {
	return providers.ChatMessage{
		Role:                 "tool",
		Name:                 call.Name,
		ProviderItemProvider: call.ProviderItemProvider,
		ProviderItemModel:    call.ProviderItemModel,
		ToolCallID:           call.ID,
		ToolInvocationID:     invocationID,
		ToolResultKind:       call.Kind,
		Content:              result.TextProjection(),
		ToolResult:           resultPointer(result),
		DiscoveredTools:      discoveredToolsForCall(provider, ok, call),
	}
}

func (r *TurnToolRuntime) invocationIDForCall(call providers.ToolCall) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.byID[call.ID]; run != nil {
		return run.invocationID
	}
	return ""
}

func discoveredToolsForCall(provider ToolDiscoveryProvider, ok bool, call providers.ToolCall) []providers.LoadableToolDefinition {
	if !ok || provider == nil {
		return nil
	}
	return providers.CloneLoadableToolDefinitions(provider.DiscoveredTools(call))
}

func postToolAdditionalContextSegment(toolName, content string) ContextSegment {
	content = strings.TrimSpace(content)
	if content == "" {
		return ContextSegment{}
	}
	title := "Hook context"
	if name := strings.TrimSpace(toolName); name != "" {
		title = "Hook context for " + name
	}
	return RequestOnlyContextBlockSegment([]wuucontext.Block{{
		Kind:    wuucontext.BlockAdditionalContext,
		Title:   title,
		Source:  "hooks.post_tool_use",
		Content: content,
	}})
}

func (r *TurnToolRuntime) executeOrAwaitRun(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	r.mu.Lock()
	run := r.runForCallLocked(call)
	r.startRunLocked(ctx, run, false)
	r.mu.Unlock()
	return r.awaitRunResult(ctx, run)
}

func (r *TurnToolRuntime) runForCallLocked(call providers.ToolCall) *toolRun {
	if r.byID == nil {
		r.byID = map[string]*toolRun{}
	}
	if call.ID != "" {
		if run := r.byID[call.ID]; run != nil {
			return run
		}
	}
	run := &toolRun{
		call:      call,
		order:     len(r.runs),
		finalized: true,
		done:      make(chan struct{}),
	}
	run.concurrencySafe = toolCanRunConcurrently(r.executor, call)
	run.streamSafe = toolCanStartDuringStreaming(r.executor, call)
	r.runs = append(r.runs, run)
	if call.ID != "" {
		r.byID[call.ID] = run
	}
	return run
}

func (r *TurnToolRuntime) awaitRunResult(ctx context.Context, run *toolRun) (toolresult.Result, error) {
	result, err := run.wait(ctx)
	if err == nil {
		return result, nil
	}
	return toolresult.Result{}, err
}

func executeToolResult(ctx context.Context, executor ToolExecutor, call providers.ToolCall) (result toolresult.Result, err error) {
	if executor == nil {
		return toolresult.Result{}, context.Canceled
	}
	arguments := strings.TrimSpace(call.Arguments)
	if arguments != "" && !json.Valid([]byte(arguments)) {
		return toolresult.Result{}, toolerrors.New(
			toolerrors.InvalidArguments,
			fmt.Sprintf("tool %q arguments are invalid JSON", call.Name),
		)
	}
	// A panic in a tool executor must not crash the whole process: convert it
	// into an error result so the turn survives and the model sees the failure.
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("tool panicked: %v", rec)
			result = toolresult.FromErrorText(errorJSON(err))
		}
	}()
	if rich, ok := executor.(RichToolExecutor); ok {
		result, err = rich.ExecuteResult(ctx, call)
	} else {
		var text string
		text, err = executor.Execute(ctx, call)
		result = toolresult.FromText(text)
	}
	if err != nil {
		result.IsError = true
	}
	if validationErr := result.Validate(); validationErr != nil {
		return toolresult.FromErrorText(errorJSON(validationErr)), validationErr
	}
	return result, err
}

func (r *TurnToolRuntime) notifyResult(call providers.ToolCall, result toolresult.Result, legacy func(providers.ToolCall, string)) {
	r.mu.Lock()
	detail := r.onResultDetail
	r.mu.Unlock()
	if detail != nil {
		detail(call, result.Clone())
	}
	if legacy != nil {
		legacy(call, result.TextProjection())
	}
}

func resultPointer(result toolresult.Result) *toolresult.Result {
	clone := result.Clone()
	return &clone
}

func toolCanRunConcurrently(executor ToolExecutor, call providers.ToolCall) bool {
	if executor == nil {
		return false
	}
	mp, ok := executor.(ToolMetadataProvider)
	if !ok {
		return false
	}
	meta, found := mp.ToolMetadata(call)
	return found && meta.ConcurrencySafe
}

func toolCanStartDuringStreaming(executor ToolExecutor, call providers.ToolCall) bool {
	if executor == nil {
		return false
	}
	mp, ok := executor.(ToolMetadataProvider)
	if !ok {
		return false
	}
	meta, found := mp.ToolMetadata(call)
	return found && meta.ReadOnly && meta.ConcurrencySafe
}
