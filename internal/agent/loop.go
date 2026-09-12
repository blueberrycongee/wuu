package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/compact"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
)

// EmptyAnswerError is returned when the model completes a turn without
// producing any text content or tool calls. StopReason carries the
// provider's finish signal (e.g. "stop", "end_turn") when one was
// received, or "" when the stream ended without a normal stop — the
// latter usually indicates a proxy/compatibility issue rather than
// intentional model behaviour.
type EmptyAnswerError struct {
	StopReason string
}

func (e *EmptyAnswerError) Error() string {
	if e.StopReason != "" {
		return fmt.Sprintf("model returned empty answer (stop_reason=%s)", e.StopReason)
	}
	return "model returned empty answer"
}

// IsEmptyAnswer reports whether err (or any error in its chain) is an
// EmptyAnswerError. Callers use this to distinguish empty-content
// failures from other fatal errors.
func IsEmptyAnswer(err error) bool {
	var target *EmptyAnswerError
	return errors.As(err, &target)
}

// ToolSurfaceFreezer is implemented by tool executors whose provider-facing
// definitions can change asynchronously (e.g. MCP catalog updates). RunToolLoop
// freezes the surface for the duration of one run so req.Tools stays
// byte-stable across rounds; deferred changes land after the run.
type ToolSurfaceFreezer interface {
	FreezeToolSurface()
	UnfreezeToolSurface()
}

