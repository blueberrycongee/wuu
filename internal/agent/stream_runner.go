package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// StreamCallback receives streaming events for live clients.
type StreamCallback func(event providers.StreamEvent)

const maxConsecutiveProactiveCompactFailures = 3

// StreamRunner manages one multi-step coding turn with streaming.
// It is a thin wrapper around RunToolLoop that supplies a streamStep
// adapter (Step → providers.StreamClient.StreamChat with recovery),
// so the actual loop logic — step counting, finish handling,
// context-overflow auto-compact — comes from the same code as Runner.
type StreamRunner struct {
	Client       providers.StreamClient
	ProviderName string
	Tools        ToolExecutor
	ToolLedger   *toolledger.Ledger
	Model        string
	APIModel     string
	// ProviderObservationKey is an opaque identity for the configured provider
	// instance and its endpoint/protocol. Runtime construction fills it so an
	// in-place provider reconfiguration cannot reuse stale context observations.
	ProviderObservationKey string
	SystemPrompt           string
	// SystemPromptSections mirrors SystemPrompt as metadata only, allowing
	// request telemetry to explain the stable prompt without exposing text.
	SystemPromptSections []SystemPromptSectionInfo
	// CompactionRegistry resolves a generation-owned compactor for each run.
	CompactionRegistry  *CompactionRegistry
	CompactionNoteStore CompactionNoteStore
	// ArchiveHistory commits original in-run messages before a fresh context
	// releases them. App-server installs this per turn for persisted sessions.
	ArchiveHistory     HistoryArchiveFunc
	CommitFreshContext FreshContextCommitFunc
	MaxSteps           int
	Temperature        float64
	// MediaInput is the admission policy for user-supplied media, resolved
	// from the session's model capabilities. Zero value means fully
	// unprobed (auto), preserving legacy pass-through behavior.
	MediaInput providers.MediaInputPolicy
	OnEvent    StreamCallback

	// OnUsage, when non-nil, is invoked once per LLM round-trip with
	// the per-call token counts reported by the provider. This mirrors
	// the field of the same name on the non-streaming Runner so that
	// callers driving long-lived background runs (e.g. sub-agents) can
	// surface live token accumulation while the run is still going.
	OnUsage func(input, output int)
	// OnTokenUsage reports the full per-call token usage, including
	// prompt cache read/write counts.
	OnTokenUsage func(usage providers.TokenUsage)

	// ContextWindowOverride is the resolved provider/model context window.
	// Zero means the runtime does not know the limit and proactive compaction
	// stays disabled; reactive provider overflow recovery still runs.
	ContextWindowOverride int
	// ForceToolFirstStep pins the run's first request to the named tool via
	// forced tool_choice. See LoopConfig.ForceToolFirstStep.
	ForceToolFirstStep string
	// CompactThresholdTokens is the modelbudget-derived absolute token count
	// at which proactive compaction fires. modelbudget owns the formula
	// (effective window minus output reserve and safety buffer); the loop
	// consumes this value instead of re-deriving its own so trace, UI, and
	// trigger can never disagree. Zero falls back to the loop's legacy
	// window-based derivation.
	CompactThresholdTokens int
	// MaxInputTokens lets callers pass a provider/model prompt limit
	// when it is smaller than the total context window.
	MaxInputTokens int
	// OutputReserveTokens lets callers pass a provider/model output limit
	// for compact threshold math without forcing a request max_tokens value.
	OutputReserveTokens int
	// CompactThresholdPct lets callers compact earlier than the default
	// usable-window calculation. Zero means auto.
	CompactThresholdPct float64
	// CompactKeepRecentTokens overrides the default recent raw-history budget
	// kept after compaction. Zero means use the default.
	CompactKeepRecentTokens int

	// DisableAutoCompact turns off the proactive fill-rate trigger.
	// The reactive context-overflow recovery still runs. Off by default.
	DisableAutoCompact bool

	// ForceInitialCompact runs one compact pass at run entry, bypassing the
	// fill-rate threshold. Explicit /compact pairs this with CompactOnly;
	// recovery flows may still continue to a normal provider request. It works
	// even when DisableAutoCompact is on.
	ForceInitialCompact bool
	// CompactOnly stops after the initial compact pass. This is for explicit
	// control-plane compaction, not normal user prompts.
	CompactOnly bool

	// StreamingToolExecution, when true, starts executing read-only tools
	// during model streaming (before the full response arrives). Off by default
	// until stabilized.
	StreamingToolExecution bool
	// ToolWaitInterrupt supplies a turn-scoped signal to wait-only tools that
	// can safely return while leaving their underlying work alive.
	ToolWaitInterrupt func() <-chan struct{}

	// BeforeStep, when set, is called at the start of each model
	// round right before building the provider request. Any returned
	// messages are appended to history for that round.
	BeforeStep func() []providers.ChatMessage
	// BeforeModelStep is the context-aware extension boundary for durable,
	// append-only plugin contributions. Returned messages join live history and
	// are persisted with the rest of the turn.
	BeforeModelStep func(context.Context, int, []providers.ChatMessage) ([]providers.ChatMessage, error)

	// BeforeRequestContext, when set, is called before each provider request.
	// Returned segments are assembled into that request but are not appended
	// to live or durable conversation history.
	BeforeRequestContext func() []ContextSegment
	// BeforeRequest transforms the complete provider-neutral request immediately
	// before it is sent. Runtime plugin hosts install their typed pipeline here.
	BeforeRequest func(context.Context, *providers.ChatRequest) error
	// OnRequestContext receives metadata-only summaries of request-only model
	// context assembled before requests.
	OnRequestContext func(info RequestContextInfo)
	// OnCompactAttempt receives metadata-only compact attempt diagnostics.
	OnCompactAttempt func(info CompactAttemptInfo)
	BeforeCompact    func(context.Context, CompactReason) error
	AfterCompact     func(context.Context, CompactReason, error) error
	// OnToolBatchRejected receives metadata-only diagnostics for whole
	// tool-call batches rejected before any tool executes.
	OnToolBatchRejected func(info ToolBatchRejectionInfo)

	// AfterTurn, when set, is invoked after a successful turn has
	// completed and usage state has been committed. It is best-effort:
	// implementations should return quickly and run slow work in the
	// background if needed.
	AfterTurn func(ctx context.Context, runner *StreamRunner, history []providers.ChatMessage, result LoopResult)

	// Effort controls reasoning depth. See ChatRequest.Effort.
	Effort string
	// Variant is the selected model-scoped provider option bundle.
	Variant string
	// ProviderOptions carries provider-specific model options selected by the
	// active model variant.
	ProviderOptions map[string]any
	// NativeDeferredToolDiscovery lets provider adapters use native
	// deferred-tool declarations for tools marked DeferLoading.
	NativeDeferredToolDiscovery bool
	// PromptCacheKey is a stable conversation-scoped cache key forwarded
	// to providers that support explicit prompt-cache routing.
	PromptCacheKey string
	// InferenceOperationKind and InferenceWorkloadProfile identify each model
	// round for the shared retry lifecycle. Empty values use the interactive
	// agent defaults.
	InferenceOperationKind   providers.InferenceOperationKind
	InferenceWorkloadProfile providers.InferenceWorkloadProfile
	// InferenceJournal is the durable write-ahead sink shared by every model
	// round and nested compaction owned by this runner.
	InferenceJournal providers.InferenceJournal
	// LoopDriver owns turn policy while the runner remains the kernel gateway
	// for provider, tool-ledger, history, and streaming invariants. Nil selects
	// the behavior-compatible default driver.
	LoopDriver             loopdriver.Driver
	DriverCheckpointStore  loopdriver.CheckpointStore
	ModelInputReceiptStore ModelInputReceiptStore

	usageMu                sync.Mutex
	conversationUsage      *UsageTracker
	trackedHistoryLen      int
	trackedHistoryHash     string
	trackedHistoryTailHash string
	trackedHistoryTailID   string
	trackedUsageContract   string

	// retainedContextMu guards the cross-turn request-context state used for
	// prompt-cache prefix continuity. The state is fingerprinted against the
	// durable history, so handing back stale state after an external history
	// rewrite is safe — the loop drops it on mismatch.
	retainedContextMu      sync.Mutex
	retainedRequestContext *RetainedRequestContextState

	compactMu                sync.Mutex
	proactiveCompactFailures int

	sysPromptMu sync.RWMutex
}

// Run executes one prompt with streaming tool-use loop.
func (r *StreamRunner) Run(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is required")
	}
	var history []providers.ChatMessage
	sysPrompt, _ := r.systemPromptSnapshot()
	if strings.TrimSpace(sysPrompt) != "" {
		history = append(history, providers.ChatMessage{Role: "system", Content: sysPrompt})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: prompt})
	res, err := r.RunWithCallback(ctx, history, r.OnEvent)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// RunWithCallback executes a conversation turn with a per-call event callback.
// It accepts the full message history and returns the loop result, including
// any new messages produced during this turn and whether history was rewritten
// by auto-compaction.
// UpdateSystemPrompt safely updates the system prompt at runtime.
// The new prompt takes effect on the next turn.
func (r *StreamRunner) UpdateSystemPrompt(newPrompt string) {
	r.UpdateSystemPromptWithSections(newPrompt, nil)
}

