package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func (g *streamTurnGateway) ReadSettings(ctx context.Context) (pluginprotocol.DriverTurnSettings, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return pluginprotocol.DriverTurnSettings{}, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return pluginprotocol.DriverTurnSettings{}, err
	}
	r := g.runner
	_, observationKey := r.providerConnectionSnapshot()
	settings := pluginprotocol.DriverTurnSettings{
		MaxSteps: r.MaxSteps, StreamingTools: r.StreamingToolExecution,
		ProviderObservationKey: observationKey, ModelVariant: r.Variant,
		ContextTokens: r.ContextWindowOverride, InputTokens: r.MaxInputTokens,
		OutputReserveTokens: r.OutputReserveTokens, CompactThresholdTokens: r.CompactThresholdTokens,
		CompactThresholdPercent: r.CompactThresholdPct, CompactKeepRecentTokens: r.CompactKeepRecentTokens,
		DisableAutoCompact:  r.DisableAutoCompact || r.proactiveCompactCircuitOpen(),
		ForceInitialCompact: r.ForceInitialCompact, CompactOnly: r.CompactOnly,
		CanArchive: r.ArchiveHistory != nil, CanCompact: g.client != nil,
	}
	if g.policy != nil {
		settings.CanCompact = true
		if g.policy.ContextWindows && r.ArchiveHistory != nil {
			settings.FreshContextTokens = g.policy.TargetTokens
		}
	} else if r.CompactionRegistry != nil && r.CompactionRegistry.Resolve(nil) != nil {
		settings.CanCompact = true
	}
	if g.policy == nil && r.contextWindowProvider() != nil && r.ArchiveHistory != nil {
		settings.FreshContextTokens = r.freshContextTargetTokens(r.CompactThresholdTokens)
	}
	_, sections := r.systemPromptSnapshot()
	for _, section := range sections {
		settings.SystemSections = append(settings.SystemSections, pluginprotocol.DriverSystemSection{Key: section.Key, Static: section.Static, Bytes: section.Bytes, Hash: section.Hash})
	}
	return settings, nil
}

func (g *streamTurnGateway) ReportTurn(ctx context.Context, observation pluginprotocol.DriverTurnObservation) error {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return err
	}
	if observation.Kind == "request_context" {
		if observation.RequestContext == nil {
			return errors.New("request context observation is required")
		}
		info := *observation.RequestContext
		info.StepIndex = g.step
		if g.runner.OnRequestContext != nil {
			g.runner.OnRequestContext(info)
		}
		if g.onEvent != nil {
			g.onEvent(providers.StreamEvent{Type: providers.EventRequestContext, RequestContext: requestContextSummary(info)})
		}
		return nil
	}
	if observation.Compact == nil {
		return errors.New("compaction observation is required")
	}
	compact := observation.Compact
	reason := CompactReason(compact.Reason)
	switch observation.Kind {
	case "before_compact":
		if g.runner.BeforeCompact != nil {
			return g.runner.BeforeCompact(ctx, reason)
		}
	case "after_compact":
		if g.runner.AfterCompact != nil {
			var compactErr error
			if compact.Failure != nil {
				compactErr = &loopdriver.FailureError{Failure: *compact.Failure}
			}
			return g.runner.AfterCompact(ctx, reason, compactErr)
		}
	case "compact_start":
		if g.onEvent != nil {
			g.onEvent(providers.StreamEvent{Type: providers.EventCompact, CompactReason: compact.Reason, CompactPhase: providers.CompactPhaseStarted})
		}
	case "compact_result":
		info := CompactInfo{Reason: reason, TokensBefore: compact.TokensBefore, TokensAfter: compact.TokensAfter, MessagesBefore: compact.MessagesBefore, MessagesAfter: compact.MessagesAfter, Summary: compact.Summary}
		if g.onEvent != nil {
			g.onEvent(providers.StreamEvent{Type: providers.EventCompact, Content: formatCompactNotice(info), CompactReason: compact.Reason, CompactPhase: providers.CompactPhaseCompleted, CompactSummary: compact.Summary})
		}
	case "compact_attempt":
		info := CompactAttemptInfo{Reason: reason, Status: CompactAttemptStatus(compact.Status), TokensBefore: compact.TokensBefore, LastResponseTotal: compact.LastResponseTotal, PendingDelta: compact.PendingDelta, UsageAdjustment: UsageAdjustment(compact.UsageAdjustment), MessagesBefore: compact.MessagesBefore, MessagesAfter: compact.MessagesAfter, Error: compact.Error, OutputLimit: compact.OutputLimit}
		g.runner.recordCompactAttempt(info)
		if g.runner.OnCompactAttempt != nil {
			g.runner.OnCompactAttempt(info)
		}
		if notice, ok := formatCompactAttemptNotice(info); ok && g.onEvent != nil {
			phase := providers.CompactPhaseCompleted
			if info.Status == CompactAttemptFailed {
				phase = providers.CompactPhaseFailed
			}
			g.onEvent(providers.StreamEvent{Type: providers.EventCompact, Content: notice, CompactReason: compact.Reason, CompactPhase: phase})
		}
	default:
		return errors.New("unknown turn observation")
	}
	return nil
}

