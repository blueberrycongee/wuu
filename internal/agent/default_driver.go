package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// DefaultDriver owns the multi-round algorithm. All execution effects go through
// the same turn resources exposed to process drivers.
type DefaultDriver struct{}

func (DefaultDriver) Descriptor(ctx context.Context) (loopdriver.Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return loopdriver.Descriptor{}, err
	}
	return loopdriver.Descriptor{ID: loopdriver.DefaultDriverID, Version: loopdriver.DefaultDriverVersion, Capabilities: []string{"multi_round", "tool_use", "compaction"}}, nil
}

func (d DefaultDriver) Create(ctx context.Context, execution loopdriver.ExecutionContext, input loopdriver.PersistedInput) (loopdriver.Instance, error) {
	descriptor, err := d.Descriptor(ctx)
	if err != nil {
		return nil, err
	}
	return &defaultDriverInstance{descriptor: descriptor, execution: execution}, nil
}

func (d DefaultDriver) Resume(ctx context.Context, execution loopdriver.ExecutionContext, input loopdriver.PersistedInput, checkpoint loopdriver.Checkpoint) (loopdriver.Instance, error) {
	instance, err := d.Create(ctx, execution, input)
	if err != nil {
		return nil, err
	}
	i := instance.(*defaultDriverInstance)
	if checkpoint.ContractVersion != loopdriver.ContractVersion || checkpoint.DriverID != i.descriptor.ID || checkpoint.DriverVersion != i.descriptor.Version {
		return nil, errors.New("incompatible default loop checkpoint")
	}
	if len(checkpoint.State) > 0 {
		if err := json.Unmarshal(checkpoint.State, &i.state); err != nil {
			return nil, fmt.Errorf("decode default loop checkpoint: %w", err)
		}
	}
	return i, nil
}

type defaultDriverState struct {
	Phase         string `json:"phase"`
	ExecutionID   string `json:"execution_id,omitempty"`
	LastReceiptID string `json:"last_receipt_id,omitempty"`
}

type defaultDriverInstance struct {
	descriptor loopdriver.Descriptor
	execution  loopdriver.ExecutionContext
	mu         sync.Mutex
	cancel     context.CancelCauseFunc
	state      defaultDriverState
}