// UpdateSystemPromptWithSections safely updates the system prompt and its
// telemetry metadata at runtime. The new prompt takes effect on the next turn.
func (r *StreamRunner) UpdateSystemPromptWithSections(newPrompt string, sections []SystemPromptSectionInfo) {
	r.sysPromptMu.Lock()
	defer r.sysPromptMu.Unlock()
	r.SystemPrompt = newPrompt
	r.SystemPromptSections = cloneSystemPromptSections(sections)
}

func (r *StreamRunner) systemPromptSnapshot() (string, []SystemPromptSectionInfo) {
	r.sysPromptMu.RLock()
	defer r.sysPromptMu.RUnlock()
	return r.SystemPrompt, cloneSystemPromptSections(r.SystemPromptSections)
}

func (r *StreamRunner) RunWithCallback(ctx context.Context, history []providers.ChatMessage, onEvent StreamCallback) (LoopResult, error) {
	driver := r.LoopDriver
	if driver == nil {
		driver = loopdriver.DefaultDriver{}
	}
	descriptor := driver.Descriptor()
	input := loopdriver.PersistedInput{Messages: providers.CloneChatMessages(history)}
	execution := loopdriver.ExecutionContextFromContext(ctx)

	var instance loopdriver.Instance
	if r.DriverCheckpointStore != nil {
		checkpoint, ok, err := r.DriverCheckpointStore.Load(ctx)
		if err != nil {
			return LoopResult{}, fmt.Errorf("load loop driver checkpoint: %w", err)
		}
		if ok {
			if checkpoint.ContractVersion != loopdriver.ContractVersion || checkpoint.DriverID != descriptor.ID || checkpoint.DriverVersion != descriptor.Version {
				return LoopResult{}, fmt.Errorf(
					"loop driver checkpoint requires %s@%s contract %d; active driver is %s@%s contract %d",
					checkpoint.DriverID,
					checkpoint.DriverVersion,
					checkpoint.ContractVersion,
					descriptor.ID,
					descriptor.Version,
					loopdriver.ContractVersion,
				)
			}
			instance, err = driver.Resume(execution, input, checkpoint)
			if err != nil {
				return LoopResult{}, fmt.Errorf("resume loop driver: %w", err)
			}
		}
	}
	if instance == nil {
		var err error
		instance, err = driver.Create(execution, input)
		if err != nil {
			return LoopResult{}, fmt.Errorf("create loop driver: %w", err)
		}
	}
	if instance == nil {
		return LoopResult{}, errors.New("loop driver returned a nil instance")
	}

	gateway := &streamDriverGateway{
		runner:     r,
		onEvent:    onEvent,
		descriptor: descriptor,
		execution:  execution,
		results:    make(map[string]LoopResult),
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			instance.Cancel(ctx.Err().Error())
		case <-done:
		}
	}()
	outcome, runErr := instance.Run(ctx, gateway)
	close(done)
	instance.Shutdown()

	res, ok := gateway.result(outcome.ReceiptID)
	if outcome.ReceiptID != "" && !ok {
		runErr = errors.Join(runErr, fmt.Errorf("loop driver returned unknown kernel receipt %q", outcome.ReceiptID))
	}
	res.DriverID = descriptor.ID
	res.DriverVersion = descriptor.Version
	res.DriverContractVersion = loopdriver.ContractVersion
	res.DriverStatus = string(outcome.Status)
	res.DriverCheckpoint = append(json.RawMessage(nil), outcome.Checkpoint.State...)
	return res, runErr
}

type streamDriverGateway struct {
	runner     *StreamRunner
	onEvent    StreamCallback
	descriptor loopdriver.Descriptor
	execution  loopdriver.ExecutionContext
	results    map[string]LoopResult
	next       int
}

func (gateway *streamDriverGateway) ExecuteModelLoop(ctx context.Context, input loopdriver.PersistedInput, policy loopdriver.LoopPolicy) (loopdriver.ModelLoopReceipt, error) {
	gateway.next++
	receipt := loopdriver.ModelLoopReceipt{ID: fmt.Sprintf("model-loop-%d", gateway.next)}
	result, err := gateway.runner.runModelToolLoop(ctx, input.Messages, gateway.onEvent, policy, gateway.descriptor, gateway.execution)
	gateway.results[receipt.ID] = result
	return receipt, err
}

func (gateway *streamDriverGateway) WriteCheckpoint(ctx context.Context, checkpoint loopdriver.Checkpoint) error {
	if checkpoint.ContractVersion != loopdriver.ContractVersion || checkpoint.DriverID != gateway.descriptor.ID || checkpoint.DriverVersion != gateway.descriptor.Version {
		return errors.New("loop driver attempted to write a checkpoint for another contract")
	}
	if gateway.runner.DriverCheckpointStore == nil {
		return nil
	}
	return gateway.runner.DriverCheckpointStore.Save(ctx, checkpoint)
}

func (gateway *streamDriverGateway) result(receiptID string) (LoopResult, bool) {
	result, ok := gateway.results[receiptID]
	return result, ok
}

