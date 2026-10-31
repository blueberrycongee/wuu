package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ModelStepBinding is an immutable, validated model-only snapshot. The owner
// retains its generation for the execution. It cannot replace tools, permission
// policy, storage, or execution scope. Revisions are durable selection intents,
// not caller-controlled provider routing or permission grants.
type ModelStepBinding struct {
	Revision                                                                      uint64
	Client                                                                        providers.StreamClient
	Provider, Model, APIModel                                                     string
	ProviderObservationKey                                                        string
	Variant, Effort                                                               string
	ProviderOptions                                                               map[string]any
	MediaInput                                                                    providers.MediaInputPolicy
	NativeDeferredToolDiscovery                                                   bool
	SystemPrompt                                                                  string
	SystemPromptSections                                                          []SystemPromptSectionInfo
	ContextWindowTokens, InputTokens, OutputReserveTokens, CompactThresholdTokens int
	BeforeModelStep                                                               func(context.Context, int, []providers.ChatMessage) ([]providers.ChatMessage, error)
	StepPolicy                                                                    func(context.Context, int, []providers.ChatMessage) (loopdriver.StepDecision, error)
	BeforeRequest                                                                 func(context.Context, *providers.ChatRequest) error
	snapshot                                                                      *StreamRunner
}

func (g *streamTurnGateway) modelRevision() uint64 {
	if g.modelBinding != nil {
		return g.modelBinding.Revision
	}
	return 0
}

func (g *streamTurnGateway) modelRunner() *StreamRunner {
	if g.modelBinding != nil {
		return g.modelBinding.snapshot
	}
	return g.runner
}