func (i *defaultDriverInstance) Run(ctx context.Context, gateway loopdriver.KernelGateway) (loopdriver.TerminalOutcome, error) {
	turn, ok := gateway.(loopdriver.TurnGateway)
	if !ok {
		return loopdriver.TerminalOutcome{Status: loopdriver.TerminalFailed}, errors.New("default loop requires independent turn resources")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	i.mu.Lock()
	i.cancel = cancel
	i.state.Phase, i.state.ExecutionID = "running", i.execution.ExecutionID
	i.mu.Unlock()
	defer cancel(nil)
	if err := gateway.WriteCheckpoint(ctx, i.Checkpoint()); err != nil {
		return loopdriver.TerminalOutcome{Status: loopdriver.TerminalFailed, Checkpoint: i.Checkpoint()}, err
	}
	request, err := turn.ReadTurn(ctx)
	if err != nil {
		return loopdriver.TerminalOutcome{Status: loopdriver.TerminalFailed, Checkpoint: i.Checkpoint()}, err
	}
	settings, err := turn.ReadSettings(ctx)
	if err != nil {
		return loopdriver.TerminalOutcome{Status: loopdriver.TerminalFailed, Checkpoint: i.Checkpoint()}, err
	}
	contextState, err := turn.ReadContextState(ctx)
	if err != nil {
		return loopdriver.TerminalOutcome{Status: loopdriver.TerminalFailed, Checkpoint: i.Checkpoint()}, err
	}
	step := &driverResourceStep{turn: turn, definitions: request.Tools, streaming: settings.StreamingTools}
	cfg := LoopConfig{
		Model: request.Model, ProviderName: request.Provider, Temperature: request.Temperature,
		MediaInput: request.MediaInput, Effort: request.Effort, ProviderOptions: request.ProviderOptions,
		NativeDeferredToolDiscovery: request.NativeDeferredToolDiscovery, ForceToolFirstStep: request.ForceToolName,
		SessionID: i.execution.SessionID, ExecutionID: i.execution.ExecutionID,
		DriverID: i.descriptor.ID, DriverVersion: i.descriptor.Version,
		RetainedRequestContext: retainedContextFromWire(contextState.Retained),
		UsageTracker:           usageFromWire(contextState.Usage),
		MaxSteps:               settings.MaxSteps, ProviderObservationKey: settings.ProviderObservationKey, ModelVariant: settings.ModelVariant,
		MaxContextTokens: settings.ContextTokens, MaxInputTokens: settings.InputTokens,
		OutputReserveTokens: settings.OutputReserveTokens, CompactThresholdTokens: settings.CompactThresholdTokens,
		CompactThresholdPct: settings.CompactThresholdPercent, CompactKeepRecentTokens: settings.CompactKeepRecentTokens,
		ForceInitialCompact: settings.ForceInitialCompact, CompactOnly: settings.CompactOnly,
	}
	cfg.RequestContextStore = driverResourceContextStore{turn: turn, identity: RequestContextCheckpoint{Provider: request.Provider, Model: request.Model, DriverID: i.descriptor.ID, DriverVersion: i.descriptor.Version}}
	if settings.DisableAutoCompact {
		cfg.MaxContextTokens, cfg.CompactThresholdTokens = 0, 0
	}
	for _, section := range settings.SystemSections {
		cfg.SystemPromptSections = append(cfg.SystemPromptSections, SystemPromptSectionInfo{Key: section.Key, Static: section.Static, Bytes: section.Bytes, Hash: section.Hash})
	}
	if settings.CanCompact {
		cfg.Compact = func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			hint, _ := compactBudgetHintFromContext(ctx)
			return turn.CompactContext(ctx, messages, pluginprotocol.DriverCompactBudget{Reason: string(hint.Reason), LastSuccessfulTokens: hint.LastSuccessfulTokens, FailedRequestTokens: hint.FailedRequestTokens, TargetTotalTokens: hint.TargetTotalTokens})
		}
	}
	if len(request.Tools) > 0 {
		cfg.Tools = driverResourceTools{step}
	}
	if request.CacheHint != nil {
		cfg.PromptCacheKey = request.CacheHint.PromptCacheKey
	}
	cfg.BeforeModelStep = func(ctx context.Context, _ int, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
		admitted, err := turn.AdmitInput(ctx)
		if err != nil {
			return nil, err
		}
		current, err := turn.ReadTurn(ctx)
		if err != nil {
			return nil, err
		}
		step.definitions = current.Tools
		return admitted.Messages, nil
	}
	cfg.BeforeRequestContext = func() []ContextSegment {
		segments, err := turn.ReadRequestContext(ctx)
		if err != nil {
			cancel(err)
		}
		return segments
	}
	cfg.BeforeRequest = func(ctx context.Context, request *providers.ChatRequest) error {
		transformed, err := turn.TransformRequest(ctx, *request)
		if err == nil {
			*request = transformed
		}
		return err
	}
	replaceContext := func(ctx context.Context, messages []providers.ChatMessage) (loopdriver.TurnContext, error) {
		current, err := turn.ReadContext(ctx)
		if err != nil {
			return loopdriver.TurnContext{}, err
		}
		return turn.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: messages, Volatile: !current.CanReplace})
	}
	cfg.AcceptContext = func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
		committed, err := replaceContext(ctx, messages)
		return committed.Messages, err
	}
	if settings.CanArchive {
		cfg.ArchiveHistory = func(ctx context.Context, messages []providers.ChatMessage) (HistoryArchive, error) {
			archive, err := turn.ArchiveContext(ctx)
			if err != nil {
				return HistoryArchive{}, err
			}
			seqs := make([]int, len(archive.Messages))
			for index, message := range archive.Messages {
				seqs[index] = message.Seq
			}
			addressed := applyArchivedHistorySeqs(messages, archive.Messages, seqs)
			result := HistoryArchive{HeadSeq: archive.Head, Seqs: make([]int, len(addressed))}
			for index, message := range addressed {
				result.Seqs[index] = message.Seq
			}
			return result, nil
		}
	}
	if settings.FreshContextTokens > 0 && settings.CanArchive {
		cfg.FreshContextTokens = settings.FreshContextTokens
		cfg.FreshContext = func(ctx context.Context, messages []providers.ChatMessage, head, fixed, target int) ([]providers.ChatMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return buildFreshContext(messages, head, fixed, target, request.MediaInput)
		}
		cfg.AcceptFreshContext = func(ctx context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
			committed, err := replaceContext(ctx, messages)
			return committed.Messages, committed.ArchiveHead, err
		}
		cfg.BeforeRequestContext = withContextWindowGuidance(cfg.BeforeRequestContext)
	}
	report := func(observation pluginprotocol.DriverTurnObservation) {
		if err := turn.ReportTurn(ctx, observation); err != nil {
			cancel(err)
		}
	}
	cfg.OnRequestContext = func(info RequestContextInfo) {
		report(pluginprotocol.DriverTurnObservation{Kind: "request_context", RequestContext: requestContextSummary(info)})
	}
	cfg.BeforeCompact = func(ctx context.Context, reason CompactReason) error {
		return turn.ReportTurn(ctx, pluginprotocol.DriverTurnObservation{Kind: "before_compact", Compact: &pluginprotocol.DriverCompactObservation{Reason: string(reason)}})
	}
	cfg.AfterCompact = func(ctx context.Context, reason CompactReason, compactErr error) error {
		observation := &pluginprotocol.DriverCompactObservation{Reason: string(reason), Failure: loopdriver.FailureToWire(compactErr)}
		return turn.ReportTurn(ctx, pluginprotocol.DriverTurnObservation{Kind: "after_compact", Compact: observation})
	}
	cfg.OnCompactStart = func(reason CompactReason) {
		report(pluginprotocol.DriverTurnObservation{Kind: "compact_start", Compact: &pluginprotocol.DriverCompactObservation{Reason: string(reason)}})
	}
	cfg.OnCompact = func(info CompactInfo) {
		report(pluginprotocol.DriverTurnObservation{Kind: "compact_result", Compact: &pluginprotocol.DriverCompactObservation{Reason: string(info.Reason), TokensBefore: info.TokensBefore, TokensAfter: info.TokensAfter, MessagesBefore: info.MessagesBefore, MessagesAfter: info.MessagesAfter, Summary: info.Summary}})
	}
	cfg.OnCompactAttempt = func(info CompactAttemptInfo) {
		report(pluginprotocol.DriverTurnObservation{Kind: "compact_attempt", Compact: &pluginprotocol.DriverCompactObservation{Reason: string(info.Reason), Status: string(info.Status), TokensBefore: info.TokensBefore, LastResponseTotal: info.LastResponseTotal, PendingDelta: info.PendingDelta, UsageAdjustment: string(info.UsageAdjustment), MessagesBefore: info.MessagesBefore, MessagesAfter: info.MessagesAfter, Error: info.Error, OutputLimit: info.OutputLimit}})
	}
	result, runErr := RunToolLoop(ctx, request.Messages, cfg, step)
	if ctx.Err() == nil {
		runErr = errors.Join(runErr, turn.SaveContextState(ctx, pluginprotocol.DriverContextState{Retained: retainedContextToWire(result.RetainedRequestContext), Usage: usageToWire(cfg.UsageTracker)}))
	}
	var receipt loopdriver.TurnReceipt
	if runErr == nil {
		receipt, runErr = turn.CompleteTurn(ctx, loopdriver.TurnResult{Content: result.Content, FinishReason: result.FinishReason, StopReason: result.StopReason, Truncated: result.Truncated})
	}
	status := loopdriver.TerminalSucceeded
	if runErr != nil {
		status = loopdriver.TerminalFailed
		if ctx.Err() != nil {
			status = loopdriver.TerminalCanceled
			runErr = errors.Join(runErr, context.Cause(ctx))
		}
	}
	i.mu.Lock()
	i.state.Phase, i.state.LastReceiptID = string(status), receipt.ID
	i.mu.Unlock()
	return loopdriver.TerminalOutcome{Status: status, ReceiptID: receipt.ID, Checkpoint: i.Checkpoint()}, runErr
}