func (r *StreamRunner) runModelToolLoop(ctx context.Context, history []providers.ChatMessage, onEvent StreamCallback, policy loopdriver.LoopPolicy, descriptor loopdriver.Descriptor, execution loopdriver.ExecutionContext) (LoopResult, error) {
	client, providerObservationKey := r.providerConnectionSnapshot()
	if client == nil {
		return LoopResult{}, errors.New("client is required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return LoopResult{}, errors.New("model is required")
	}
	ctx = providers.WithInferenceJournal(ctx, r.InferenceJournal)
	requestModel := strings.TrimSpace(r.APIModel)
	if requestModel == "" {
		requestModel = r.Model
	}
	runUsageContract := usageContractKey(providerObservationKey, r.ProviderName, requestModel, r.Variant, r.Effort, r.ProviderOptions)
	history = filterDurableHistory(history)
	recoveredToolMessages, err := r.pendingToolResultMessages(ctx, history)
	if err != nil {
		return LoopResult{}, fmt.Errorf("recover pending tool results: %w", err)
	}
	history = append(history, recoveredToolMessages...)
	runUsage, baseHistoryLen := r.prepareUsageTrackerForContract(history, runUsageContract)

	effectiveOnEvent := onEvent

	tools := r.Tools
	if policy.DisableTools {
		tools = nil
	}
	step := &streamStep{
		client:                  client,
		onEvent:                 effectiveOnEvent,
		tools:                   tools,
		toolLedger:              r.ToolLedger,
		enableStreamingToolExec: r.StreamingToolExecution,
	}

	compactContextTokens := r.ContextWindowOverride
	maxCtx := compactContextTokens
	compactThresholdTokens := r.CompactThresholdTokens
	if policy.DisableCompaction || r.DisableAutoCompact || r.proactiveCompactCircuitOpen() {
		maxCtx = 0 // disables the proactive trigger inside RunToolLoop
		compactThresholdTokens = 0
	}
	_, systemPromptSections := r.systemPromptSnapshot()
	beforeStep := r.BeforeStep
	if beforeStep != nil {
		capturedBeforeStep := beforeStep
		beforeStep = func() []providers.ChatMessage {
			return filterDurableHistory(capturedBeforeStep())
		}
	}
	contextWindowProvider := r.contextWindowProvider()
	contextWindowsEnabled := contextWindowProvider != nil && r.ArchiveHistory != nil && !policy.DisableCompaction
	beforeRequestContext := r.BeforeRequestContext
	if contextWindowsEnabled {
		beforeRequestContext = withContextWindowGuidance(beforeRequestContext)
	}
	operationKind := r.InferenceOperationKind
	if operationKind == "" {
		operationKind = providers.InferenceOperationAgentRound
	}
	workloadProfile := r.InferenceWorkloadProfile
	if workloadProfile == "" {
		workloadProfile = providers.InferenceProfileInteractive
	}
	maxSteps := r.MaxSteps
	if policy.ModelRoundLimit > 0 {
		maxSteps = policy.ModelRoundLimit
	}
	forceToolFirstStep := r.ForceToolFirstStep
	if policy.DisableTools {
		forceToolFirstStep = ""
	}
	cfg := LoopConfig{
		Tools:                    tools,
		Model:                    requestModel,
		ProviderName:             r.ProviderName,
		ProviderObservationKey:   providerObservationKey,
		ModelVariant:             r.Variant,
		InferenceOperationKind:   operationKind,
		InferenceWorkloadProfile: workloadProfile,
		SessionID:                execution.SessionID,
		ExecutionID:              execution.ExecutionID,
		DriverID:                 descriptor.ID,
		DriverVersion:            descriptor.Version,
		ModelInputReceiptStore:   r.ModelInputReceiptStore,
		Temperature:              r.Temperature,
		MediaInput:               r.MediaInput,
		MaxSteps:                 maxSteps,
		MaxContextTokens:         maxCtx,
		MaxInputTokens:           r.MaxInputTokens,
		OutputReserveTokens:      r.OutputReserveTokens,
		CompactThresholdTokens:   compactThresholdTokens,
		CompactThresholdPct:      r.CompactThresholdPct,
		CompactKeepRecentTokens:  r.CompactKeepRecentTokens,
		ForceInitialCompact:      r.ForceInitialCompact && !policy.DisableCompaction,
		CompactOnly:              r.CompactOnly && !policy.DisableCompaction,
		ToolWaitInterrupt:        r.ToolWaitInterrupt,
		BeforeStep:               beforeStep,
		BeforeModelStep:          r.BeforeModelStep,
		BeforeRequestContext:     beforeRequestContext,
		BeforeRequest:            r.BeforeRequest,
		SystemPromptSections:     systemPromptSections,
		ForceToolFirstStep:       forceToolFirstStep,
		OnRequestContext: func(info RequestContextInfo) {
			if r.OnRequestContext != nil {
				r.OnRequestContext(info)
			}
			if effectiveOnEvent == nil {
				return
			}
			effectiveOnEvent(providers.StreamEvent{
				Type:           providers.EventRequestContext,
				RequestContext: requestContextSummary(info),
			})
		},
		OnUsage:       r.OnUsage,
		OnTokenUsage:  r.OnTokenUsage,
		BeforeCompact: r.BeforeCompact,
		AfterCompact:  r.AfterCompact,
		OnMessage: func(msg providers.ChatMessage) {
			if effectiveOnEvent == nil || msg.Hidden || isNonDurableHistoryMessage(msg) || isInternalContextHistoryMessage(msg) {
				return
			}
			copyMsg := msg
			effectiveOnEvent(providers.StreamEvent{
				Type:    providers.EventMessage,
				Message: &copyMsg,
			})
		},
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			budget, budgetErr := applyAdaptiveCompactBudget(ctx, messages, compact.Budget{
				ContextTokens:       compactContextTokens,
				InputTokens:         r.MaxInputTokens,
				OutputReserveTokens: r.OutputReserveTokens,
				KeepRecentTokens:    r.CompactKeepRecentTokens,
			})
			if budgetErr != nil {
				return messages, budgetErr
			}
			return compact.CompactWithNativeOrSummary(ctx, messages, client, requestModel, budget, compact.NativeOptions{
				Provider:                    r.ProviderName,
				Tools:                       toolDefinitions(tools),
				Temperature:                 r.Temperature,
				ProviderOptions:             r.ProviderOptions,
				MediaInput:                  r.MediaInput,
				NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
			})
		},
		CompactionRegistry:  r.CompactionRegistry,
		CompactionNoteStore: r.CompactionNoteStore,
		// Forward each tool result through the streaming callback so
		// clients can render tool output live (the loop itself only
		// records the tool message into the history).
		OnToolResultDetail: func(call providers.ToolCall, result toolresult.Result) {
			if effectiveOnEvent == nil {
				return
			}
			textResult := result.TextProjection()
			toolCall := enrichToolCallDisplay(r.Tools, providers.ToolCall{
				ID:                   call.ID,
				ProviderItemID:       call.ProviderItemID,
				ProviderItemProvider: call.ProviderItemProvider,
				ProviderItemModel:    call.ProviderItemModel,
				Name:                 call.Name,
				Arguments:            call.Arguments,
				Kind:                 call.Kind,
				Display:              call.Display,
			})
			effectiveOnEvent(providers.StreamEvent{
				Type:             providers.EventToolUseEnd,
				ToolCall:         &toolCall,
				ToolResult:       truncateLog(textResult, 2000),
				ToolResultDetail: resultPointer(result),
			})
			if update, ok := todoUpdateEventFromToolResult(toolCall, textResult); ok {
				effectiveOnEvent(providers.StreamEvent{
					Type:       providers.EventTodoUpdate,
					TodoUpdate: update,
				})
			}
		},
		OnCompactStart: func(reason CompactReason) {
			if effectiveOnEvent == nil {
				return
			}
			effectiveOnEvent(providers.StreamEvent{
				Type:          providers.EventCompact,
				CompactReason: string(reason),
				CompactPhase:  providers.CompactPhaseStarted,
			})
		},
		// Surface auto-compact events as stream events. The loop fires
		// this for both the proactive and the reactive overflow path.
		OnCompact: func(info CompactInfo) {
			if effectiveOnEvent == nil {
				return
			}
			effectiveOnEvent(providers.StreamEvent{
				Type:           providers.EventCompact,
				Content:        formatCompactNotice(info),
				CompactReason:  string(info.Reason),
				CompactPhase:   providers.CompactPhaseCompleted,
				CompactSummary: info.Summary,
			})
		},
		OnCompactAttempt: func(info CompactAttemptInfo) {
			r.recordCompactAttempt(info)
			if r.OnCompactAttempt != nil {
				r.OnCompactAttempt(info)
			}
			if effectiveOnEvent == nil {
				return
			}
			notice, ok := formatCompactAttemptNotice(info)
			if !ok {
				return
			}
			phase := providers.CompactPhaseCompleted
			if info.Status == CompactAttemptFailed {
				phase = providers.CompactPhaseFailed
			}
			effectiveOnEvent(providers.StreamEvent{
				Type:          providers.EventCompact,
				Content:       notice,
				CompactReason: string(info.Reason),
				CompactPhase:  phase,
			})
		},
		OnToolBatchRejected: func(info ToolBatchRejectionInfo) {
			if r.OnToolBatchRejected != nil {
				r.OnToolBatchRejected(info)
			}
		},
		UsageTracker:                runUsage,
		Effort:                      r.Effort,
		ProviderOptions:             provideroptions.Clone(r.ProviderOptions),
		NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
		PromptCacheKey:              r.PromptCacheKey,
		RetainedRequestContext:      r.takeRetainedRequestContext(),
	}
	cfg.ForkCompactionNote = r.compactionNoteFork(cfg.RetainedRequestContext)
	if contextWindowsEnabled {
		cfg.ArchiveHistory = r.ArchiveHistory
		cfg.FreshContextTokens = r.freshContextTargetTokens(compactThresholdTokens)
		cfg.FreshContext = func(ctx context.Context, messages []providers.ChatMessage, head, fixed, target int) ([]providers.ChatMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return buildFreshContext(messages, head, fixed, target)
		}
		cfg.AcceptFreshContext = func(acceptCtx context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
			if r.CommitFreshContext != nil {
				return r.CommitFreshContext(acceptCtx, messages, head, contextWindowProvider.CompactionKey(), CompactionNote{})
			}
			return messages, head, nil
		}
	}
	cfg.OnCompactionNote = func(status string, noteErr error) {
		emitCompactionNoteEvent(effectiveOnEvent, status, noteErr)
	}
	if policy.DisableCompaction {
		cfg.Compact = nil
		cfg.CompactionRegistry = nil
	}

	res, err := RunToolLoop(ctx, history, cfg, step)
	r.storeRetainedRequestContext(res.RetainedRequestContext)
	res.ContextTokens = runUsage.EstimateCurrent()
	if len(recoveredToolMessages) > 0 && !res.HistoryRewritten {
		res.NewMessages = append(append([]providers.ChatMessage(nil), recoveredToolMessages...), res.NewMessages...)
	}
	res.NewMessages = filterDurableHistory(res.NewMessages)
	res.DurableNewMessages = filterDurableHistory(res.DurableNewMessages)
	if len(recoveredToolMessages) > 0 {
		res.DurableNewMessages = append(append([]providers.ChatMessage(nil), recoveredToolMessages...), res.DurableNewMessages...)
	}
	if err != nil {
		r.commitUsageTrackerForContract(runUsage, history[:baseHistoryLen], runUsageContract)
		return res, err
	}
	finalHistory := append(providers.CloneChatMessages(history[:baseHistoryLen]), res.NewMessages...)
	if len(recoveredToolMessages) > 0 && !res.HistoryRewritten {
		// Recovered results already belong to the input history used by the
		// loop and were prepended to NewMessages only so callers persist them.
		finalHistory = append(providers.CloneChatMessages(history[:baseHistoryLen]), res.NewMessages[len(recoveredToolMessages):]...)
	}
	if res.HistoryRewritten {
		finalHistory = providers.CloneChatMessages(res.NewMessages)
	}
	r.commitUsageTrackerForContract(runUsage, finalHistory, runUsageContract)
	if r.AfterTurn != nil {
		fullHistory := make([]providers.ChatMessage, 0, len(history)+len(res.NewMessages))
		fullHistory = append(fullHistory, history...)
		fullHistory = append(fullHistory, res.NewMessages...)
		fullHistory = filterDurableHistory(fullHistory)
		r.AfterTurn(ctx, r, fullHistory, res)
	}
	return res, nil
}

func emitCompactionNoteEvent(onEvent StreamCallback, status string, noteErr error) {
	if onEvent == nil {
		return
	}
	// Using (or synchronously generating) a note is an implementation detail of
	// the compaction pass. OnCompact emits the authoritative completed event
	// immediately afterward; surfacing both would complete the pending row here
	// and force the real result into a second context-compaction row.
	if status == "context_note" || status == "forced" {
		return
	}
	// Stopping a turn also stops its hidden note fork. That is an expected
	// interruption, not a compaction failure that should be shown to the user.
	if status == "failed" && errors.Is(noteErr, context.Canceled) {
		return
	}
	content := "Context note used."
	switch status {
	case "updated":
		content = "Context note updated."
	case "forced":
		content = "Forced context note generated."
	case "failed":
		content = "Context note failed."
		if noteErr != nil {
			content += " " + noteErr.Error()
		}
	}
	onEvent(providers.StreamEvent{
		Type: providers.EventCompact, Content: content,
		CompactReason: "context_note", CompactPhase: providers.CompactPhaseCompleted,
	})
}