func (g *streamTurnGateway) bindModelStep(ctx context.Context) error {
	if !g.modelBoundaryPending || g.runner.ResolveModelStep == nil {
		return nil
	}
	binding, err := g.runner.ResolveModelStep(ctx, g.modelRevision())
	if err != nil {
		return fmt.Errorf("resolve next-step model: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if binding == nil || binding.Revision == g.modelRevision() {
		g.modelBoundaryPending = false
		return nil
	}
	if g.descriptor.ID != "" && !slices.Contains(g.descriptor.Capabilities, "step_model_selection") {
		return errors.New("selected loop driver does not support next-step model selection")
	}
	if binding.Revision < g.modelRevision() || binding.Client == nil || strings.TrimSpace(binding.Model) == "" || strings.TrimSpace(binding.Provider) == "" {
		return errors.New("invalid model selection revision or binding")
	}
	// Copy all exported configuration, never mutexes or mutable usage estimates.
	// Execution resources remain the original ones; only model fields change.
	snapshot := cloneRunnerModelConfiguration(g.runner)
	snapshot.Client, snapshot.ProviderName = binding.Client, binding.Provider
	snapshot.Model, snapshot.APIModel = binding.Model, binding.APIModel
	snapshot.ProviderObservationKey = binding.ProviderObservationKey
	snapshot.Variant, snapshot.Effort = binding.Variant, binding.Effort
	snapshot.ProviderOptions = providers.CloneRequestObject(binding.ProviderOptions)
	snapshot.MediaInput, snapshot.NativeDeferredToolDiscovery = binding.MediaInput, binding.NativeDeferredToolDiscovery
	snapshot.SystemPrompt, snapshot.SystemPromptSections = binding.SystemPrompt, append([]SystemPromptSectionInfo(nil), binding.SystemPromptSections...)
	snapshot.ContextWindowOverride, snapshot.MaxInputTokens = binding.ContextWindowTokens, binding.InputTokens
	snapshot.OutputReserveTokens, snapshot.CompactThresholdTokens = binding.OutputReserveTokens, binding.CompactThresholdTokens
	snapshot.BeforeModelStep, snapshot.StepPolicy, snapshot.BeforeRequest = binding.BeforeModelStep, binding.StepPolicy, binding.BeforeRequest
	policy := g.policy
	if snapshot.SelectedCompaction != nil {
		selected, err := snapshot.SelectedCompaction.Describe(ctx, snapshot.compactionDescription())
		if err != nil {
			return err
		}
		policy = &selected
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	oldPrompt, _ := g.modelRunner().systemPromptSnapshot()
	newBinding := *binding
	newBinding.snapshot = snapshot
	// Keep session/project instructions appended to the model-owned base prompt.
	if len(g.activeHistory) > 0 && g.activeHistory[0].Role == "system" && oldPrompt != "" && strings.HasPrefix(g.activeHistory[0].Content, oldPrompt) {
		g.activeHistory[0].Content = snapshot.SystemPrompt + strings.TrimPrefix(g.activeHistory[0].Content, oldPrompt)
	} else if snapshot.SystemPrompt != "" && (len(g.activeHistory) == 0 || g.activeHistory[0].Role != "system") {
		g.activeHistory = append([]providers.ChatMessage{{Role: "system", Content: snapshot.SystemPrompt}}, g.activeHistory...)
	}
	g.policy = policy
	g.modelBinding = &newBinding
	g.client, _ = snapshot.providerConnectionSnapshot()
	g.turnRouting = providers.TurnRouting{}
	g.contextTokens = estimateMessages(g.activeHistory)
	g.revision++
	g.modelBoundaryPending = false
	return nil
}

func (g *streamTurnGateway) validateModelBinding(request providers.ChatRequest) error {
	r := g.modelRunner()
	if request.Provider != r.ProviderName {
		return errors.New("model request must use the bound provider")
	}
	if g.modelBinding != nil && (request.ModelSelectionRevision != g.modelRevision() || request.Model != r.requestModel()) {
		return errors.New("model request does not match the admitted model selection")
	}
	return nil
}

func cloneRunnerModelConfiguration(r *StreamRunner) *StreamRunner {
	prompt, sections := r.systemPromptSnapshot()
	r.usageMu.Lock()
	client, observationKey := r.Client, r.ProviderObservationKey
	r.usageMu.Unlock()
	return &StreamRunner{
		ResolveModelStep:            r.ResolveModelStep,
		SelectedPolicy:              r.SelectedPolicy,
		SelectedToolBatch:           r.SelectedToolBatch,
		ResumeSampling:              r.ResumeSampling,
		Client:                      client,
		ProviderName:                r.ProviderName,
		Tools:                       r.Tools,
		ToolLedger:                  r.ToolLedger,
		Model:                       r.Model,
		APIModel:                    r.APIModel,
		ProviderObservationKey:      observationKey,
		SystemPrompt:                prompt,
		SystemPromptSections:        sections,
		SelectedCompaction:          r.SelectedCompaction,
		CompactionRegistry:          r.CompactionRegistry,
		CompactionNoteStore:         r.CompactionNoteStore,
		ArchiveHistory:              r.ArchiveHistory,
		CommitFreshContext:          r.CommitFreshContext,
		MaxSteps:                    r.MaxSteps,
		Temperature:                 r.Temperature,
		MediaInput:                  r.MediaInput,
		OnEvent:                     r.OnEvent,
		OnUsage:                     r.OnUsage,
		OnTokenUsage:                r.OnTokenUsage,
		ContextWindowOverride:       r.ContextWindowOverride,
		ForceToolFirstStep:          r.ForceToolFirstStep,
		CompactThresholdTokens:      r.CompactThresholdTokens,
		MaxInputTokens:              r.MaxInputTokens,
		OutputReserveTokens:         r.OutputReserveTokens,
		CompactThresholdPct:         r.CompactThresholdPct,
		CompactKeepRecentTokens:     r.CompactKeepRecentTokens,
		DisableAutoCompact:          r.DisableAutoCompact,
		ForceInitialCompact:         r.ForceInitialCompact,
		CompactOnly:                 r.CompactOnly,
		StreamingToolExecution:      r.StreamingToolExecution,
		ToolWaitInterrupt:           r.ToolWaitInterrupt,
		BeforeStep:                  r.BeforeStep,
		BeforeModelStep:             r.BeforeModelStep,
		StepPolicy:                  r.StepPolicy,
		BeforeRequestContext:        r.BeforeRequestContext,
		BeforeRequest:               r.BeforeRequest,
		WrapModelClient:             r.WrapModelClient,
		OnRequestContext:            r.OnRequestContext,
		OnCompactAttempt:            r.OnCompactAttempt,
		BeforeCompact:               r.BeforeCompact,
		AfterCompact:                r.AfterCompact,
		OnToolBatchRejected:         r.OnToolBatchRejected,
		AfterTurn:                   r.AfterTurn,
		Effort:                      r.Effort,
		Variant:                     r.Variant,
		ProviderOptions:             r.ProviderOptions,
		NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
		PromptCacheKey:              r.PromptCacheKey,
		InferenceOperationKind:      r.InferenceOperationKind,
		InferenceWorkloadProfile:    r.InferenceWorkloadProfile,
		InferenceJournal:            r.InferenceJournal,
		BeginModelReceipts:          r.BeginModelReceipts,
		LoopDriver:                  r.LoopDriver,
		AcquireLoopDriver:           r.AcquireLoopDriver,
		DriverBindingStore:          r.DriverBindingStore,
		DriverCheckpointStore:       r.DriverCheckpointStore,
		ModelInputReceiptStore:      r.ModelInputReceiptStore,
		OnModelContext:              r.OnModelContext,
		RequestContextStore:         r.RequestContextStore,
	}
}