// RunToolLoop drives the shared multi-step tool-use loop both Runner
// and StreamRunner depend on. It is transport-agnostic: callers
// supply a Step that knows how to perform one model round-trip
// (Chat for Runner, SSE consumption for StreamRunner) and a
// LoopConfig describing the rest.
//
// Behavior:
//   - Loops up to cfg.MaxSteps rounds (0 = unlimited).
//   - On context-overflow errors from the step, calls cfg.Compact
//     once and re-issues the step. Consecutive overflows propagate;
//     fresh context windows allow recovery again after a successful step.
//   - Output truncation is treated as a completed model response with
//     FinishReason=length. The caller/UI can surface that reason without
//     classifying the turn as a user interruption or transport failure.
//   - Executes any tool calls the model requested, recording results
//     as tool messages and (if configured) emitting them through
//     OnToolResult so callers can render them live.
//   - Returns the final assistant message + the slice of new messages
//     produced during this run + cumulative token usage.
//
// The history slice is treated as immutable; callers can reuse it.
func RunToolLoop(
	ctx context.Context,
	history []providers.ChatMessage,
	cfg LoopConfig,
	step Step,
) (loopResult LoopResult, runErr error) {
	if step == nil {
		return LoopResult{}, errors.New("agent: step is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return LoopResult{}, errors.New("agent: model is required")
	}

	// Resolve the effective compaction strategy. When CompactionRegistry
	// is set, the highest-priority registered provider wins. A provider
	// that reports ErrCompactionUnavailable defers to cfg.Compact, so
	// experimental strategies can fall back to the default summarizer
	// when they have no strategy for the current transcript.
	effectiveCompact := resolveEffectiveCompaction(cfg)
	if cfg.FreshContext != nil && cfg.ArchiveHistory != nil {
		effectiveCompact = nil
	}
	ctx, workflow, ownsWorkflow := providers.EnsureInferenceWorkflow(ctx, cfg.InferenceWorkloadProfile)
	if ownsWorkflow {
		defer func() {
			outcome := providers.InferenceOutcomeSucceeded
			if runErr != nil {
				outcome = providers.InferenceOutcomeFailed
				if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) || ctx.Err() != nil {
					outcome = providers.InferenceOutcomeCanceled
				}
			}
			if err := providers.CompleteInferenceWorkflow(workflow, providers.InferenceJournalFromContext(ctx), outcome); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}()
	}

	messages := make([]providers.ChatMessage, len(history))
	copy(messages, history)
	// Transient context is request-only across runs, but append-only inside
	// a run so provider continuation deltas can match prior request prefixes.
	messages = filterTransientModelContextHistory(messages)
	// providerMessages is the provider-facing transcript: live history plus
	// the request-only context messages already sent in this run. Each round
	// extends it append-only — before per-request compatibility transforms — so every
	// request's messages extend the prior request's exact byte prefix and
	// prompt caches stay warm. It re-bases only when history is rewritten.
	providerMessages := providers.CloneChatMessages(messages)
	startLen := len(messages)
	var durableNewMessages []providers.ChatMessage
	var historyArchiveHeadSeq int
	defer func() {
		loopResult.DurableNewMessages = providers.CloneChatMessages(durableNewMessages)
		loopResult.DurableMessagesTracked = true
		loopResult.HistoryArchiveHeadSeq = historyArchiveHeadSeq
	}()

	// retainedContext tracks the transcript's request-only messages (position
	// + content) for cross-run prompt-cache continuity. It seeds from the
	// previous run's state below and is handed to the next run via
	// LoopResult.RetainedRequestContext.
	var retainedContext []RetainedContextMessage
	defer func() {
		loopResult.RetainedRequestContext = buildRetainedRequestContextState(retainedContext, messages)
	}()

	// Pin the provider-facing tool surface for the duration of the run so
	// asynchronous catalog changes (e.g. MCP server events) cannot reshape
	// req.Tools between rounds and invalidate the prompt-cache prefix
	// mid-run. Model-initiated surface growth (deferred tool loads) is not
	// affected.
	if freezer, ok := cfg.Tools.(ToolSurfaceFreezer); ok && freezer != nil {
		freezer.FreezeToolSurface()
		defer freezer.UnfreezeToolSurface()
	}

	currentMaxTokens := cfg.DefaultMaxTokens // 0 = provider default

	var (
		totalIn, totalOut, totalCacheCreation, totalCacheRead int
		// Reactive auto-compact (overflow recovery) runs at most once
		// between successful steps for fresh windows (once per Run for
		// legacy compaction); if a single compaction isn't enough, surfacing the
		// error is more honest than silently looping. Proactive compact
		// runs before provider requests, including mid-turn continuation
		// requests after completed tool results. A failed or no-op
		// proactive attempt suppresses further proactive attempts for
		// this Run so the loop cannot spin on an unhelpful compactor.
		overflowCompacted   bool
		proactiveSuppressed bool
		historyRewritten    bool
		// Tracks current context fill so we can decide whether to
		// proactively compact before the next round. Uses
		// response.usage as ground truth + delta estimation for
		// messages added since the last successful response.
		usage = cfg.UsageTracker
		// Request-only context produced by tool execution in the current
		// run. It is consumed by the next provider request and never enters
		// live or durable history.
		postToolContextSegments []ContextSegment
		// sentRequestContext gates request-only context re-emission: it maps
		// each context message key to the content already retained in
		// providerMessages. Producers re-emit their full snapshot every
		// round; unchanged blocks are dropped at the gate so the transcript
		// carries one copy per distinct snapshot instead of one copy per
		// round. Retention itself lives in providerMessages.
		sentRequestContext map[string]string
		// Previous request's stable-prefix fingerprint for cache-break telemetry.
		// Used to detect and log when model, tools, or system prompt changes
		// between rounds, which would break cache reuse.
		prevCacheFingerprint string
		// Compaction is a nested inference flow. Its final operation becomes the
		// parent of the first agent round that consumes the rewritten history.
		lastAgentOperationID         string
		nextOperationParentID        string
		newContextRequested          bool
		lowBudgetReminderSent        bool
		emptyAnswerRecoveryAttempted bool
	)
	if usage == nil {
		usage = NewUsageTracker()
		// Without caller-owned cross-turn state, seed this run from a
		// local estimate so resumed long sessions can compact before
		// the first provider request.
		usage.SetAdjustment(UsageAdjustmentInitialHistoryEstimate)
		usage.RecordPendingMessages(messages)
	}
	// Cross-run continuity: splice the previous run's retained request-only
	// context back into the transcript when the durable history still matches
	// its fingerprint, so this run's first request byte-extends the previous
	// run's last request. Stale state (history rewritten, forked, or edited
	// since) fails the fingerprint and is silently dropped — one cache miss,
	// never a correctness issue. The actual splice is deferred to the first
	// round (see pendingRetainedContext) so generic messages can be reconciled
	// against fresh context. Typed blocks are an ordered update stream: prior
	// values stay in the prefix and a later active/inactive update supersedes
	// them without leaking stale semantics.
	var pendingRetainedContext []RetainedContextMessage
	if state := cfg.RetainedRequestContext; state.validFor(messages) {
		pendingRetainedContext = state.Messages
	}
	// A replacement remains speculative until request validation and durable
	// commit succeed. Restore it on every early return, before the retained
	// context defer constructs the next run's state.
	var rollbackFreshContext func()
	defer func() {
		if rollbackFreshContext != nil {
			rollbackFreshContext()
			loopResult.NewMessages = newMessagesForReturn(messages, startLen, historyRewritten)
			loopResult.HistoryRewritten = historyRewritten
			if runErr != nil {
				emitCompactAttempt(cfg, CompactAttemptInfo{
					Reason: CompactReasonNewContext, Status: CompactAttemptFailed,
					TokensBefore:   estimateFreshContextMessages(messages),
					MessagesBefore: compactNoticeMessageCount(messages), Error: runErr.Error(),
				})
			}
		}
	}()
	threshold := proactiveCompactThreshold(cfg)
	// resetTranscript re-bases the live history and the provider transcript
	// after a history rewrite (compaction, tool-call repair, post-tool
	// rewrite). Every rewrite site must keep these fields in lockstep or the
	// provider transcript silently diverges from live history; route new
	// rewrite paths through here.
	resetTranscript := func(rewritten []providers.ChatMessage) {
		messages = rewritten
		providerMessages = providers.CloneChatMessages(rewritten)
		sentRequestContext = nil
		retainedContext = nil
		// A rewrite invalidates the previous run's retained positions; drop
		// any not-yet-spliced carryover so it cannot land on new history.
		pendingRetainedContext = nil
		historyRewritten = true
		usage.Reset()
		usage.SetAdjustment(UsageAdjustmentCompactionRewriteEstimate)
		usage.RecordPendingMessages(messages)
	}
	// runCompactPass executes one compact attempt. Non-forced passes are
	// gated on the proactive fill-rate threshold and suppress further
	// proactive attempts on failure/no-op; forced passes (user-requested
	// /compact) bypass the gate and never suppress the proactive path.
	runCompactPass := func(reason CompactReason, force bool) {
		if effectiveCompact == nil {
			return
		}
		if !force && (proactiveSuppressed || threshold <= 0 || usage.EstimateCurrent() < threshold || !canProactivelyCompact(messages, cfg)) {
			return
		}
		usageBefore := usage.Breakdown()
		before := usageBefore.Total()
		msgsBefore := len(messages)
		if cfg.OnCompactStart != nil {
			cfg.OnCompactStart(reason)
		}
		if cfg.BeforeCompact != nil {
			if hookErr := cfg.BeforeCompact(ctx, reason); hookErr != nil {
				if !force {
					proactiveSuppressed = true
				}
				emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
					Reason: reason, Status: CompactAttemptFailed, TokensBefore: before,
					MessagesBefore: msgsBefore, Error: hookErr.Error(),
				}, usageBefore))
				return
			}
		}
		compactCtx, lineage := providers.BeginInferenceOperationLineage(ctx, lastAgentOperationID)
		compactCtx = withCompactBudgetHint(compactCtx, compactBudgetHint{Reason: reason})
		compacted, cerr := effectiveCompact(compactCtx, messages)
		if cerr == nil && compactChanged(messages, compacted) && estimateFreshContextMessages(compacted) >= estimateFreshContextMessages(messages) {
			cerr = errors.New("compaction replacement did not shrink under the local context estimator")
		}
		if cfg.AfterCompact != nil {
			if hookErr := cfg.AfterCompact(ctx, reason, cerr); hookErr != nil {
				cerr = hookErr
			}
		}
		switch {
		case cerr != nil:
			if !force {
				proactiveSuppressed = true
			}
			emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
				Reason:         reason,
				Status:         CompactAttemptFailed,
				TokensBefore:   before,
				MessagesBefore: msgsBefore,
				Error:          cerr.Error(),
				OutputLimit:    compact.IsSummaryOutputLimit(cerr),
			}, usageBefore))
		case compactChanged(messages, compacted):
			noticeMessagesBefore := compactNoticeMessageCount(messages)
			noticeMessagesAfter := compactNoticeMessageCount(compacted)
			if compactOperationID := lineage.LastOperationID(); compactOperationID != "" && compactOperationID != lastAgentOperationID {
				nextOperationParentID = compactOperationID
			}
			resetTranscript(compacted)
			emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
				Reason:         reason,
				Status:         CompactAttemptSucceeded,
				TokensBefore:   before,
				MessagesBefore: msgsBefore,
				MessagesAfter:  len(messages),
			}, usageBefore))
			if cfg.OnCompact != nil {
				cfg.OnCompact(CompactInfo{
					Reason:         reason,
					TokensBefore:   before,
					TokensAfter:    usage.EstimateCurrent(),
					MessagesBefore: noticeMessagesBefore,
					MessagesAfter:  noticeMessagesAfter,
					Summary:        compactSummaryFromMessages(messages),
				})
			}
		default:
			if !force {
				proactiveSuppressed = true
			}
			emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
				Reason:         reason,
				Status:         CompactAttemptUnchanged,
				TokensBefore:   before,
				MessagesBefore: msgsBefore,
				MessagesAfter:  len(compacted),
			}, usageBefore))
		}
	}
	tryProactiveCompact := func() { runCompactPass(CompactReasonProactive, false) }
	appendMessage := func(msg providers.ChatMessage) {
		messages = append(messages, msg)
		providerMessages = append(providerMessages, providers.CloneChatMessage(msg))
		durableNewMessages = append(durableNewMessages, providers.CloneChatMessage(msg))
		if cfg.OnMessage != nil && !msg.Hidden {
			cfg.OnMessage(msg)
		}
		if cfg.OnHistoryAdvanced != nil {
			cfg.OnHistoryAdvanced(providers.CloneChatMessages(messages))
		}
	}
	if cfg.OnHistoryAdvanced != nil {
		cfg.OnHistoryAdvanced(providers.CloneChatMessages(messages))
	}

	freshContextEnabled := cfg.FreshContext != nil && cfg.ArchiveHistory != nil
	if cfg.ForceInitialCompact && freshContextEnabled {
		newContextRequested = true
	} else if cfg.ForceInitialCompact {
		runCompactPass(CompactReasonManual, true)
	}
	if cfg.CompactOnly && !newContextRequested {
		return LoopResult{
			NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
			HistoryRewritten: historyRewritten,
		}, nil
	}

	for stepIdx := 0; cfg.MaxSteps == 0 || stepIdx < cfg.MaxSteps; stepIdx++ {
		if cfg.BeforeStep != nil {
			injected := cfg.BeforeStep()
			if len(injected) > 0 {
				for _, msg := range injected {
					appendMessage(msg)
				}
				usage.RecordPendingMessages(injected)
			}
		}
		if cfg.BeforeModelStep != nil {
			injected, injectErr := cfg.BeforeModelStep(ctx, stepIdx, providers.CloneChatMessages(messages))
			if injectErr != nil {
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), fmt.Errorf("before model step: %w", injectErr)
			}
			for _, msg := range injected {
				appendMessage(msg)
			}
			usage.RecordPendingMessages(injected)
		}
		hardContextRollover := freshContextEnabled && threshold > 0 && usage.EstimateCurrent() >= threshold
		attemptFreshContext := newContextRequested || hardContextRollover
		if !attemptFreshContext {
			tryProactiveCompact()
		}
		if repaired, changed, nerr := repairLiveToolCallHistory(messages); nerr != nil {
			return LoopResult{
				NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten:    historyRewritten,
				InputTokens:         totalIn,
				OutputTokens:        totalOut,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
			}, nerr
		} else if changed {
			resetTranscript(repaired)
		}
		currentSegments := requestContextSegments(cfg.BeforeRequestContext)
		if cfg.ContextWindowStatus != nil {
			currentSegments = append(currentSegments, RequestOnlyContextMessages([]providers.ChatMessage{
				contextWindowReminder(cfg.ContextWindowStatus(ctx, messages)),
			})...)
		}
		var freshContextFailure string
		freshContextApplied := false
		var freshContextAttempt *CompactAttemptInfo
		var freshContextInfo *CompactInfo
		var freshRollbackMessages []providers.ChatMessage
		var freshRollbackProviderMessages []providers.ChatMessage
		freshRollbackHistoryRewritten := historyRewritten
		if attemptFreshContext {
			targetTokens := cfg.FreshContextTokens
			if targetTokens <= 0 {
				targetTokens = FreshContextTargetTokens
			}
			fixedSegments := append(append([]ContextSegment(nil), currentSegments...), postToolContextSegments...)
			fixedMessages := assembleModelRequest(nil, fixedSegments).Messages
			fixedRequest := providers.ChatRequest{Messages: fixedMessages}
			if cfg.Tools != nil {
				fixedRequest.Tools = cfg.Tools.Definitions()
			}
			fixedTokens := estimateOutboundRequestTokens(fixedRequest)
			beforeTokens := fixedTokens + estimateFreshContextMessages(messages)
			beforeMessages := compactNoticeMessageCount(messages)
			archive, archiveErr := cfg.ArchiveHistory(ctx, providers.CloneChatMessages(durableNewMessages))
			if archiveErr == nil {
				messages = applyArchivedHistorySeqs(messages, durableNewMessages, archive.Seqs)
				providerMessages = applyArchivedHistorySeqs(providerMessages, durableNewMessages, archive.Seqs)
				durableNewMessages = nil
				if archive.HeadSeq > historyArchiveHeadSeq {
					historyArchiveHeadSeq = archive.HeadSeq
				}
			}
			var replacement []providers.ChatMessage
			freshErr := archiveErr
			if freshErr == nil {
				replacement, freshErr = cfg.FreshContext(ctx, providers.CloneChatMessages(messages), historyArchiveHeadSeq, fixedTokens, targetTokens)
			}
			if freshErr == nil && compactChanged(messages, replacement) {
				freshRollbackMessages = providers.CloneChatMessages(messages)
				freshRollbackProviderMessages = providers.CloneChatMessages(providerMessages)
				rollbackRetainedContext := retainedContext
				rollbackPendingContext := pendingRetainedContext
				rollbackSentContext := sentRequestContext
				rollbackFreshContext = func() {
					messages = freshRollbackMessages
					providerMessages = freshRollbackProviderMessages
					historyRewritten = freshRollbackHistoryRewritten
					retainedContext = append(append([]RetainedContextMessage(nil), rollbackRetainedContext...), rollbackPendingContext...)
					pendingRetainedContext = rollbackPendingContext
					sentRequestContext = rollbackSentContext
					usage.Reset()
					usage.SetAdjustment(UsageAdjustmentRequestShapeReset)
					usage.RecordPendingMessages(messages)
				}
				resetTranscript(replacement)
				newContextRequested = false
				lowBudgetReminderSent = false
				freshContextApplied = true
				// This request is sent only after the checkpoint commit succeeds.
				currentSegments = requestContextSegments(cfg.BeforeRequestContext)
				currentSegments = append(currentSegments, RequestOnlyContextMessages([]providers.ChatMessage{
					contextWindowReminder("The host has installed a fresh active window. Read your persistent notes and recover missing facts from durable history before continuing. No summary was generated."),
				})...)
				afterTokens := fixedTokens + estimateFreshContextMessages(messages)
				freshContextAttempt = &CompactAttemptInfo{
					Reason: CompactReasonNewContext, Status: CompactAttemptSucceeded,
					TokensBefore: beforeTokens, MessagesBefore: beforeMessages, MessagesAfter: len(messages),
				}
				freshContextInfo = &CompactInfo{
					Reason: CompactReasonNewContext, TokensBefore: beforeTokens, TokensAfter: afterTokens,
					MessagesBefore: beforeMessages, MessagesAfter: compactNoticeMessageCount(messages),
					Summary: compactSummaryFromMessages(messages),
				}
			} else {
				if freshErr == nil {
					freshErr = ErrFreshContextNotSmaller
				}
				freshContextFailure = freshErr.Error()
				newContextRequested = false
				emitCompactAttempt(cfg, CompactAttemptInfo{
					Reason: CompactReasonNewContext, Status: CompactAttemptFailed,
					TokensBefore: beforeTokens, MessagesBefore: beforeMessages, Error: freshContextFailure,
				})
				if cfg.CompactOnly {
					return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), freshErr
				}
			}
		}
		// Cross-run continuity: on the first round, splice back only the
		// previous run's retained request-only context at its recorded
		// positions. Typed blocks retain their ordered update stream; generic
		// messages are kept only when re-emitted byte-identically.
		if len(pendingRetainedContext) > 0 {
			reconciled := reconciledRetainedContext(pendingRetainedContext, currentSegments)
			if len(reconciled) > 0 {
				providerMessages = spliceRetainedContext(messages, reconciled)
				for _, entry := range reconciled {
					retainedContext = append(retainedContext, entry)
					sentRequestContext = recordSentRequestContext(sentRequestContext, []providers.ChatMessage{entry.Message})
				}
			}
			pendingRetainedContext = nil
		}
		// Emit-on-change: producers re-emit their full context snapshot every
		// round; only blocks whose content the transcript does not already
		// retain are appended. A changed block appends its new snapshot while
		// the prior copy stays put inside the stable prefix.
		newContextSegments := filterUnsentRequestContext(currentSegments, sentRequestContext)
		// Post-tool context is one-shot new information and bypasses the
		// emit-on-change gate. It stays re-queueable until the request that
		// carries it succeeds so an overflow-compact retry does not drop it.
		consumedPostToolSegments := postToolContextSegments
		postToolContextSegments = nil
		currentRequestSegments := append(append([]ContextSegment(nil), currentSegments...), consumedPostToolSegments...)
		inactiveMessages := inactiveRequestContextMessages(currentRequestSegments, sentRequestContext)
		requestSegments := append(newContextSegments, consumedPostToolSegments...)
		requestSegments = append(requestSegments, RequestOnlyContextMessages(inactiveMessages)...)
		if freshContextFailure != "" {
			requestSegments = append(requestSegments, RequestOnlyContextMessages([]providers.ChatMessage{
				contextWindowReminder("The requested context-window transition was not applied; active history is unchanged. Continue safely or retry later. Reason: " + freshContextFailure),
			})...)
		}
		if freshContextEnabled && !freshContextApplied && !lowBudgetReminderSent && threshold > 0 {
			reminderAt := max(1, threshold-min(freshContextReminderTokens, threshold/4))
			if usage.EstimateCurrent() >= reminderAt {
				requestSegments = append(requestSegments, RequestOnlyContextMessages([]providers.ChatMessage{
					contextWindowReminder("The active context budget is low. Save progress, decisions and next steps with your note tools now, then call new_context at a safe boundary. The host will release this transcript without summarizing it."),
				})...)
				lowBudgetReminderSent = true
			}
		}
		assembly := assembleModelRequest(providerMessages, requestSegments)
		// Retain this round's request-only context in the transcript before
		// compatibility transforms run, so retention is never contaminated by
		// request-scoped rewrites.
		providerMessages = assembly.Messages
		for _, msg := range assembly.RequestOnlyMessages {
			sentRequestContext = recordSentRequestContext(sentRequestContext, []providers.ChatMessage{msg})
			retainedContext = append(retainedContext, RetainedContextMessage{
				AfterDurable: len(messages),
				Message:      providers.CloneChatMessage(msg),
			})
		}
		requestMessages := assembly.Messages
		operation := providers.NewInferenceOperation(
			cfg.InferenceOperationKind,
			cfg.InferenceWorkloadProfile,
		)
		operation.ParentOperationID = nextOperationParentID
		nextOperationParentID = ""
		req := providers.ChatRequest{
			Provider:                    cfg.ProviderName,
			Model:                       cfg.Model,
			Messages:                    requestMessages,
			Temperature:                 cfg.Temperature,
			MediaInput:                  cfg.MediaInput,
			Operation:                   operation,
			MaxTokens:                   currentMaxTokens,
			StepIndex:                   stepIdx,
			Effort:                      cfg.Effort,
			ProviderOptions:             provideroptions.Clone(cfg.ProviderOptions),
			NativeDeferredToolDiscovery: cfg.NativeDeferredToolDiscovery,
		}
		if cfg.Tools != nil {
			req.Tools = cfg.Tools.Definitions()
		}
		// Providers only emit a forced tool_choice when the request carries
		// tools, so the intent passes through unconditionally here.
		if stepIdx == 0 && cfg.ForceToolFirstStep != "" {
			req.ForceToolName = cfg.ForceToolFirstStep
		}
		if cfg.BeforeRequest != nil {
			// Per-request transform: hand it an isolated deep copy so
			// in-place edits stay request-scoped. Nothing a transform does
			// reaches providerMessages or live history.
			req.Messages = providers.CloneChatMessages(req.Messages)
			forceBefore := strings.TrimSpace(req.ForceToolName)
			forceAvailableBefore := requestHasTool(req.Tools, forceBefore)
			if err := cfg.BeforeRequest(ctx, &req); err != nil {
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), fmt.Errorf("transform model request: %w", err)
			}
			if err := validateTransformedRequest(req, forceBefore, forceAvailableBefore); err != nil {
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), err
			}
		}
		// Pluggable transform chain: multi-plugin request transforms
		// registered via the capability contract. Executes after the
		// legacy BeforeRequest so existing callers are unaffected.
		if cfg.RequestTransforms != nil && cfg.RequestTransforms.Count() > 0 {
			req.Messages = providers.CloneChatMessages(req.Messages)
			forceBefore := strings.TrimSpace(req.ForceToolName)
			forceAvailableBefore := requestHasTool(req.Tools, forceBefore)
			if err := cfg.RequestTransforms.Apply(ctx, &req, nil); err != nil {
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), fmt.Errorf("transform model request (chain): %w", err)
			}
			if err := validateTransformedRequest(req, forceBefore, forceAvailableBefore); err != nil {
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), err
			}
		}
		if freshContextApplied {
			targetTokens := cfg.FreshContextTokens
			if targetTokens <= 0 {
				targetTokens = FreshContextTargetTokens
			}
			if estimated := estimateOutboundRequestTokens(req); estimated > targetTokens {
				err := fmt.Errorf("%w after request transforms: estimated=%d target=%d", ErrFreshContextTooLarge, estimated, targetTokens)
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), err
			}
			rebuildCommittedRequest := false
			if cfg.AcceptFreshContext != nil {
				committed, head, err := cfg.AcceptFreshContext(ctx, providers.CloneChatMessages(messages), historyArchiveHeadSeq)
				if err != nil {
					return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), err
				}
				seqs := make([]int, len(messages))
				for i := range messages {
					seqs[i] = committed[i].Seq
				}
				providerMessages = applyArchivedHistorySeqs(providerMessages, messages, seqs)
				req.Messages = applyArchivedHistorySeqs(req.Messages, messages, seqs)
				rebuildCommittedRequest = len(committed) > len(messages)
				providerMessages = append(providerMessages, providers.CloneChatMessages(committed[len(messages):])...)
				messages = committed
				historyArchiveHeadSeq = head
			}
			rollbackFreshContext = nil
			if freshContextAttempt != nil {
				emitCompactAttempt(cfg, *freshContextAttempt)
			}
			if freshContextInfo != nil && cfg.OnCompact != nil {
				cfg.OnCompact(*freshContextInfo)
			}
			if rebuildCommittedRequest {
				// Concurrent steering saved during commit must be seen before
				// another action. Reassemble and budget-check the committed window
				// rather than sending the now-stale pre-commit request.
				usage.Reset()
				usage.RecordPendingMessages(messages)
				stepIdx--
				continue
			}
		}
		if cfg.CompactOnly {
			return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), nil
		}
		cacheHint := buildCacheHint(req.Messages)
		applyPromptCacheKeyOverride(&cacheHint, cfg.PromptCacheKey)
		req.CacheHint = cacheHint
		// Telemetry mirrors the exact outbound request after compatibility
		// transforms; the retained transcript was already fixed above.
		assembly.Messages = req.Messages

		// Cache-break telemetry: detect changes in stable-prefix components
		// (model, tools, system prompt) that would break cache reuse. Section
		// content never reaches the loop; the builder-computed per-section
		// hashes stand in for it.
		systemHashes := make([]string, 0, len(cfg.SystemPromptSections))
		for _, sec := range cfg.SystemPromptSections {
			systemHashes = append(systemHashes, sec.Key+":"+sec.Hash)
		}
		currentFingerprint := cacheStablePrefixFingerprint(cfg.Model, req.Tools, systemHashes)
		if stepIdx > 0 && prevCacheFingerprint != "" && prevCacheFingerprint != currentFingerprint {
			// A cache-stable component changed between rounds. This shouldn't happen
			// within a single turn, so log it for diagnostics.
			providers.DebugLogf("prompt-cache stable prefix changed between rounds")
		}
		prevCacheFingerprint = currentFingerprint

		requestInfo := requestContextInfo(stepIdx, assembly, req.Tools, cacheHint, cfg.SystemPromptSections)
		requestUsageContract := usageContractKey(
			cfg.ProviderObservationKey,
			req.Provider,
			req.Model,
			cfg.ModelVariant,
			req.Effort,
			req.ProviderOptions,
		)
		if cfg.OnRequestContext != nil {
			cfg.OnRequestContext(requestInfo)
		}
		if cfg.ModelInputReceiptStore != nil {
			receipt := ModelInputReceipt{
				ContractVersion:  ModelInputReceiptContractVersion,
				OperationID:      req.Operation.ID,
				SessionID:        cfg.SessionID,
				ExecutionID:      cfg.ExecutionID,
				DriverID:         cfg.DriverID,
				DriverVersion:    cfg.DriverVersion,
				Provider:         cfg.ProviderName,
				Model:            req.Model,
				StepIndex:        stepIdx,
				InputFactSeqs:    durableInputFactSeqs(req.Messages),
				Messages:         providers.CloneChatMessages(req.Messages),
				Tools:            modelInputTools(req.Tools),
				ToolSurfaceHash:  requestInfo.ToolSurfaceHash,
				SystemSections:   modelInputSystemSections(cfg.SystemPromptSections),
				PromptCacheKey:   requestInfo.PromptCacheKey,
				ForceToolName:    req.ForceToolName,
				Temperature:      req.Temperature,
				Effort:           req.Effort,
				HistoryRewritten: historyRewritten,
				CreatedAt:        time.Now().UTC(),
			}
			if err := cfg.ModelInputReceiptStore.SaveModelInputReceipt(ctx, receipt); err != nil {
				return loopResultSnapshot(messages, startLen, historyRewritten, totalIn, totalOut, totalCacheCreation, totalCacheRead), fmt.Errorf("persist model input receipt: %w", err)
			}
		}

		result, err := step.Execute(ctx, req)
		lastAgentOperationID = req.Operation.ID
		if err != nil {
			// Context window exceeded — try a one-shot compaction of
			// older history and re-issue. Provider-agnostic; the
			// CompactFn carries whatever client/model knowledge it
			// needs. This is the reactive backstop for the case
			// where our proactive estimate undercounted.
			// Once visible assistant text has streamed, retrying from the original
			// request would erase that partial answer from durable history (and can
			// duplicate what the user already saw). Preserve it through the normal
			// error path below; reactive compaction is safe only before output.
			if freshContextEnabled && providers.IsContextOverflow(err) && !overflowCompacted && stepResultHasNoPartialOutput(result) {
				overflowCompacted = true
				cfg.FreshContextTokens = reactiveFreshContextTarget(cfg.FreshContextTokens,
					usage.LastSuccessfulRequestTokensForContract(requestUsageContract), estimateOutboundRequestTokens(req))
				postToolContextSegments = consumedPostToolSegments
				newContextRequested = true
				continue
			}
			if effectiveCompact != nil && providers.IsContextOverflow(err) && !overflowCompacted && stepResultHasNoPartialOutput(result) {
				overflowCompacted = true // gate first; never retry twice
				usageBefore := usage.Breakdown()
				before := usageBefore.Total()
				msgsBefore := len(messages)
				lastSuccessfulTokens := usage.LastSuccessfulRequestTokensForContract(requestUsageContract)
				failedRequestTokens := estimateOutboundRequestTokens(req)
				targetTotalTokens := reactiveCompactTarget(threshold, lastSuccessfulTokens)
				if cfg.OnCompactStart != nil {
					cfg.OnCompactStart(CompactReasonOverflow)
				}
				var compacted []providers.ChatMessage
				var cerr error
				if cfg.BeforeCompact != nil {
					if hookErr := cfg.BeforeCompact(ctx, CompactReasonOverflow); hookErr != nil {
						cerr = hookErr
					}
				}
				var lineage *providers.InferenceOperationLineage
				if cerr == nil {
					compactCtx, compactLineage := providers.BeginInferenceOperationLineage(ctx, req.Operation.ID)
					compactCtx = withCompactBudgetHint(compactCtx, compactBudgetHint{
						Reason:               CompactReasonOverflow,
						LastSuccessfulTokens: lastSuccessfulTokens,
						FailedRequestTokens:  failedRequestTokens,
						TargetTotalTokens:    targetTotalTokens,
					})
					lineage = compactLineage
					compacted, cerr = effectiveCompact(compactCtx, messages)
					if cerr == nil && compactChanged(messages, compacted) && estimateFreshContextMessages(compacted) >= estimateFreshContextMessages(messages) {
						cerr = errors.New("compaction replacement did not shrink under the local context estimator")
					}
					if cfg.AfterCompact != nil {
						if hookErr := cfg.AfterCompact(ctx, CompactReasonOverflow, cerr); hookErr != nil {
							cerr = hookErr
						}
					}
				}
				if cerr == nil {
					if compactChanged(messages, compacted) {
						noticeMessagesBefore := compactNoticeMessageCount(messages)
						noticeMessagesAfter := compactNoticeMessageCount(compacted)
						if compactOperationID := lineage.LastOperationID(); compactOperationID != "" && compactOperationID != req.Operation.ID {
							nextOperationParentID = compactOperationID
						}
						resetTranscript(compacted)
						// The failed request consumed this round's post-tool
						// context; re-queue it so the retried request still
						// carries tool-produced notices.
						postToolContextSegments = consumedPostToolSegments
						emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
							Reason:         CompactReasonOverflow,
							Status:         CompactAttemptSucceeded,
							TokensBefore:   before,
							MessagesBefore: msgsBefore,
							MessagesAfter:  len(messages),
						}, usageBefore))
						if cfg.OnCompact != nil {
							cfg.OnCompact(CompactInfo{
								Reason:         CompactReasonOverflow,
								TokensBefore:   before,
								TokensAfter:    usage.EstimateCurrent(),
								MessagesBefore: noticeMessagesBefore,
								MessagesAfter:  noticeMessagesAfter,
								Summary:        compactSummaryFromMessages(messages),
							})
						}
						continue
					}
					emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
						Reason:         CompactReasonOverflow,
						Status:         CompactAttemptUnchanged,
						TokensBefore:   before,
						MessagesBefore: msgsBefore,
						MessagesAfter:  len(compacted),
					}, usageBefore))
				} else {
					emitCompactAttempt(cfg, compactAttemptWithUsage(CompactAttemptInfo{
						Reason:         CompactReasonOverflow,
						Status:         CompactAttemptFailed,
						TokensBefore:   before,
						MessagesBefore: msgsBefore,
						Error:          cerr.Error(),
						OutputLimit:    compact.IsSummaryOutputLimit(cerr),
					}, usageBefore))
				}
			}
			// A streaming step can fail after content deltas were already shown to
			// the user. Keep that visible text in durable history, but deliberately
			// drop provider-native identity, reasoning, and tool calls because the
			// failed stream may have left those structures incomplete.
			if strings.TrimSpace(result.Content) != "" {
				appendMessage(providers.ChatMessage{
					Role:    "assistant",
					Content: result.Content,
					Phase:   result.Phase,
				})
			}
			return LoopResult{
				NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten:    historyRewritten,
				InputTokens:         totalIn,
				OutputTokens:        totalOut,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
			}, err
		}

		if freshContextEnabled {
			// Successful progress ends the previous overflow recovery attempt.
			overflowCompacted = false
		}

		if result.Usage != nil {
			totalIn += result.Usage.InputTokens
			totalOut += result.Usage.OutputTokens
			totalCacheCreation += result.Usage.CacheCreationTokens
			totalCacheRead += result.Usage.CacheReadTokens
			if cfg.OnUsage != nil {
				cfg.OnUsage(result.Usage.InputTokens, result.Usage.OutputTokens)
			}
			if cfg.OnTokenUsage != nil {
				cfg.OnTokenUsage(*result.Usage)
			}
			// Fold the precise per-call usage into the tracker. Request-only
			// context is retained and re-sent for the rest of the run, so it
			// occupies the context window like any other message and must
			// count toward the compaction estimate.
			usage.RecordResponseForContract(requestUsageContract, result.Usage)
		}
		if err := providers.ValidateAssistantToolCalls(result.ToolCalls); err != nil {
			return LoopResult{
				NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten:    historyRewritten,
				InputTokens:         totalIn,
				OutputTokens:        totalOut,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
			}, fmt.Errorf("provider returned invalid tool_calls: %w", err)
		}

		finishReason := result.FinishReason
		if finishReason == "" {
			finishReason = providers.NormalizeFinishReason(result.StopReason, result.Truncated, len(result.ToolCalls) > 0)
		}

		assistant := providers.ChatMessage{
			Role:                 "assistant",
			Content:              result.Content,
			Phase:                result.Phase,
			ProviderItemID:       result.ProviderItemID,
			ProviderItemProvider: result.ProviderItemProvider,
			ProviderItemModel:    result.ProviderItemModel,
			ReasoningContent:     result.ReasoningContent,
			ReasoningBlocks:      cloneReasoningBlocks(result.ReasoningBlocks),
			ToolCalls:            result.ToolCalls,
			FinishReason:         finishReason,
			StopReason:           result.StopReason,
			Truncated:            result.Truncated,
		}
		if shouldPersistAssistantMessage(assistant) {
			appendMessage(assistant)
		}

		// Anthropic's pause_turn pauses a long-running turn (server-side tool
		// use such as web search) and expects the conversation — including
		// the paused assistant content just appended — to be resent so the
		// model can continue. Treating it as a terminal stop silently
		// truncated those turns. The step cap still bounds repeated pauses.
		if len(result.ToolCalls) == 0 && strings.EqualFold(strings.TrimSpace(result.StopReason), "pause_turn") {
			continue
		}

		// No tool calls → model is done. Return content plus finish metadata.
		if len(result.ToolCalls) == 0 {
			finalContent := result.Content
			if strings.TrimSpace(finalContent) == "" {
				canYield := requestHasTool(req.Tools, yieldTurnToolName)
				if canYield && finishReason == providers.FinishReasonStop && !emptyAnswerRecoveryAttempted && (cfg.MaxSteps == 0 || stepIdx+1 < cfg.MaxSteps) {
					emptyAnswerRecoveryAttempted = true
					postToolContextSegments = append(postToolContextSegments, emptyAnswerRecoveryContext())
					continue
				}
				if isLegitimateEmptyCompletion(finishReason, result.StopReason) && (!canYield || finishReason == providers.FinishReasonLength) {
					return LoopResult{
						Content:             "",
						NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
						HistoryRewritten:    historyRewritten,
						InputTokens:         totalIn,
						OutputTokens:        totalOut,
						CacheCreationTokens: totalCacheCreation,
						CacheReadTokens:     totalCacheRead,
						FinishReason:        finishReason,
						StopReason:          result.StopReason,
						Truncated:           result.Truncated,
					}, nil
				}
				return LoopResult{
					NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
					HistoryRewritten:    historyRewritten,
					InputTokens:         totalIn,
					OutputTokens:        totalOut,
					CacheCreationTokens: totalCacheCreation,
					CacheReadTokens:     totalCacheRead,
				}, &EmptyAnswerError{StopReason: result.StopReason}
			}
			return LoopResult{
				Content:             finalContent,
				NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten:    historyRewritten,
				InputTokens:         totalIn,
				OutputTokens:        totalOut,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
				FinishReason:        finishReason,
				StopReason:          result.StopReason,
				Truncated:           result.Truncated,
			}, nil
		}

		if cfg.Tools == nil {
			return LoopResult{
				NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten:    historyRewritten,
				InputTokens:         totalIn,
				OutputTokens:        totalOut,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
			}, errors.New("model requested tools but none are configured")
		}

		// Execute tool calls. Read-only tools that are concurrency-safe run in
		// parallel; write tools run serially.
		//
		// The tool's execution context carries the current `messages`
		// slice (via ContextWithHistory) so tools like spawn_agent can fork
		// from the parent agent's current history.
		toolCtx := ContextWithHistory(ctx, messages)
		if cfg.ToolWaitInterrupt != nil {
			toolCtx = toolctx.WithWaitInterrupt(toolCtx, cfg.ToolWaitInterrupt())
		}
		toolRuntime := result.ToolRuntime
		if toolRuntime == nil {
			toolRuntime = NewTurnToolRuntime(ToolRuntimeConfig{Executor: cfg.Tools, StepIndex: stepIdx})
		}
		toolRuntime.SetStepIndex(stepIdx)
		toolRuntime.SetResultCallback(cfg.OnToolResultDetail)
		orderedToolMessages, toolErr := toolRuntime.ExecuteFinalCalls(toolCtx, result.ToolCalls, cfg.OnToolResult, cfg.OnToolBatchRejected)
		if toolErr != nil {
			return LoopResult{
				NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten:    historyRewritten,
				InputTokens:         totalIn,
				OutputTokens:        totalOut,
				CacheCreationTokens: totalCacheCreation,
				CacheReadTokens:     totalCacheRead,
			}, fmt.Errorf("execute tool batch: %w", toolErr)
		}
		postToolContextSegments = append(postToolContextSegments, toolRuntime.TakeRequestContextSegments()...)
		acceptedContextRequest := freshContextEnabled && acceptedNewContextRequest(orderedToolMessages)
		enforceAggregateResultBudget(orderedToolMessages)
		for _, toolMsg := range orderedToolMessages {
			appendMessage(toolMsg)
		}
		usage.RecordPendingMessages(orderedToolMessages)
		if requestHasTool(req.Tools, yieldTurnToolName) && acceptedTurnYield(orderedToolMessages) {
			return LoopResult{
				NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten: historyRewritten,
				InputTokens:      totalIn, OutputTokens: totalOut,
				CacheCreationTokens: totalCacheCreation, CacheReadTokens: totalCacheRead,
				FinishReason: providers.FinishReasonStop, StopReason: "stop",
			}, nil
		}
		if acceptedContextRequest {
			if deferRepeatedContextTransition(messages, usage.EstimateCurrent(), cfg.FreshContextTokens) {
				postToolContextSegments = append(postToolContextSegments, RequestOnlyContextMessages([]providers.ChatMessage{
					contextWindowReminder("The context-window transition was not applied: this window already contains a continuation summary and remains within the fresh-window target budget. Keep the recovered facts and continue the task; do not request another transition until the context grows. Capacity recovery remains automatic."),
				})...)
			} else {
				newContextRequested = true
			}
		}
	}

	return LoopResult{
		NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
		HistoryRewritten:    historyRewritten,
		InputTokens:         totalIn,
		OutputTokens:        totalOut,
		CacheCreationTokens: totalCacheCreation,
		CacheReadTokens:     totalCacheRead,
	}, fmt.Errorf("max steps exceeded (%d)", cfg.MaxSteps)
}