// CompactContext proposes a replacement. It never commits or decides whether
// to retry a model request; the driver validates and accepts the proposal.
func (g *streamTurnGateway) CompactContext(ctx context.Context, messages []providers.ChatMessage, hint pluginprotocol.DriverCompactBudget) ([]providers.ChatMessage, error) {
	ctx, leave, err := g.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer leave()
	if err := g.prepare(ctx); err != nil {
		return nil, err
	}
	if len(g.streamedBatches) != 0 {
		return nil, errors.New("join the pending streaming tool batch before compacting context")
	}
	if err := validateDriverMessages(messages); err != nil {
		return nil, err
	}
	r := g.runner
	model := strings.TrimSpace(r.APIModel)
	if model == "" {
		model = r.Model
	}
	media := providers.ResolveMediaInput(g.client, r.MediaInput, r.ProviderOptions)
	if hint.Window != nil {
		window := hint.Window
		if window.HistoryHead != g.archiveHead {
			return nil, errors.New("context window archive head is stale")
		}
		if g.policy != nil {
			if !g.policy.ContextWindows {
				return nil, errors.New("selected policy disables context windows")
			}
			result, err := r.SelectedCompaction.Window(ctx, compactionservice.WindowRequest{PolicyKey: g.policy.Key, Messages: providers.CloneChatMessages(messages), HistoryHead: window.HistoryHead, FixedTokens: window.FixedTokens, TargetTokens: window.TargetTokens, MediaInput: media})
			return result.Messages, err
		}
		if r.contextWindowProvider() == nil {
			return nil, errors.New("context window provider is unavailable")
		}
		return buildFreshContext(messages, window.HistoryHead, window.FixedTokens, window.TargetTokens, media)
	}

	ctx = providers.WithInferenceJournal(ctx, r.InferenceJournal)
	ctx, lineage := providers.BeginInferenceOperationLineage(ctx, g.lastModelOperationID)
	ctx = withCompactBudgetHint(ctx, compactBudgetHint{Reason: CompactReason(hint.Reason), LastSuccessfulTokens: hint.LastSuccessfulTokens, FailedRequestTokens: hint.FailedRequestTokens, TargetTotalTokens: hint.TargetTotalTokens})
	cfg := LoopConfig{
		Model: model, CompactionRegistry: r.CompactionRegistry, CompactionNoteStore: r.CompactionNoteStore,
		ForkCompactionNote: r.compactionNoteFork(r.snapshotRetainedRequestContext()),
		OnCompactionNote:   func(status string, err error) { emitCompactionNoteEvent(g.onEvent, status, err) },
	}
	if g.policy != nil {
		definitions, err := toolDefinitions(ctx, r.Tools)
		if err != nil {
			return nil, err
		}
		request := compactionservice.Request{Model: model, Messages: providers.CloneChatMessages(messages), Budget: compact.Budget{MediaInput: media, ContextTokens: r.ContextWindowOverride, InputTokens: r.MaxInputTokens, OutputReserveTokens: r.OutputReserveTokens, KeepRecentTokens: r.CompactKeepRecentTokens}, Options: compact.NativeOptions{Provider: r.ProviderName, Tools: definitions, Temperature: r.Temperature, ProviderOptions: r.ProviderOptions, MediaInput: media, NativeDeferredToolDiscovery: providers.NativeToolDiscoveryEnabled(r.NativeDeferredToolDiscovery, definitions)}, Hint: compactionservice.BudgetHint{Reason: hint.Reason, LastSuccessfulTokens: hint.LastSuccessfulTokens, FailedRequestTokens: hint.FailedRequestTokens, TargetTotalTokens: hint.TargetTotalTokens}}
		replacement, err := compactSelected(compactionservice.WithModelClient(ctx, g.client), r.SelectedCompaction, *g.policy, request, cfg)
		g.proposedContextParentID, g.proposedContextHash = "", ""
		if err == nil && compactChanged(messages, replacement) {
			g.proposedContextParentID = lineage.LastOperationID()
			g.proposedContextHash = hashMessagesForRequestShape(replacement)
		}
		return replacement, err
	}
	if g.client != nil {
		cfg.Compact = func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			budget, err := applyAdaptiveCompactBudget(ctx, messages, compact.Budget{MediaInput: media, ContextTokens: r.ContextWindowOverride, InputTokens: r.MaxInputTokens, OutputReserveTokens: r.OutputReserveTokens, KeepRecentTokens: r.CompactKeepRecentTokens})
			if err != nil {
				return nil, err
			}
			definitions, err := toolDefinitions(ctx, r.Tools)
			if err != nil {
				return nil, err
			}
			return compact.CompactWithNativeOrSummary(ctx, messages, g.client, model, budget, compact.NativeOptions{Provider: r.ProviderName, Tools: definitions, Temperature: r.Temperature, ProviderOptions: r.ProviderOptions, MediaInput: media, NativeDeferredToolDiscovery: providers.NativeToolDiscoveryEnabled(r.NativeDeferredToolDiscovery, definitions)})
		}
	}
	compactContext := resolveEffectiveCompaction(cfg)
	if compactContext == nil {
		return nil, errors.New("compaction provider is unavailable")
	}
	replacement, err := compactContext(ctx, providers.CloneChatMessages(messages))
	g.proposedContextParentID, g.proposedContextHash = "", ""
	if err == nil && compactChanged(messages, replacement) {
		g.proposedContextParentID = lineage.LastOperationID()
		g.proposedContextHash = hashMessagesForRequestShape(replacement)
	}
	return replacement, err
}