func (r *StreamRunner) pendingToolResultMessages(ctx context.Context, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
	if r == nil || r.ToolLedger == nil {
		return nil, nil
	}
	projected := make([]string, 0)
	for _, message := range history {
		if id := strings.TrimSpace(message.ToolInvocationID); id != "" {
			projected = append(projected, id)
		}
	}
	if err := r.ToolLedger.MarkProjected(ctx, projected); err != nil {
		return nil, err
	}
	pending, err := r.ToolLedger.PendingProjection(ctx)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	var messages []providers.ChatMessage
	for index := 0; index < len(pending); {
		batchID := pending[index].BatchID
		end := index
		calls := make([]providers.ToolCall, 0, 1)
		for end < len(pending) && pending[end].BatchID == batchID {
			invocation := pending[end]
			calls = append(calls, providers.ToolCall{
				ID: invocation.ProviderCallID, Name: invocation.ToolName,
				Kind: invocation.ToolKind, Arguments: invocation.Arguments,
			})
			end++
		}
		messages = append(messages, providers.ChatMessage{Role: "assistant", ToolCalls: calls})
		for _, invocation := range pending[index:end] {
			messages = append(messages, providers.ChatMessage{
				Role: "tool", Name: invocation.ToolName, ToolCallID: invocation.ProviderCallID,
				ToolInvocationID: invocation.ID, ToolResultKind: invocation.ToolKind,
				Content: invocation.Result.TextProjection(), ToolResult: resultPointer(invocation.Result),
			})
		}
		index = end
	}
	return messages, nil
}

func todoUpdateEventFromToolResult(call providers.ToolCall, result string) (*providers.TodoUpdate, bool) {
	if call.Display == nil || call.Display.Capability != "todo" {
		return nil, false
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(result), &status); err != nil || status.Status != "updated" {
		return nil, false
	}
	var update providers.TodoUpdate
	if err := json.Unmarshal([]byte(call.Arguments), &update); err != nil || len(update.Todos) == 0 {
		return nil, false
	}
	return &update, true
}

func isInternalContextHistoryMessage(msg providers.ChatMessage) bool {
	return compact.IsInternalContextMessage(msg)
}

// prepareUsageTracker snapshots the runner's shared conversation
// usage state and advances it to the history passed for this turn.
// The returned tracker is run-local; callers must commit it
// explicitly after deciding which history actually persists.
func (r *StreamRunner) prepareUsageTracker(history []providers.ChatMessage) (*UsageTracker, int) {
	return r.prepareUsageTrackerForContract(history, r.currentUsageContract())
}

func (r *StreamRunner) prepareUsageTrackerForContract(history []providers.ChatMessage, currentUsageContract string) (*UsageTracker, int) {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()

	if r.conversationUsage == nil {
		r.conversationUsage = NewUsageTracker()
	}

	tracker := r.conversationUsage.Clone()
	trackedLen := r.trackedHistoryLen
	trackedHash := r.trackedHistoryHash
	trackedTailHash := r.trackedHistoryTailHash
	trackedTailID := r.trackedHistoryTailID
	if currentUsageContract != "" && r.trackedUsageContract != "" && currentUsageContract != r.trackedUsageContract {
		tracker.Reset()
		tracker.SetAdjustment(UsageAdjustmentProviderContractReset)
		trackedLen = 0
		trackedHash = ""
		trackedTailHash = ""
		trackedTailID = ""
	}
	if trackedLen < 0 {
		trackedLen = 0
	}
	// Length-shrink safety net: if the tracked history got shorter than the
	// baseline expects, the baseline is stale and is rebuilt from scratch
	// below. Out-of-loop history replacement drives invalidation explicitly via
	// ResetConversationUsage rather than relying on this heuristic, because a
	// compaction can replace history with a summary that is byte-smaller but not
	// necessarily message-count-smaller.
	if trackedLen > len(history) {
		tracker.Reset()
		tracker.SetAdjustment(UsageAdjustmentLengthReset)
		trackedLen = 0
	}
	if trackedLen > 0 && trackedHash != "" && hashMessagesForRequestShape(history[:trackedLen]) != trackedHash {
		// Provider checkpoints and durable reloads can expand or normalize the
		// old prefix while retaining the exact response boundary that the live
		// provider usage measured. Preserve that trustworthy baseline and count
		// only messages appended after the boundary instead of pessimistically
		// re-estimating the entire durable transcript.
		breakdown := tracker.Breakdown()
		if anchor := findHistoryTailAnchor(history, trackedTailHash, trackedTailID); canRebaseUsageAfterTail(history, anchor, breakdown) {
			tracker.SetAdjustment(UsageAdjustmentRequestShapeTailRebase)
			tracker.RecordPendingMessages(history[anchor+1:])
			return tracker, len(history)
		}
		tracker.Reset()
		tracker.SetAdjustment(UsageAdjustmentRequestShapeReset)
		trackedLen = 0
	}
	if trackedLen == 0 {
		if tracker.Breakdown().Adjustment == "" {
			tracker.SetAdjustment(UsageAdjustmentInitialHistoryEstimate)
		}
		tracker.RecordPendingMessages(history)
		return tracker, len(history)
	}
	if len(history) > trackedLen {
		tracker.RecordPendingMessages(history[trackedLen:])
		trackedLen = len(history)
	}
	return tracker, trackedLen
}

// takeRetainedRequestContext hands the stored cross-turn request-context
// state to the next run and clears it, so a crashed or aborted run cannot
// replay stale state twice.
func (r *StreamRunner) takeRetainedRequestContext() *RetainedRequestContextState {
	r.retainedContextMu.Lock()
	defer r.retainedContextMu.Unlock()
	state := r.retainedRequestContext
	r.retainedRequestContext = nil
	return state
}

// storeRetainedRequestContext publishes a run's retained request-context
// state for the conversation's next run.
func (r *StreamRunner) storeRetainedRequestContext(state *RetainedRequestContextState) {
	r.retainedContextMu.Lock()
	defer r.retainedContextMu.Unlock()
	r.retainedRequestContext = state
}

// ContextWindowsAvailable reports whether the active extension supplies
// persistent working memory and opts into summary-free context windows.
func (r *StreamRunner) ContextWindowsAvailable() bool {
	return r != nil && r.contextWindowProvider() != nil
}

func (r *StreamRunner) contextWindowProvider() ContextWindowProvider {
	if r == nil || r.CompactionRegistry == nil {
		return nil
	}
	provider, ok := r.CompactionRegistry.Resolve(nil).(ContextWindowProvider)
	if !ok || provider == nil || !provider.ContextWindowsEnabled() {
		return nil
	}
	return provider
}

func (r *StreamRunner) requestModel() string {
	model := strings.TrimSpace(r.APIModel)
	if model == "" {
		model = strings.TrimSpace(r.Model)
	}
	return model
}

// compactionNoteFork uses the main model and tool surface. History addressing
// and request-only context can change the prefix; cache reuse must be measured,
// not assumed. Tool calls in the response are rejected and never executed.
func (r *StreamRunner) CompactionNoteFork() CompactionNoteFork {
	return r.compactionNoteFork(nil)
}

func (r *StreamRunner) compactionNoteFork(retained *RetainedRequestContextState) CompactionNoteFork {
	if r == nil {
		return func(context.Context, []providers.ChatMessage, CompactionNotePlan) (CompactionNoteForkResult, error) {
			return CompactionNoteForkResult{}, errors.New("context note runner is unavailable")
		}
	}
	client, _ := r.providerConnectionSnapshot()
	providerName := r.ProviderName
	model := r.requestModel()
	tools := toolDefinitions(r.Tools)
	temperature := r.Temperature
	effort := r.Effort
	providerOptions := provideroptions.Clone(r.ProviderOptions)
	nativeDeferredToolDiscovery := r.NativeDeferredToolDiscovery
	mediaInput := r.MediaInput
	beforeRequest := r.BeforeRequest
	promptCacheKey := r.PromptCacheKey
	inferenceJournal := r.InferenceJournal
	inputBudget := r.freshContextTargetTokens(0)
	return boundedCompactionNoteFork(func(ctx context.Context, history []providers.ChatMessage, plan CompactionNotePlan) (CompactionNoteForkResult, error) {
		if client == nil {
			return CompactionNoteForkResult{}, errors.New("context note client is unavailable")
		}
		messages := providers.CloneChatMessages(filterDurableHistory(history))
		if retained != nil && retained.validFor(messages) {
			messages = spliceRetainedContext(messages, retained.Messages)
		}
		messages = annotateCompactionNoteHistorySeqs(messages)
		messages = append(messages, providers.ChatMessage{
			Role: "user", Content: strings.TrimSpace(plan.Prompt), Hidden: true, ReadOnly: true,
			Origin: "internal", Cause: "compaction_note_fork",
		})
		req := providers.ChatRequest{
			Provider:                    providerName,
			Model:                       model,
			Messages:                    messages,
			Tools:                       tools,
			Temperature:                 temperature,
			Effort:                      effort,
			ProviderOptions:             provideroptions.Clone(providerOptions),
			NativeDeferredToolDiscovery: nativeDeferredToolDiscovery,
			MediaInput:                  mediaInput,
		}
		if plan.MaxBytes > 0 {
			// A cost ceiling, not a UTF-8 size guarantee. The bounded fork
			// validates actual bytes and regenerates overlong/truncated output.
			req.MaxTokens = max(1, plan.MaxBytes/3)
		}
		if beforeRequest != nil {
			forceBefore := strings.TrimSpace(req.ForceToolName)
			forceAvailableBefore := requestHasTool(req.Tools, forceBefore)
			if err := beforeRequest(ctx, &req); err != nil {
				return CompactionNoteForkResult{}, fmt.Errorf("transform context note request: %w", err)
			}
			if err := validateTransformedRequest(req, forceBefore, forceAvailableBefore); err != nil {
				return CompactionNoteForkResult{}, err
			}
		}
		if _, bounded := ctx.Value(compactionNoteBudgetKey{}).(int); bounded && estimateOutboundRequestTokens(req) > inputBudget {
			return CompactionNoteForkResult{}, errors.New("transformed context note request exceeds the model input budget")
		}
		cacheHint := buildCacheHint(req.Messages)
		applyPromptCacheKeyOverride(&cacheHint, promptCacheKey)
		req.CacheHint = cacheHint
		req.Operation = providers.EnsureInferenceOperation(req.Operation, providers.InferenceOperationCompaction, providers.InferenceProfileContinuationCritical)
		ctx = providers.WithInferenceJournal(ctx, inferenceJournal)
		result, err := NewStreamStep(client).Execute(ctx, req)
		if err != nil {
			return CompactionNoteForkResult{Usage: result.Usage}, err
		}
		if len(result.ToolCalls) != 0 {
			return CompactionNoteForkResult{Usage: result.Usage}, errors.New("context note fork attempted to call a tool")
		}
		if result.Truncated || result.FinishReason == providers.FinishReasonLength {
			return CompactionNoteForkResult{Usage: result.Usage}, errCompactionNoteTruncated
		}
		markdown := strings.TrimSpace(result.Content)
		if markdown == "" {
			return CompactionNoteForkResult{Usage: result.Usage}, errors.New("context note fork returned empty Markdown")
		}
		return CompactionNoteForkResult{Markdown: markdown, Usage: result.Usage}, nil
	})
}