func (i *defaultDriverInstance) Checkpoint() loopdriver.Checkpoint {
	i.mu.Lock()
	defer i.mu.Unlock()
	state, _ := json.Marshal(i.state)
	return loopdriver.Checkpoint{ContractVersion: loopdriver.ContractVersion, DriverID: i.descriptor.ID, DriverVersion: i.descriptor.Version, State: state}
}

func (i *defaultDriverInstance) Cancel(reason string) {
	i.mu.Lock()
	cancel := i.cancel
	i.mu.Unlock()
	if cancel != nil {
		cancel(errors.New(reason))
	}
}

func (i *defaultDriverInstance) Shutdown() { i.Cancel("driver shutdown") }

type driverResourceStep struct {
	turn        loopdriver.TurnGateway
	definitions []providers.ToolDefinition
	batch       int
	streaming   bool
}

func (s *driverResourceStep) Definitions() []providers.ToolDefinition { return s.definitions }

type driverResourceTools struct{ *driverResourceStep }

// The ToolExecutor interface requires Execute; RunToolLoop joins the resource
// batch instead. Individual calls cannot bypass the execution receipt.
func (s driverResourceTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	return "", errors.New("tools require a turn batch")
}

func (s *driverResourceStep) Execute(ctx context.Context, request providers.ChatRequest) (StepResult, error) {
	s.batch++
	batchID := fmt.Sprintf("round-%d", s.batch)
	var round loopdriver.ModelRound
	var err error
	if s.streaming && len(s.definitions) > 0 {
		round, err = s.turn.CallModelWithTools(ctx, request, batchID)
	} else {
		round, err = s.turn.CallModel(ctx, request)
	}
	result := StepResult{Usage: round.Usage}
	for _, message := range round.Messages {
		if message.Role != "assistant" {
			continue
		}
		result.Content = message.Content
		result.Images = message.Images
		result.ProviderItems = message.ProviderItems
		result.Phase = message.Phase
		result.ProviderItemID = message.ProviderItemID
		result.ProviderItemProvider = message.ProviderItemProvider
		result.ProviderItemModel = message.ProviderItemModel
		result.ReasoningContent = message.ReasoningContent
		result.ReasoningBlocks = message.ReasoningBlocks
		result.ToolCalls = message.ToolCalls
		result.FinishReason = message.FinishReason
		result.StopReason = message.StopReason
		result.Truncated = message.Truncated
	}
	if round.ToolBatchID != "" {
		batchID = round.ToolBatchID
	}
	result.ToolRuntime = &driverResourceBatch{turn: s.turn, id: batchID, accepted: round.ToolWorkStarted}
	return result, err
}

type driverResourceBatch struct {
	turn     loopdriver.TurnGateway
	id       string
	accepted bool
}

func (b *driverResourceBatch) HasAcceptedCalls() bool                       { return b.accepted }
func (b *driverResourceBatch) TakeRequestContextSegments() []ContextSegment { return nil }

// The resource owns step identity and live callbacks; joining only collects its
// already emitted results. These callback parameters belong to the shared local
// batch interface and must not emit the same service events a second time.
func (b *driverResourceBatch) Join(ctx context.Context, calls []providers.ToolCall, stepIndex int, onDetail func(providers.ToolCall, toolresult.Result), onResult func(providers.ToolCall, string), onRejected func(ToolBatchRejectionInfo)) ([]providers.ChatMessage, error) {
	return b.turn.CallTools(ctx, loopdriver.ToolBatch{ID: b.id, Calls: calls, Messages: HistoryFromContext(ctx)})
}