func applyArchivedHistorySeqs(messages, archived []providers.ChatMessage, seqs []int) []providers.ChatMessage {
	if len(messages) == 0 || len(archived) == 0 || len(seqs) == 0 {
		return messages
	}
	out := providers.CloneChatMessages(messages)
	searchFrom := 0
	for archivedIndex, archivedMessage := range archived {
		if archivedIndex >= len(seqs) || seqs[archivedIndex] <= 0 {
			continue
		}
		archivedMessage.Seq = 0
		for messageIndex := searchFrom; messageIndex < len(out); messageIndex++ {
			candidate := providers.CloneChatMessage(out[messageIndex])
			candidate.Seq = 0
			if !reflect.DeepEqual(candidate, archivedMessage) {
				continue
			}
			out[messageIndex].Seq = seqs[archivedIndex]
			searchFrom = messageIndex + 1
			break
		}
	}
	return out
}

func validateTransformedRequest(req providers.ChatRequest, forceBefore string, forceAvailableBefore bool) error {
	if strings.TrimSpace(req.Model) == "" {
		return errors.New("transformed model request has empty model")
	}
	if err := providers.ValidateToolCallHistory(req.Messages); err != nil {
		return fmt.Errorf("transformed model request has invalid message sequence: %w", err)
	}
	if err := providers.ValidateToolDefinitionsForProvider(providers.ToolSurfaceValidationTarget{}, req.Tools); err != nil {
		return fmt.Errorf("transformed model request has invalid tools: %w", err)
	}
	forced := strings.TrimSpace(req.ForceToolName)
	if forced == "" {
		return nil
	}
	if requestHasTool(req.Tools, forced) {
		return nil
	}
	// Preserve a pre-existing request shape that the runtime already allowed.
	// The plugin validator owns regressions introduced by the transform, not
	// unrelated legacy policy decisions made before this extension point.
	if forced == forceBefore && !forceAvailableBefore {
		return nil
	}
	return fmt.Errorf("transformed model request forces unavailable tool %q", forced)
}