// commitUsageTracker publishes a run-local usage snapshot as the new
// shared baseline for future turns.
func (r *StreamRunner) commitUsageTracker(tracker *UsageTracker, history []providers.ChatMessage) {
	r.commitUsageTrackerForContract(tracker, history, r.currentUsageContract())
}

func (r *StreamRunner) commitUsageTrackerForContract(tracker *UsageTracker, history []providers.ChatMessage, usageContract string) {
	if tracker == nil {
		return
	}
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	r.conversationUsage = tracker.Clone()
	r.trackedHistoryLen = len(history)
	r.trackedHistoryHash = hashMessagesForRequestShape(history)
	r.trackedHistoryTailHash = historyTailAnchor(history)
	r.trackedHistoryTailID = historyTailIdentity(history)
	r.trackedUsageContract = usageContract
}

func (r *StreamRunner) currentUsageContract() string {
	if r == nil {
		return ""
	}
	r.usageMu.Lock()
	providerObservationKey := r.ProviderObservationKey
	r.usageMu.Unlock()
	model := strings.TrimSpace(r.APIModel)
	if model == "" {
		model = r.Model
	}
	return usageContractKey(providerObservationKey, r.ProviderName, model, r.Variant, r.Effort, r.ProviderOptions)
}

func (r *StreamRunner) providerConnectionSnapshot() (providers.StreamClient, string) {
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	return r.Client, r.ProviderObservationKey
}

// UpdateProviderConnection atomically replaces the transport and its opaque
// observation identity for subsequent runs.
func (r *StreamRunner) UpdateProviderConnection(client providers.StreamClient, observationKey string) {
	if r == nil {
		return
	}
	r.usageMu.Lock()
	r.Client = client
	r.ProviderObservationKey = observationKey
	r.usageMu.Unlock()
}

func historyTailAnchor(history []providers.ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	return hashMessagesForRequestShape(history[len(history)-1:])
}

func historyTailIdentity(history []providers.ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	msg := history[len(history)-1]
	var b strings.Builder
	writeIdentity := func(kind, provider, model, id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		b.WriteString(kind)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(provider))
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(model))
		b.WriteByte(':')
		b.WriteString(id)
		b.WriteByte('\n')
	}
	writeIdentity("message", msg.ProviderItemProvider, msg.ProviderItemModel, msg.ProviderItemID)
	writeIdentity("tool_result", "", "", msg.ToolCallID)
	writeIdentity("tool_invocation", "", "", msg.ToolInvocationID)
	for _, call := range msg.ToolCalls {
		writeIdentity("tool_call", call.ProviderItemProvider, call.ProviderItemModel, call.ProviderItemID)
		writeIdentity("tool_call_id", "", "", call.ID)
	}
	for _, block := range msg.ReasoningBlocks {
		writeIdentity("reasoning_signature", "", "", block.Signature)
	}
	if b.Len() == 0 {
		return ""
	}
	return shortRequestShapeHash(b.String())
}

func findHistoryTailAnchor(history []providers.ChatMessage, anchor, identity string) int {
	if anchor == "" {
		return -1
	}
	for i := len(history) - 1; i >= 0; i-- {
		if hashMessagesForRequestShape(history[i:i+1]) != anchor {
			continue
		}
		if identity == "" || historyTailIdentity(history[i:i+1]) == identity {
			return i
		}
	}
	return -1
}

func canRebaseUsageAfterTail(history []providers.ChatMessage, anchor int, usage UsageBreakdown) bool {
	if usage.LastResponseTotal <= 0 || usage.PendingDelta != 0 || anchor < 0 || anchor >= len(history)-1 {
		return false
	}
	for _, msg := range history[anchor+1:] {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			return false
		}
	}
	return true
}

// ResetConversationUsage discards the persistent cross-turn usage baseline and
// re-seeds it from the supplied post-compaction history. It mirrors the
// explicit usage.Reset()+RecordPendingMessages the loop performs after an
// in-loop compaction (loop.go), but targets the runner-level baseline shared
// across turns.
//
// It exists for out-of-loop history rewrites
// compact, which replaces the parent thread's history from a completion wakeup
// without ever passing through the loop's own compaction path. Without this
// explicit invalidation the runner keeps the pre-compaction (inflated) ground
// truth until the length heuristic in prepareUsageTracker happens to fire,
// which reports a stale, over-large context size in the window between the
// rewrite and the next response. Seeding the pending delta from the new
// history makes EstimateCurrent reflect the compacted size immediately rather
// than dropping to zero.
func (r *StreamRunner) ResetConversationUsage(history []providers.ChatMessage) {
	// Retained request context owns its own durable-history fingerprint and is
	// deliberately not cleared here. A matching rewrite can reuse it; a real
	// rewrite/fork/edit is rejected by RetainedRequestContextState.validFor.
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	if r.conversationUsage == nil {
		r.conversationUsage = NewUsageTracker()
	}
	r.conversationUsage.Reset()
	r.conversationUsage.SetAdjustment(UsageAdjustmentCompactionRewriteEstimate)
	r.conversationUsage.RecordPendingMessages(history)
	r.trackedHistoryLen = len(history)
	r.trackedHistoryHash = hashMessagesForRequestShape(history)
	r.trackedHistoryTailHash = historyTailAnchor(history)
	r.trackedHistoryTailID = historyTailIdentity(history)
	model := strings.TrimSpace(r.APIModel)
	if model == "" {
		model = r.Model
	}
	r.trackedUsageContract = usageContractKey(r.ProviderObservationKey, r.ProviderName, model, r.Variant, r.Effort, r.ProviderOptions)
}

// SynchronizeConversationUsage reconciles a long-lived runner with the
// durable history refreshed under the app-server execution lease. Ordinary
// turn entry is a no-op and preserves live provider ground truth. If another
// owner appended or rewrote history, the persisted total becomes the new
// baseline; without one, the tracker falls back to a local estimate.
func (r *StreamRunner) SynchronizeConversationUsage(history []providers.ChatMessage, persistedTotal int) {
	if r == nil {
		return
	}
	historyHash := hashMessagesForRequestShape(history)
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	if r.conversationUsage == nil {
		r.conversationUsage = NewUsageTracker()
	}
	if r.trackedHistoryLen == len(history) &&
		(r.trackedHistoryHash == "" || r.trackedHistoryHash == historyHash) {
		r.trackedHistoryHash = historyHash
		r.trackedHistoryTailHash = historyTailAnchor(history)
		r.trackedHistoryTailID = historyTailIdentity(history)
		return
	}
	if len(history) >= r.trackedHistoryLen {
		usage := r.conversationUsage.Breakdown()
		anchor := findHistoryTailAnchor(history, r.trackedHistoryTailHash, r.trackedHistoryTailID)
		persistedMatches := persistedTotal <= 0 || persistedTotal == usage.LastResponseTotal
		if anchor == len(history)-1 && persistedMatches && usage.LastResponseTotal > 0 && usage.PendingDelta == 0 {
			r.conversationUsage.SetAdjustment(UsageAdjustmentRequestShapeTailRebase)
			r.conversationUsage.RecordPendingMessages(history[anchor+1:])
			r.trackedHistoryLen = len(history)
			r.trackedHistoryHash = historyHash
			r.trackedHistoryTailHash = historyTailAnchor(history)
			r.trackedHistoryTailID = historyTailIdentity(history)
			return
		}
	}
	r.conversationUsage.Reset()
	if persistedTotal > 0 {
		r.conversationUsage.SeedGroundTruth(persistedTotal)
		r.conversationUsage.SetAdjustment(UsageAdjustmentExternalRewriteSeed)
	} else {
		r.conversationUsage.SetAdjustment(UsageAdjustmentExternalRewriteEstimate)
		r.conversationUsage.RecordPendingMessages(history)
	}
	r.trackedHistoryLen = len(history)
	r.trackedHistoryHash = historyHash
	r.trackedHistoryTailHash = historyTailAnchor(history)
	r.trackedHistoryTailID = historyTailIdentity(history)
}