func requestHasTool(tools []providers.ToolDefinition, name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func loopResultSnapshot(messages []providers.ChatMessage, startLen int, historyRewritten bool, input, output, cacheCreation, cacheRead int) LoopResult {
	return LoopResult{
		NewMessages:         newMessagesForReturn(messages, startLen, historyRewritten),
		HistoryRewritten:    historyRewritten,
		InputTokens:         input,
		OutputTokens:        output,
		CacheCreationTokens: cacheCreation,
		CacheReadTokens:     cacheRead,
	}
}

func emitCompactAttempt(cfg LoopConfig, info CompactAttemptInfo) {
	if cfg.OnCompactAttempt != nil {
		cfg.OnCompactAttempt(info)
	}
}

func compactAttemptWithUsage(info CompactAttemptInfo, usage UsageBreakdown) CompactAttemptInfo {
	info.LastResponseTotal = usage.LastResponseTotal
	info.PendingDelta = usage.PendingDelta
	info.UsageAdjustment = usage.Adjustment
	return info
}

// proactiveCompactThreshold returns the absolute token count at which
// the loop should run a proactive compact pass, or 0 if proactive
// compact is disabled (caller didn't supply a window).
func proactiveCompactThreshold(cfg LoopConfig) int {
	if cfg.MaxContextTokens <= 0 && cfg.MaxInputTokens <= 0 && cfg.CompactThresholdTokens <= 0 {
		return 0
	}
	if cfg.CompactThresholdPct > 0 && cfg.CompactThresholdPct < 1 {
		return proactiveCompactPercentThreshold(cfg, cfg.CompactThresholdPct)
	}
	// modelbudget owns the threshold formula; prefer its resolved value so
	// the trigger, trace, and UI all read the same number. The window-based
	// derivation below remains as a fallback for callers without a budget.
	if cfg.CompactThresholdTokens > 0 {
		return cfg.CompactThresholdTokens
	}
	return proactiveCompactUsableWindow(cfg)
}

func proactiveCompactUsableWindow(cfg LoopConfig) int {
	maxOutput := compactMaxOutputTokens(cfg)
	if cfg.MaxInputTokens > 0 {
		reserve := maxOutput
		if reserve > 20_000 {
			reserve = 20_000
		}
		return max(0, cfg.MaxInputTokens-reserve)
	}
	if cfg.MaxContextTokens <= 0 {
		return 0
	}
	return max(0, cfg.MaxContextTokens-maxOutput)
}

func proactiveCompactPercentThreshold(cfg LoopConfig, pct float64) int {
	baseWindow := cfg.MaxContextTokens
	if baseWindow <= 0 || (cfg.MaxInputTokens > 0 && cfg.MaxInputTokens < baseWindow) {
		baseWindow = cfg.MaxInputTokens
	}
	threshold := int(float64(baseWindow) * pct)
	if cfg.MaxInputTokens > 0 {
		inputThreshold := int(float64(cfg.MaxInputTokens) * pct)
		if inputThreshold > 0 && inputThreshold < threshold {
			threshold = inputThreshold
		}
	}
	if cfg.MaxContextTokens > 0 {
		outputReserved := cfg.MaxContextTokens - compactMaxOutputTokens(cfg)
		if outputReserved > 0 && outputReserved < threshold {
			threshold = outputReserved
		}
	}
	return threshold
}

func compactMaxOutputTokens(cfg LoopConfig) int {
	reserve := cfg.OutputReserveTokens
	if reserve <= 0 {
		reserve = cfg.DefaultMaxTokens
	}
	if reserve <= 0 {
		reserve = providers.MaxOutputTokensFor(cfg.Model)
	}
	return reserve
}

func canProactivelyCompact(messages []providers.ChatMessage, cfg LoopConfig) bool {
	return compact.CanCompactWithBudget(messages, cfg.Model, compact.Budget{
		ContextTokens:       cfg.MaxContextTokens,
		InputTokens:         cfg.MaxInputTokens,
		OutputReserveTokens: cfg.OutputReserveTokens,
		KeepRecentTokens:    cfg.CompactKeepRecentTokens,
	})
}

// resolveEffectiveCompaction picks the compaction strategy for a run.
// Note failures fall back to traditional compaction of the original transcript.
// Other providers retain their explicit ErrCompactionUnavailable opt-out.
func resolveEffectiveCompaction(cfg LoopConfig) CompactFn {
	fallback := cfg.Compact
	if cfg.CompactionRegistry == nil {
		return fallback
	}
	resolved := cfg.CompactionRegistry.Resolve(nil)
	if resolved == nil {
		return fallback
	}
	return func(ctx context.Context, messages []providers.ChatMessage) (result []providers.ChatMessage, resultErr error) {
		if forked, ok := resolved.(ForkingCompactionProvider); ok && forked.CompactionNotesEnabled() {
			original := providers.CloneChatMessages(messages)
			defer func() {
				if resultErr != nil {
					if cfg.OnCompactionNote != nil {
						cfg.OnCompactionNote("failed", resultErr)
					}
					result, resultErr = fallbackAfterNoteFailure(ctx, original, resultErr, fallback)
				}
			}()
			note, valid, err := loadValidCompactionNote(ctx, cfg.CompactionNoteStore, forked.CompactionKey(), messages)
			if err != nil {
				return nil, fmt.Errorf("load context note: %w", err)
			}
			if !valid {
				note, _, err = generateCompactionNote(
					ctx, forked, cfg.CompactionNoteStore, cfg.ForkCompactionNote, cfg.Model, messages, true,
				)
				if errors.Is(err, ErrCompactionNoteSuperseded) {
					note, valid, err = loadValidCompactionNote(ctx, cfg.CompactionNoteStore, forked.CompactionKey(), messages)
					if err == nil && !valid {
						err = ErrCompactionNoteSuperseded
					}
				}
				if errors.Is(err, ErrCompactionNoteUnsupported) {
					return resolved.Compact(ctx, cfg.Model, messages)
				}
				if err != nil {
					return nil, fmt.Errorf("forced context note: %w", err)
				}
				if cfg.OnCompactionNote != nil {
					cfg.OnCompactionNote("forced", nil)
				}
			} else if cfg.OnCompactionNote != nil {
				cfg.OnCompactionNote("context_note", nil)
			}
			replacement, err := forked.CompactWithNote(ctx, cfg.Model, messages, note)
			if err != nil {
				return nil, err
			}
			if replacement.CoveredMessages <= 0 || replacement.CoveredMessages > len(replacement.Messages) {
				return nil, fmt.Errorf("context note replacement returned invalid coverage %d for %d messages", replacement.CoveredMessages, len(replacement.Messages))
			}
			if err := providers.ValidateToolCallHistory(replacement.Messages); err != nil {
				return nil, fmt.Errorf("context note replacement: %w", err)
			}
			if estimateFreshContextMessages(replacement.Messages) >= estimateFreshContextMessages(original) {
				return nil, errors.New("context note replacement did not shrink active history")
			}
			note.CoveredMessages = replacement.CoveredMessages
			note.CoveredHash = CompactionHistoryHash(replacement.Messages[:replacement.CoveredMessages])
			if cfg.CompactionNoteStore != nil {
				if err := cfg.CompactionNoteStore.StoreCompactionNote(ctx, forked.CompactionKey(), note); err != nil {
					return nil, fmt.Errorf("re-anchor context note: %w", err)
				}
			}
			return replacement.Messages, nil
		}
		compacted, err := resolved.Compact(ctx, cfg.Model, messages)
		if err == nil || !errors.Is(err, ErrCompactionUnavailable) {
			return compacted, err
		}
		if fallback == nil {
			return messages, nil
		}
		return fallback(ctx, messages)
	}
}

func stepResultHasNoPartialOutput(result StepResult) bool {
	return strings.TrimSpace(result.Content) == "" &&
		strings.TrimSpace(result.ReasoningContent) == "" &&
		strings.TrimSpace(result.ProviderItemID) == "" &&
		len(result.ReasoningBlocks) == 0 &&
		len(result.ToolCalls) == 0 &&
		result.ToolRuntime == nil
}

func compactChanged(before, after []providers.ChatMessage) bool {
	if len(before) != len(after) {
		return true
	}
	return !reflect.DeepEqual(before, after)
}

// compactNoticeMessageCount reports the model-visible conversation units a
// user would understand as history. System scaffolding and hidden bookkeeping
// are excluded, while the hidden replacement summary counts as one message.
// This keeps note-backed compaction from reporting preserved system prompts as
// dozens of remaining conversation messages.
func compactNoticeMessageCount(messages []providers.ChatMessage) int {
	count := 0
	for _, message := range messages {
		if compact.IsConversationSummaryContent(message.Content) {
			count++
			continue
		}
		if message.Hidden || strings.EqualFold(strings.TrimSpace(message.Role), "system") || compact.IsInternalContextMessage(message) {
			continue
		}
		count++
	}
	return count
}

// compactSummaryFromMessages extracts the replacement-context summary body
// from a compacted message list. A successful pass installs at most one
// "[Conversation summary]" system message at the history head; older
// sessions can carry the bare-prefix form, which the extraction also accepts.
func compactSummaryFromMessages(msgs []providers.ChatMessage) string {
	for _, msg := range msgs {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		if compact.IsConversationSummaryContent(msg.Content) {
			return compact.SummaryBodyFromContent(msg.Content)
		}
	}
	return ""
}

// copyMessages returns an independent copy of msgs so callers can
// safely retain it after the loop's working slice is reused.
func copyMessages(msgs []providers.ChatMessage) []providers.ChatMessage {
	return providers.CloneChatMessages(msgs)
}

func repairLiveToolCallHistory(messages []providers.ChatMessage) ([]providers.ChatMessage, bool, error) {
	repaired, err := providers.RepairAndValidateToolCallHistory(messages)
	if err != nil {
		return nil, false, err
	}
	return repaired, !reflect.DeepEqual(repaired, messages), nil
}

func newMessagesForReturn(messages []providers.ChatMessage, startLen int, historyRewritten bool) []providers.ChatMessage {
	if historyRewritten {
		return copyMessages(filterTransientModelContextHistory(messages))
	}
	if startLen < 0 {
		startLen = 0
	}
	if startLen > len(messages) {
		startLen = len(messages)
	}
	return copyMessages(filterTransientModelContextHistory(messages[startLen:]))
}

func filterTransientModelContextHistory(msgs []providers.ChatMessage) []providers.ChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]providers.ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if isTransientModelContextMessage(msg) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func isTransientModelContextMessage(msg providers.ChatMessage) bool {
	if compact.IsInternalContextMessage(msg) {
		return false
	}
	name := strings.TrimSpace(msg.Name)
	switch name {
	case wuucontext.TaskContractMessageName:
		return true
	}
	if wuucontext.IsSystemReminder(name, "") {
		return true
	}
	if isLegacyHookContextMessage(msg) {
		return true
	}
	if !msg.Hidden {
		return false
	}
	return wuucontext.IsSystemReminder("", msg.Content)
}

func isLegacyHookContextMessage(msg providers.ChatMessage) bool {
	return strings.EqualFold(strings.TrimSpace(msg.Role), "user") &&
		strings.HasPrefix(strings.TrimSpace(msg.Content), "[Hook context for ")
}

func cloneReasoningBlocks(blocks []providers.ReasoningBlock) []providers.ReasoningBlock {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]providers.ReasoningBlock, len(blocks))
	copy(out, blocks)
	return out
}

func shouldPersistAssistantMessage(msg providers.ChatMessage) bool {
	if strings.TrimSpace(msg.Content) != "" {
		return true
	}
	if strings.TrimSpace(msg.ReasoningContent) != "" {
		return true
	}
	if len(msg.ReasoningBlocks) > 0 {
		return true
	}
	return len(msg.ToolCalls) > 0
}

func isLegitimateEmptyCompletion(finishReason providers.FinishReason, stopReason string) bool {
	switch finishReason {
	case providers.FinishReasonLength:
		return true
	}
	switch strings.TrimSpace(strings.ToLower(stopReason)) {
	case "end_turn":
		return true
	default:
		return false
	}
}

// errorJSON marshals an error into the JSON payload tool callers see
// when their tool execution fails. Centralized here so both runners
// produce identical error shapes.
func errorJSON(err error) string {
	message := "tool execution failed"
	if err != nil {
		message = err.Error()
	}
	payload := map[string]any{
		"ok":               false,
		"error":            message,
		"next_suggestions": toolErrorNextSuggestions(message),
	}
	kind := toolerrors.Kind(err)
	if kind == "" {
		kind = extractToolErrorKind(message)
	}
	if kind != "" {
		payload["error_kind"] = kind
	}
	b, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(b)
}