// SeedConversationUsageBaseline primes the cross-turn usage baseline from a
// persisted retained-context value (the ContextTokens of the thread's last
// completed turn) when a runtime is rebuilt over existing history — process
// restart, session resume, thread reopen. That persisted value derives from
// real provider usage, so it beats re-estimating the whole history with the
// pessimistic byte heuristic, which over-counts JSON-heavy histories enough
// to risk an immediate premature compaction on the first resumed turn. No-op
// when the tracker already holds live state (fresher than the persisted row)
// or when total is zero.
func (r *StreamRunner) SeedConversationUsageBaseline(total, historyLen int) {
	if r == nil || total <= 0 {
		return
	}
	r.usageMu.Lock()
	defer r.usageMu.Unlock()
	if r.conversationUsage == nil {
		r.conversationUsage = NewUsageTracker()
	}
	if r.conversationUsage.SeedGroundTruth(total) {
		r.conversationUsage.SetAdjustment(UsageAdjustmentRuntimeRebuildSeed)
		r.trackedHistoryLen = historyLen
		r.trackedHistoryHash = ""
		r.trackedHistoryTailHash = ""
		r.trackedHistoryTailID = ""
	}
}

func isNonDurableHistoryMessage(msg providers.ChatMessage) bool {
	return isTransientModelContextMessage(msg)
}

func (r *StreamRunner) proactiveCompactCircuitOpen() bool {
	r.compactMu.Lock()
	defer r.compactMu.Unlock()
	return r.proactiveCompactFailures >= maxConsecutiveProactiveCompactFailures
}

func (r *StreamRunner) recordCompactAttempt(info CompactAttemptInfo) {
	r.compactMu.Lock()
	defer r.compactMu.Unlock()

	if info.Status == CompactAttemptSucceeded {
		r.proactiveCompactFailures = 0
		return
	}
	if info.Reason == CompactReasonProactive && info.Status == CompactAttemptFailed {
		r.proactiveCompactFailures++
	}
}

func filterDurableHistory(msgs []providers.ChatMessage) []providers.ChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	// In-place filter: transient messages are rare, so the write-back cost is
	// negligible and we avoid a heap allocation.
	n := 0
	for _, msg := range msgs {
		if !isNonDurableHistoryMessage(msg) {
			msgs[n] = msg
			n++
		}
	}
	return msgs[:n]
}

func requestContextSummary(info RequestContextInfo) *providers.RequestContextSummary {
	return &providers.RequestContextSummary{
		StepIndex:                info.StepIndex,
		TransientMessages:        info.TransientMessages,
		ContentBytes:             info.ContentBytes,
		BlockKinds:               append([]string(nil), info.BlockKinds...),
		BlockKindCounts:          cloneStringIntMap(info.BlockKindCounts),
		BlockKindBytes:           cloneStringIntMap(info.BlockKindBytes),
		SegmentLifecycleCounts:   cloneStringIntMap(info.SegmentLifecycleCounts),
		SegmentPlacementCounts:   cloneStringIntMap(info.SegmentPlacementCounts),
		SegmentCachePolicyCounts: cloneStringIntMap(info.SegmentCachePolicyCounts),
		MessageCount:             info.MessageCount,
		SystemMessages:           info.SystemMessages,
		HiddenMessages:           info.HiddenMessages,
		ToolCount:                info.ToolCount,
		StablePrefix:             info.StablePrefix,
		TurnPrefix:               info.TurnPrefix,
		DynamicBytes:             info.DynamicBytes,
		SystemBytes:              info.SystemBytes,
		StablePrefixBytes:        info.StablePrefixBytes,
		TurnPrefixBytes:          info.TurnPrefixBytes,
		MessageBytes:             info.MessageBytes,
		ToolSchemaBytes:          info.ToolSchemaBytes,
		LoadableToolCount:        info.LoadableToolCount,
		LoadableToolSchemaBytes:  info.LoadableToolSchemaBytes,
		LoadableToolSurfaceHash:  info.LoadableToolSurfaceHash,
		SystemHash:               info.SystemHash,
		StablePrefixHash:         info.StablePrefixHash,
		TurnPrefixHash:           info.TurnPrefixHash,
		ToolSurfaceHash:          info.ToolSurfaceHash,
		PromptCacheKey:           info.PromptCacheKey,
		InputTokens:              info.InputTokens,
		OutputTokens:             info.OutputTokens,
		CacheCreationTokens:      info.CacheCreationTokens,
		CacheReadTokens:          info.CacheReadTokens,
		SystemSections:           systemPromptSectionSummaries(info.SystemSections),
	}
}

func cloneStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func systemPromptSectionSummaries(sections []SystemPromptSectionInfo) []providers.SystemPromptSectionSummary {
	if len(sections) == 0 {
		return nil
	}
	out := make([]providers.SystemPromptSectionSummary, 0, len(sections))
	for _, section := range sections {
		out = append(out, providers.SystemPromptSectionSummary{
			Key:    section.Key,
			Static: section.Static,
			Bytes:  section.Bytes,
			Hash:   section.Hash,
		})
	}
	return out
}

// streamStep adapts providers.StreamClient (with recovery) to the
// transport-agnostic Step interface. One Execute call opens an SSE
// stream and consumes it to completion. Recoverable failures create distinct
// execution attempts under the request operation's workload profile.
type streamStep struct {
	client  providers.StreamClient
	onEvent StreamCallback
	// Streaming tool execution: when set, read-only tools start
	// executing as soon as their arguments are fully received,
	// overlapping with continued model output.
	tools                   ToolExecutor
	toolLedger              *toolledger.Ledger
	enableStreamingToolExec bool
}

// NewStreamStep returns the shared stream-first model step used by agent loops.
// Providers with native streaming use StreamChat directly; unary-only providers
// are adapted to a synthetic stream by AdaptStreamClient.
func NewStreamStep(client providers.Client) Step {
	return &streamStep{client: providers.AdaptStreamClient(client)}
}