func extractToolErrorKind(message string) string {
	const marker = "error_kind="
	idx := strings.Index(message, marker)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(message[idx+len(marker):], ` "'`)
	if rest == "" {
		return ""
	}
	end := 0
	for end < len(rest) {
		ch := rest[end]
		if ch == '"' || ch == '\'' || ch == ',' || ch == ';' || ch == ')' || ch == ']' || ch == '}' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			break
		}
		end++
	}
	return strings.TrimSpace(rest[:end])
}

func toolErrorNextSuggestions(message string) []string {
	kind := extractToolErrorKind(message)
	switch kind {
	case "boundary_denied":
		return []string{"explain that the target is outside the reachable workspace boundary and ask the user to add it as a workspace if needed"}
	}
	if strings.Contains(message, "safe_retry=") {
		return []string{"follow safe_retry after refreshing the relevant file, command, or workspace evidence"}
	}
	if strings.Contains(message, "model_next_action=") {
		return []string{"follow model_next_action from the error payload"}
	}
	return []string{"inspect the error, correct the inputs or choose a safer tool, then retry only if the next step is clear"}
}

// historyContextKey is the unexported key under which RunToolLoop
// threads the current parent-agent message slice into a tool's
// execution context. Tools that support history inheritance can read
// it; everyone else can ignore it. Using an unexported zero-sized
// struct as the key guarantees no collisions with other ctx values.
type historyContextKey struct{}

// ContextWithHistory attaches a snapshot of the parent agent's current
// message history to ctx so a tool can later retrieve it via
// HistoryFromContext. RunToolLoop installs it for every tool execution;
// it is exported so tests and embedders can construct equivalent tool
// contexts. The slice is shared by reference — tools must treat it as
// read-only and copy if they need to retain it past the Execute call.
func ContextWithHistory(ctx context.Context, history []providers.ChatMessage) context.Context {
	return context.WithValue(ctx, historyContextKey{}, history)
}

// HistoryFromContext returns the parent agent's current message
// history if RunToolLoop attached one. Returns nil otherwise.
//
// Tools that read this should copy the slice if they need it past
// the Execute call: it points at the live messages slice that
// RunToolLoop is mutating.
func HistoryFromContext(ctx context.Context) []providers.ChatMessage {
	h, _ := ctx.Value(historyContextKey{}).([]providers.ChatMessage)
	return h
}

// ── Concurrency partitioning ───────────────────────────────────────
//
// consecutive read-only tools run in parallel (up to maxToolConcurrency),
// write tools run serially. This preserves ordering semantics while
// getting maximum throughput for common patterns like multiple
// concurrent reads.

const maxToolConcurrency = 10

// toolBatch groups consecutive tool calls that share a concurrency
// mode. concurrent=true means every call in the batch can run in
// parallel.
type toolBatch struct {
	calls      []providers.ToolCall
	concurrent bool
}