func (s *streamStep) Execute(ctx context.Context, req providers.ChatRequest) (StepResult, error) {
	var err error
	req, err = providers.EnsureInferenceExecutionContext(ctx, req, providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
	if err != nil {
		return StepResult{}, err
	}
	var (
		contentBuf        strings.Builder
		thinkingBuf       strings.Builder
		reasoningBlocks   []providers.ReasoningBlock
		pendingTools      = map[int]*providers.ToolCall{}
		usage             *providers.TokenUsage
		stopReason        string
		finishReason      providers.FinishReason
		truncated         bool
		messagePhase      providers.MessagePhase
		providerItemID    string
		providerItemModel string
	)

	// The runtime is swapped on stream retries while the reliable-stream
	// goroutine keeps calling the observer/guard closures, so every access
	// goes through the mutex-guarded accessors.
	var (
		toolRuntimeMu sync.Mutex
		toolRuntime   *TurnToolRuntime
	)
	newToolRuntime := func() *TurnToolRuntime {
		return NewTurnToolRuntime(ToolRuntimeConfig{
			Executor: s.tools, Ledger: s.toolLedger, OperationID: req.Operation.ID, StepIndex: req.StepIndex,
			RunContext: ctx,
		})
	}
	currentToolRuntime := func() *TurnToolRuntime {
		toolRuntimeMu.Lock()
		defer toolRuntimeMu.Unlock()
		return toolRuntime
	}
	if s.tools != nil && (s.enableStreamingToolExec || s.toolLedger != nil) {
		toolRuntime = newToolRuntime()
	}

	observeEvent := func(eventCtx context.Context, ev providers.StreamEvent) error {
		rt := currentToolRuntime()
		if rt == nil || !s.enableStreamingToolExec {
			return nil
		}
		return rt.ObserveStreamEvent(eventCtx, ev)
	}

	resetRuntime := func() {
		toolRuntimeMu.Lock()
		defer toolRuntimeMu.Unlock()
		if toolRuntime != nil {
			toolRuntime.Cancel()
			toolRuntime = newToolRuntime()
		}
	}
	replayGuard := func(_ providers.StreamRetryContext) error {
		rt := currentToolRuntime()
		if rt == nil {
			return nil
		}
		decision, err := rt.ReplayDecision(ctx)
		if err != nil {
			return err
		}
		if decision.Action == toolledger.ReplayBlock {
			return &toolledger.ReplayBlockedError{Decision: decision}
		}
		if decision.SupersedePartial {
			rt.mu.Lock()
			batchID := rt.batchID
			rt.mu.Unlock()
			if err := s.toolLedger.SupersedePreparedBatch(ctx, batchID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := s.runReliableStream(ctx, req, &contentBuf, &thinkingBuf, &reasoningBlocks, pendingTools, &messagePhase, &providerItemID, &providerItemModel, &usage, &stopReason, &finishReason, &truncated, resetRuntime, replayGuard, observeEvent); err != nil {
		if rt := currentToolRuntime(); rt != nil {
			rt.Cancel()
		}
		failure := providers.NormalizeFailure(err)
		outcome := providers.InferenceOutcomeFailed
		if failure.Category == providers.FailureCanceled || failure.Category == providers.FailureDeadline {
			outcome = providers.InferenceOutcomeCanceled
		}
		partialToolCalls := make([]providers.ToolCall, 0, len(pendingTools))
		for i := 0; i < len(pendingTools); i++ {
			if tc, ok := pendingTools[i]; ok {
				partialToolCalls = append(partialToolCalls, *tc)
			}
		}
		partialToolRuntime := currentToolRuntime()
		if partialToolRuntime != nil {
			partialToolRuntime.Cancel()
		}
		partial := StepResult{
			Content:          contentBuf.String(),
			Phase:            messagePhase,
			ProviderItemID:   providerItemID,
			ReasoningContent: thinkingBuf.String(),
			ReasoningBlocks:  cloneReasoningBlocks(reasoningBlocks),
			ToolCalls:        partialToolCalls,
			ToolRuntime:      partialToolRuntime,
		}
		if journalErr := req.Execution.Complete(outcome, failure); journalErr != nil {
			return partial, errors.Join(fmt.Errorf("stream request failed: %w", err), fmt.Errorf("complete failed inference operation: %w", journalErr))
		}
		return partial, fmt.Errorf("stream request failed: %w", err)
	}
	// Build ordered tool calls list from the pending map.
	toolCalls := make([]providers.ToolCall, 0, len(pendingTools))
	for i := 0; i < len(pendingTools); i++ {
		if tc, ok := pendingTools[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	if finishReason == "" {
		finishReason = providers.NormalizeFinishReason(stopReason, truncated, len(toolCalls) > 0)
	}

	// Non-streaming fallback: when the stream completed without producing any
	// visible content or tool calls AND the provider did not send any terminal
	// reason, the stream was likely broken by a proxy or compatibility issue.
	// A terminal length/max_tokens reason is a completed model response, not a
	// broken stream, even when the visible text is empty.
	if strings.TrimSpace(contentBuf.String()) == "" && len(toolCalls) == 0 && strings.TrimSpace(stopReason) == "" && finishReason == "" && !truncated {
		if rt := currentToolRuntime(); rt != nil {
			rt.Cancel()
		}
		providers.DebugLogf("stream returned empty content with stop_reason=%q, attempting non-streaming fallback", stopReason)
		if s.onEvent != nil {
			s.onEvent(providers.StreamEvent{
				Type:    providers.EventReconnect,
				Content: "Empty stream response — trying non-streaming fallback...",
			})
		}
		completedAttempt := req.Execution.LatestAttempt()
		nextAttempt, err := completedAttempt.PrepareRecoveryAttempt(ctx, providers.RecoveryPlan{
			Action: providers.RecoverySwitchTransport,
			Reason: "empty streaming response",
		}, time.Time{})
		if err != nil {
			return StepResult{}, fmt.Errorf("record non-streaming fallback: %w", err)
		}
		req.Attempt = nextAttempt
		resp, err := providers.ExecuteChat(ctx, s.client, req, req.Operation.Kind, req.Operation.WorkloadProfile)
		if err != nil {
			return StepResult{}, fmt.Errorf("non-streaming fallback failed: %w", err)
		}
		fbToolCalls := make([]providers.ToolCall, len(resp.ToolCalls))
		copy(fbToolCalls, resp.ToolCalls)
		for i := range fbToolCalls {
			if strings.TrimSpace(fbToolCalls[i].ProviderItemID) != "" {
				fbToolCalls[i].ProviderItemProvider = req.Provider
			}
		}
		enrichToolCallsDisplay(s.tools, fbToolCalls)
		// Emit the fallback content through the streaming callback so
		// live clients can render it.
		if s.onEvent != nil && strings.TrimSpace(resp.Content) != "" {
			s.onEvent(providers.StreamEvent{
				Type:    providers.EventContentDelta,
				Content: resp.Content,
				Phase:   resp.Phase,
			})
			s.onEvent(providers.StreamEvent{
				Type:         providers.EventDone,
				Usage:        resp.Usage,
				StopReason:   resp.StopReason,
				FinishReason: normalizedChatResponseFinish(resp),
				Truncated:    resp.Truncated,
			})
		}
		fbFinishReason := normalizedChatResponseFinish(resp)
		fbProvider := providerNameForNativeState(req.Provider, resp.ProviderItemID, resp.ReasoningContent, resp.ReasoningBlocks, fbToolCalls)
		fbModel := resp.ProviderItemModel
		if fbProvider != "" && strings.TrimSpace(fbModel) == "" {
			fbModel = req.Model
		}
		return StepResult{
			Content:              resp.Content,
			Phase:                resp.Phase,
			ProviderItemID:       resp.ProviderItemID,
			ProviderItemProvider: fbProvider,
			ProviderItemModel:    fbModel,
			ReasoningContent:     resp.ReasoningContent,
			ReasoningBlocks:      cloneReasoningBlocks(resp.ReasoningBlocks),
			ToolCalls:            fbToolCalls,
			Usage:                resp.Usage,
			FinishReason:         fbFinishReason,
			StopReason:           resp.StopReason,
			Truncated:            resp.Truncated,
		}, nil
	}
	finalToolRuntime := currentToolRuntime()
	if len(toolCalls) == 0 && finalToolRuntime != nil {
		finalToolRuntime.Cancel()
		finalToolRuntime = nil
	}
	if err := req.Execution.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
		return StepResult{}, fmt.Errorf("complete inference operation: %w", err)
	}

	providerItemProvider := providerNameForNativeState(req.Provider, providerItemID, thinkingBuf.String(), reasoningBlocks, toolCalls)
	if providerItemProvider != "" && strings.TrimSpace(providerItemModel) == "" {
		providerItemModel = req.Model
	}
	return StepResult{
		Content:              contentBuf.String(),
		Phase:                messagePhase,
		ProviderItemID:       providerItemID,
		ProviderItemProvider: providerItemProvider,
		ProviderItemModel:    providerItemModel,
		ReasoningContent:     thinkingBuf.String(),
		ReasoningBlocks:      cloneReasoningBlocks(reasoningBlocks),
		ToolCalls:            toolCalls,
		Usage:                usage,
		FinishReason:         finishReason,
		StopReason:           stopReason,
		Truncated:            truncated,
		ToolRuntime:          finalToolRuntime,
	}, nil
}

func providerNameForNativeState(providerName, providerItemID, reasoningContent string, reasoningBlocks []providers.ReasoningBlock, toolCalls []providers.ToolCall) string {
	hasNativeState := strings.TrimSpace(providerItemID) != "" || strings.TrimSpace(reasoningContent) != "" || len(reasoningBlocks) > 0
	if !hasNativeState {
		for _, call := range toolCalls {
			if strings.TrimSpace(call.ProviderItemID) != "" {
				hasNativeState = true
				break
			}
		}
	}
	if !hasNativeState {
		return ""
	}
	return strings.TrimSpace(providerName)
}

func normalizedChatResponseFinish(resp providers.ChatResponse) providers.FinishReason {
	if resp.FinishReason != "" {
		return resp.FinishReason
	}
	return providers.NormalizeFinishReason(resp.StopReason, resp.Truncated, len(resp.ToolCalls) > 0)
}

func truncateLog(s string, maxLen int) string {
	return stringutil.Truncate(s, maxLen, "...")
}

// formatCompactNotice produces the human-readable string surfaced via
// EventCompact. Kept short — it shows up as a single system line in
// the chat viewport.
func formatCompactNotice(info CompactInfo) string {
	verb := "Compacted"
	switch info.Reason {
	case CompactReasonOverflow:
		verb = "Recovered from context overflow — compacted"
	case CompactReasonManual:
		verb = "Manually compacted"
	case CompactReasonNewContext:
		verb = "Started a fresh context window with"
	}
	if info.TokensBefore > 0 && info.TokensAfter > 0 {
		return fmt.Sprintf("✦ %s history: %d → %d messages (~%s → ~%s tokens)",
			verb, info.MessagesBefore, info.MessagesAfter,
			formatTokenCount(info.TokensBefore), formatTokenCount(info.TokensAfter))
	}
	if info.TokensBefore > 0 {
		return fmt.Sprintf("✦ %s history: %d → %d messages (was ~%s tokens)",
			verb, info.MessagesBefore, info.MessagesAfter,
			formatTokenCount(info.TokensBefore))
	}
	return fmt.Sprintf("✦ %s history: %d → %d messages",
		verb, info.MessagesBefore, info.MessagesAfter)
}

func formatCompactAttemptNotice(info CompactAttemptInfo) (string, bool) {
	switch {
	case info.Status == CompactAttemptFailed && info.OutputLimit:
		return "Context compaction still exceeded the model's summary output limit after retry; history is unchanged. Retry compaction or switch to a model with a larger output limit.", true
	case info.Reason == CompactReasonProactive && info.Status == CompactAttemptFailed:
		return "Context compaction failed; continuing without compacting history.", true
	case info.Reason == CompactReasonOverflow && info.Status == CompactAttemptFailed:
		return "Context-overflow compact failed; history is unchanged.", true
	case info.Reason == CompactReasonNewContext && info.Status == CompactAttemptFailed:
		return "Fresh context could not be installed; active history is unchanged.", true
	// A user-requested /compact must report its outcome even when nothing
	// happened — silence would read as a successful compaction.
	case info.Reason == CompactReasonManual && info.Status == CompactAttemptFailed:
		return "Manual context compaction failed; history is unchanged.", true
	case info.Reason == CompactReasonManual && info.Status == CompactAttemptUnchanged:
		return "Nothing to compact yet; history is unchanged.", true
	case info.Status == CompactAttemptUnchanged:
		return "Nothing to compact yet; history is unchanged.", true
	}
	return "", false
}

// formatTokenCount renders a token count in a compact form: 1234 →
// "1.2k", 12_345 → "12k", 1_234_567 → "1.2M".
func formatTokenCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

func (s *streamStep) runReliableStream(
	ctx context.Context,
	req providers.ChatRequest,
	contentBuf *strings.Builder,
	thinkingBuf *strings.Builder,
	reasoningBlocks *[]providers.ReasoningBlock,
	pendingTools map[int]*providers.ToolCall,
	messagePhase *providers.MessagePhase,
	providerItemID *string,
	providerItemModel *string,
	usage **providers.TokenUsage,
	stopReason *string,
	finishReason *providers.FinishReason,
	truncated *bool,
	onAttemptStart func(),
	replayGuard providers.StreamReplayGuard,
	eventObserver providers.StreamEventObserver,
) error {
	onEvent := s.onEvent

	resetPartialOutput := func() {
		hadContent := contentBuf.Len() > 0
		hadThinking := thinkingBuf.Len() > 0
		if !hadContent && !hadThinking && len(*reasoningBlocks) == 0 {
			return
		}
		contentBuf.Reset()
		thinkingBuf.Reset()
		*reasoningBlocks = nil
		*messagePhase = ""
		*providerItemID = ""
		*providerItemModel = ""
		*usage = nil
		*stopReason = ""
		*finishReason = ""
		*truncated = false
		if onEvent == nil {
			return
		}
		if hadContent {
			onEvent(providers.StreamEvent{
				Type: providers.EventContentReplace,
			})
		}
		if hadThinking {
			onEvent(providers.StreamEvent{
				Type: providers.EventThinkingReplace,
			})
		}
	}

	onRetry := func(attempt, maxRetries int, err error, delay time.Duration) {
		providers.DebugLogf("stream reconnecting (%d/%d) in %s: %v",
			attempt, maxRetries, delay, err)
		// Log full error details for post-mortem analysis.
		var httpErr *providers.HTTPError
		if errors.As(err, &httpErr) {
			providers.DebugLogf("  HTTP %d body: %s", httpErr.StatusCode, httpErr.Body)
		}
		var streamErr *providers.StreamError
		if errors.As(err, &streamErr) {
			providers.DebugLogf("  stream error code=%s msg=%s", streamErr.Code, streamErr.Message)
		}
		if onEvent != nil {
			onEvent(providers.StreamEvent{
				Type:    providers.EventReconnect,
				Content: fmt.Sprintf("Reconnecting... %d/%d", attempt, maxRetries),
			})
		}
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// A failed attempt may have accumulated partial tool calls that
		// never received a matching EventToolUseEnd. Those incomplete
		// JSON arguments would leak into the final StepResult and later
		// cause mapMessage to fail when the message is replayed as
		// history. Clear the map before starting a fresh provider attempt.
		for k := range pendingTools {
			delete(pendingTools, k)
		}
		if onAttemptStart != nil {
			onAttemptStart()
		}

		client := providers.NewReliableStreamClient(
			s.client,
			onRetry,
			providers.WithStreamReplayGuard(replayGuard),
			providers.WithStreamEventObserver(eventObserver),
		)
		ch, err := client.StreamChat(ctx, req)
		if err != nil {
			return err
		}

		var (
			streamErr error
			sawDone   bool
		)

		for event := range ch {
			switch event.Type {
			case providers.EventContentDelta:
				if event.Phase != "" {
					*messagePhase = event.Phase
				}
				if strings.TrimSpace(event.ProviderItemID) != "" {
					*providerItemID = event.ProviderItemID
					*providerItemModel = req.Model
				}
				contentBuf.WriteString(event.Content)

			case providers.EventContentReplace:
				if event.Phase != "" {
					*messagePhase = event.Phase
				}
				if strings.TrimSpace(event.ProviderItemID) != "" {
					*providerItemID = event.ProviderItemID
					*providerItemModel = req.Model
				}
				contentBuf.Reset()
				contentBuf.WriteString(event.Content)

			case providers.EventThinkingDelta:
				thinkingBuf.WriteString(event.Content)

			case providers.EventThinkingDone:
				if event.ReasoningBlock != nil {
					*reasoningBlocks = append(*reasoningBlocks, *event.ReasoningBlock)
				}

			case providers.EventToolUseStart:
				if event.ToolCall != nil {
					if strings.TrimSpace(event.ToolCall.ProviderItemID) != "" {
						event.ToolCall.ProviderItemProvider = req.Provider
						event.ToolCall.ProviderItemModel = req.Model
					}
					idx := len(pendingTools)
					toolCall := enrichToolCallDisplay(s.tools, providers.ToolCall{
						ID:                   event.ToolCall.ID,
						ProviderItemID:       event.ToolCall.ProviderItemID,
						ProviderItemProvider: event.ToolCall.ProviderItemProvider,
						ProviderItemModel:    event.ToolCall.ProviderItemModel,
						Name:                 event.ToolCall.Name,
						Kind:                 event.ToolCall.Kind,
					})
					pendingTools[idx] = &toolCall
					event.ToolCall = &toolCall
				}

			case providers.EventToolUseDelta:
				if len(pendingTools) > 0 {
					latest := pendingTools[len(pendingTools)-1]
					latest.Arguments += event.Content
				}

			case providers.EventToolUseEnd:
				if event.ToolCall != nil {
					if strings.TrimSpace(event.ToolCall.ProviderItemID) != "" {
						event.ToolCall.ProviderItemProvider = req.Provider
						event.ToolCall.ProviderItemModel = req.Model
					}
					toolCall := enrichToolCallDisplay(s.tools, *event.ToolCall)
					for _, tc := range pendingTools {
						if tc.ID == toolCall.ID {
							if strings.TrimSpace(toolCall.ProviderItemID) != "" {
								tc.ProviderItemID = toolCall.ProviderItemID
								tc.ProviderItemProvider = toolCall.ProviderItemProvider
								tc.ProviderItemModel = toolCall.ProviderItemModel
							}
							if strings.TrimSpace(toolCall.Name) != "" {
								tc.Name = toolCall.Name
							}
							if toolCall.Arguments != "" {
								tc.Arguments = toolCall.Arguments
							}
							if toolCall.Kind != "" {
								tc.Kind = toolCall.Kind
							}
							if toolCall.Display != nil {
								tc.Display = toolCall.Display
							}
							break
						}
					}
					event.ToolCall = &toolCall
				}

			case providers.EventUsage:
				if event.Usage != nil {
					*usage = event.Usage
				}

			case providers.EventProviderState:
				if event.ProviderState != nil {
					event.ProviderState.StepIndex = req.StepIndex
				}

			case providers.EventLifecycle:
				if event.Lifecycle != nil && event.Lifecycle.Phase == providers.StreamPhaseReconnecting && event.Lifecycle.ResetPartial {
					resetPartialOutput()
					for k := range pendingTools {
						delete(pendingTools, k)
					}
					if onAttemptStart != nil {
						onAttemptStart()
					}
				}

			case providers.EventError:
				if event.Error != nil {
					streamErr = event.Error
				} else {
					streamErr = errors.New("stream error")
				}
				continue

			case providers.EventDone:
				sawDone = true
				if event.Usage != nil {
					*usage = event.Usage
				}
				if event.StopReason != "" {
					*stopReason = event.StopReason
				}
				if event.FinishReason != "" {
					*finishReason = event.FinishReason
				}
				if event.Truncated {
					*truncated = true
				}
			}

			if onEvent != nil {
				onEvent(event)
			}
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if streamErr == nil && !sawDone {
			streamErr = providers.NewIncompleteStreamError("stream closed before done")
		}
		if streamErr == nil {
			return nil
		}

		if onEvent != nil && !providers.IsContextOverflow(streamErr) {
			onEvent(providers.StreamEvent{
				Type:  providers.EventError,
				Error: streamErr,
			})
		}
		return streamErr
	}
}

func enrichToolCallsDisplay(executor ToolExecutor, calls []providers.ToolCall) {
	for i := range calls {
		calls[i] = enrichToolCallDisplay(executor, calls[i])
	}
}

func enrichToolCallDisplay(executor ToolExecutor, call providers.ToolCall) providers.ToolCall {
	if call.Display != nil {
		return call
	}
	displayProvider, ok := executor.(ToolDisplayProvider)
	if !ok {
		return call
	}
	display, ok := displayProvider.ToolDisplay(call)
	if !ok || (strings.TrimSpace(display.Text) == "" && strings.TrimSpace(display.Label) == "") {
		return call
	}
	call.Display = display.Clone()
	return call
}