// partitionToolCalls groups consecutive tool calls into batches based
// on concurrency safety. If the ToolExecutor implements
// ToolMetadataProvider, we use per-tool metadata; otherwise all tools
// are treated as serial (backwards compatible).
func partitionToolCalls(executor ToolExecutor, calls []providers.ToolCall) []toolBatch {
	if len(calls) == 0 {
		return nil
	}
	// Single call — no partitioning needed.
	if len(calls) == 1 {
		return []toolBatch{{calls: calls, concurrent: false}}
	}

	mp, hasMetadata := executor.(ToolMetadataProvider)
	if !hasMetadata {
		// No metadata provider — run everything serially.
		return []toolBatch{{calls: calls, concurrent: false}}
	}

	var batches []toolBatch
	var currentCalls []providers.ToolCall
	currentConcurrent := false

	for i, call := range calls {
		meta, ok := mp.ToolMetadata(call)
		canConcur := ok && meta.ConcurrencySafe

		if i == 0 {
			currentConcurrent = canConcur
			currentCalls = []providers.ToolCall{call}
			continue
		}

		if canConcur == currentConcurrent {
			currentCalls = append(currentCalls, call)
		} else {
			batches = append(batches, toolBatch{
				calls:      currentCalls,
				concurrent: currentConcurrent,
			})
			currentCalls = []providers.ToolCall{call}
			currentConcurrent = canConcur
		}
	}
	batches = append(batches, toolBatch{
		calls:      currentCalls,
		concurrent: currentConcurrent,
	})

	return batches
}

// maxAggregateResultChars caps the total content of all tool-role messages in
// a single batch. Prevents N parallel tools x 50K each from bloating the prompt.
const maxAggregateResultChars = 200_000

// enforceAggregateResultBudget trims tool messages in-place so their total
// content stays within the aggregate budget. It trims the largest results
// first and assigns each replacement its final byte length up front. The
// marker must count against that length; repeatedly appending an unbudgeted
// marker can otherwise leave total unchanged and spin forever.
func enforceAggregateResultBudget(msgs []providers.ChatMessage) {
	total := 0
	type toolResult struct {
		index  int
		length int
	}
	results := make([]toolResult, 0, len(msgs))
	for i, m := range msgs {
		if m.Role == "tool" {
			total += len(m.Content)
			results = append(results, toolResult{index: i, length: len(m.Content)})
		}
	}
	if total <= maxAggregateResultChars {
		return
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].length > results[j].length
	})
	for _, result := range results {
		if total <= maxAggregateResultChars {
			break
		}
		original := msgs[result.index].Content
		excess := total - maxAggregateResultChars
		targetLen := len(original) - excess
		if targetLen < 0 {
			targetLen = 0
		}
		marker := fmt.Sprintf(
			"\n[trimmed: original %d chars, aggregate budget %d]",
			len(original),
			maxAggregateResultChars,
		)
		var replacement string
		switch {
		case targetLen == 0:
			replacement = ""
		case targetLen <= len(marker):
			// An unusually large batch can leave less room than the marker itself.
			// A bounded partial marker is preferable to exceeding the hard budget.
			replacement = marker[:targetLen]
		default:
			prefixLen := targetLen - len(marker)
			replacement = original[:prefixLen] + marker
		}
		msgs[result.index].Content = replacement
		total = total - len(original) + len(replacement)
	}
}

func systemReminderBlockKinds(content string) []string {
	var kinds []string
	seen := make(map[string]struct{})
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		end := strings.Index(line, "]")
		if end <= 1 {
			continue
		}
		kind := strings.TrimSpace(line[1:end])
		if kind == "" {
			continue
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		kinds = append(kinds, kind)
	}
	return kinds
}
