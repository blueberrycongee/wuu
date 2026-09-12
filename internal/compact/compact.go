package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Compact can be the only recovery path after a provider context overflow.
// Large histories may require several summary requests, so the default must be
// long enough for recovery while still bounding a stuck compaction.
const defaultCompactTimeout = 20 * time.Minute

const (
	compactSummaryFallbackMaxTokens = 4096
	// SummaryInputMaxTokens caps each summarization request independently from
	// the provider model's advertised context window.
	SummaryInputMaxTokens        = 80_000
	compactSummaryInputMinTokens = 4_000
	compactSummaryInputFraction  = 0.5
	// Ordinary conversation text carries user requirements and engineering
	// rationale, so retain substantially more of it than tool output. Runtime
	// limits are also clamped to the current summary-input budget below.
	compactPromptMessageMaxBytes    = 80_000
	compactPromptToolResultMaxBytes = 8_000
	compactPromptToolArgsMaxBytes   = 4_000
	// Structured-only results may contain large or private producer payloads.
	// Keep using their bounded semantic index instead of copying the raw body.
	compactPromptStructuredMaxBytes = 500
)

// A length-limited summary is unusable. Retry once from the same history with
// a stricter compression instruction, then fail without replacing history.
const maxCompactLengthRetries = 1

var errCompactSummaryOutputLimit = errors.New("compact summary reached the output limit before completion")

// IsSummaryOutputLimit reports whether compaction failed because the provider
// explicitly ended a summary at its output limit.
func IsSummaryOutputLimit(err error) bool {
	return errors.Is(err, errCompactSummaryOutputLimit)
}

const compactLengthRecoveryInstruction = `

The previous summary attempt reached its output limit. Produce one complete replacement summary within the same output budget. Compress aggressively: keep only decision-critical requirements, current state, external effects, verification, and next steps. Do not mention this retry and do not end mid-section.`

const (
	// ConversationSummaryPrefix marks the synthetic summary installed after
	// compacting older conversation turns. Kept stable for persisted sessions
	// and cache-hint detection.
	ConversationSummaryPrefix = "[Conversation summary]"
	summarySectionHeader      = "Summary:"
	summaryContinuationNote   = "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation."
)

func compactTimeout() time.Duration {
	if v := os.Getenv("WUU_COMPACT_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return defaultCompactTimeout
}

func withCompactTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := compactTimeout()
	if timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining <= timeout {
			return ctx, func() {}
		}
	}
	return context.WithTimeout(ctx, timeout)
}

// EstimateTokens provides a rough token count estimate.
// English: ~4 chars per token. CJK: ~2 chars per token.
//
// Counts total runes and CJK runes in a single pass. Invalid UTF-8
// sequences yield utf8.RuneError (1 rune each) from the range loop,
// matching the behavior of utf8.RuneCountInString, so the count is
// identical to the previous two-pass implementation.
func EstimateTokens(text string) int {
	return contextbudget.EstimateTokens(text)
}

// EstimateMessagesTokens estimates total tokens for a message list.
// Counts content, reasoning, tool calls (name + arguments + envelope),
// images, and per-message overhead. Slightly pessimistic so proactive
// compact fires before the hard overflow.
func EstimateMessagesTokens(messages []providers.ChatMessage) int {
	return contextbudget.EstimateMessagesTokens(messages)
}

// ShouldCompact returns true if messages exceed the threshold.
func ShouldCompact(messages []providers.ChatMessage, maxContextTokens int) bool {
	return contextbudget.ShouldCompact(messages, maxContextTokens)
}

// maxCompactRetries caps how many times Compact will defensively trim
// the oldest message and re-issue the summarization request after
// hitting a context-overflow on the compact request itself. Aligned
// with Codex CLI's safeguard.
const maxCompactRetries = 3

// compactReservedMaxTokens reserves output headroom before deciding how much
// raw recent context can survive compaction. This mirrors OpenCode's overflow
// guard, which keeps up to 20K tokens unavailable for retained input.
const compactReservedMaxTokens = 20_000

// compactDefaultKeepRecentTokens is the recent raw history budget kept after
// compaction. It follows Pi's provider-neutral default; users can override it
// through agent.compact_keep_recent_tokens.
const compactDefaultKeepRecentTokens = 20_000

// compactTailAllFitFallbackTurns avoids no-op compaction when the configured
// tail budget is larger than the whole conversation. Normal budgeted selection
// can retain more turns; this only chooses a minimal recent tail when no budget
// boundary was reached.
const compactTailAllFitFallbackTurns = 2

// Compact compresses older messages into a summary. It finds an
// appropriate boundary near the end of the conversation, summarizes
// everything before it through the provided client's normal Chat path,
// and returns the compacted message list. Provider-specific remote
// compaction endpoints are intentionally not part of this flow; wuu owns
// the prompt, output format, and history replacement.
//
// Defensive trimming: if the summarization request itself overflows
// the model's context window (because the conversation being
// compacted is itself enormous), Compact drops the oldest entry from
// the to-be-summarized slice and retries up to maxCompactRetries
// times. This prevents the "compact → overflow → compact again →
// overflow again" deadlock the simple form is vulnerable to.
func Compact(ctx context.Context, messages []providers.ChatMessage, client providers.Client, model string) ([]providers.ChatMessage, error) {
	return CompactWithContextWindow(ctx, messages, client, model, 0)
}

func CompactWithContextWindow(ctx context.Context, messages []providers.ChatMessage, client providers.Client, model string, maxContextTokens int) ([]providers.ChatMessage, error) {
	return CompactWithBudget(ctx, messages, client, model, Budget{ContextTokens: maxContextTokens})
}

type Budget struct {
	ContextTokens       int
	InputTokens         int
	OutputReserveTokens int
	KeepRecentTokens    int
	// TargetTokens caps the model-visible durable history returned by this
	// compact pass. Zero keeps the configured/default raw-tail policy. Reactive
	// compaction sets this from a trusted model threshold or a same-contract
	// successful request so only the minimum necessary old prefix is rewritten.
	TargetTokens int
	// SummaryInputTokens is an operational chunk budget, separate from the
	// model's context/output contract. Unknown-model recovery seeds it from
	// observed requests and geometrically backs off on summary overflow.
	SummaryInputTokens int
}

// NativeOptions carries the current inference route into optional provider-
// native compaction without changing the portable summary API.
type NativeOptions struct {
	Provider                    string
	Tools                       []providers.ToolDefinition
	Temperature                 float64
	ProviderOptions             map[string]any
	MediaInput                  providers.MediaInputPolicy
	NativeDeferredToolDiscovery bool
}

// CanCompactWithBudget reports whether CompactWithBudget can replace at least
// one real conversation message with a summary. It intentionally mirrors the
// production boundary selection so callers can skip no-op auto-compact passes
// before emitting user-facing state or paying for a summary request.
func CanCompactWithBudget(messages []providers.ChatMessage, model string, budget Budget) bool {
	_, ok := planCompaction(messages, model, budget)
	return ok
}

func CompactWithBudget(ctx context.Context, messages []providers.ChatMessage, client providers.Client, model string, budget Budget) ([]providers.ChatMessage, error) {
	return CompactWithBudgetAndOptions(ctx, messages, client, model, budget, nil)
}

func CompactWithBudgetAndOptions(ctx context.Context, messages []providers.ChatMessage, client providers.Client, model string, budget Budget, options map[string]any) ([]providers.ChatMessage, error) {
	plan, ok := planCompaction(messages, model, budget)
	if !ok {
		return messages, nil
	}
	ctx, cancel := withCompactTimeout(ctx)
	defer cancel()

	toSummarize := plan.conversation[:plan.keepStart]
	toKeep := plan.conversation[plan.keepStart:]

	summary, err := summarizeCompactHistory(ctx, client, model, budget, options, toSummarize, plan.previousSummary)
	if err != nil {
		return messages, err
	}
	if summary == "" {
		return messages, nil
	}

	summaryDiscoveredTools := providers.MergeLoadableToolDefinitions(
		plan.previousSummaryDiscoveredTools,
		providers.DiscoveredToolsFromMessages(plan.conversation[:plan.keepStart]),
	)
	compacted := providers.CloneChatMessages(plan.systemPrefix)
	compacted = append(compacted, providers.ChatMessage{
		Role:            "system",
		Content:         BuildSummaryContent(summary),
		DiscoveredTools: summaryDiscoveredTools,
	})
	if plan.protectedCurrentUser != nil {
		compacted = append(compacted, providers.CloneChatMessage(*plan.protectedCurrentUser))
	}
	compacted = append(compacted, providers.CloneChatMessages(toKeep)...)
	return compacted, nil
}

// CompactWithNativeOrSummary prefers a provider-native checkpoint when the
// active client exposes that capability. Unsupported and exhausted transient
// failures fall back to the portable text summary; cancellation, authentication,
// and quota failures are returned without another provider request.
func CompactWithNativeOrSummary(ctx context.Context, messages []providers.ChatMessage, client providers.Client, model string, budget Budget, options NativeOptions) ([]providers.ChatMessage, error) {
	native, ok := client.(providers.NativeCompactor)
	if !ok || !native.NativeCompactionAvailable() {
		return CompactWithBudgetAndOptions(ctx, messages, client, model, budget, options.ProviderOptions)
	}
	req := providers.ChatRequest{
		Model:                       model,
		Provider:                    options.Provider,
		Messages:                    messages,
		Tools:                       options.Tools,
		Temperature:                 options.Temperature,
		ProviderOptions:             options.ProviderOptions,
		MediaInput:                  options.MediaInput,
		NativeDeferredToolDiscovery: options.NativeDeferredToolDiscovery,
		Operation:                   providers.EnsureInferenceOperation(providers.InferenceOperation{}, providers.InferenceOperationCompaction, providers.InferenceProfileContinuationCritical),
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if !req.Attempt.Valid() {
			req, err = providers.PrepareInferenceAttemptContext(ctx, client, req, providers.InferenceOperationCompaction, providers.InferenceProfileContinuationCritical)
			if err != nil {
				return messages, finishNativeCompaction(req, err)
			}
		}
		result, nativeErr := native.NativeCompact(ctx, req)
		if nativeErr == nil {
			if len(result.Replacement) == 0 {
				nativeErr = errors.New("native compaction returned empty replacement history")
			} else {
				if err := req.Attempt.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
					return messages, finishNativeCompaction(req, err)
				}
				if err := req.Execution.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
					return messages, err
				}
				return result.Replacement, nil
			}
		}
		err = nativeErr
		failure := providers.NormalizeFailure(err)
		outcome := providers.InferenceOutcomeFailed
		if failure.Category == providers.FailureCanceled || failure.Category == providers.FailureDeadline {
			outcome = providers.InferenceOutcomeCanceled
		}
		if attemptErr := req.Attempt.Complete(outcome, failure); attemptErr != nil {
			err = errors.Join(err, attemptErr)
			break
		}
		if errors.Is(err, providers.ErrNativeCompactionUnavailable) {
			break
		}
		if ctx.Err() != nil {
			return messages, finishNativeCompaction(req, ctx.Err())
		}
		switch failure.Category {
		case providers.FailureAuthentication, providers.FailureQuota, providers.FailureCanceled:
			return messages, finishNativeCompaction(req, err)
		}
		if !providers.IsRetryable(err) || attempt == 1 {
			break
		}
		nextAttempt, recoveryErr := req.Attempt.PrepareRecoveryAttempt(ctx, providers.PlanRecovery(failure), time.Time{})
		if recoveryErr != nil {
			err = errors.Join(err, recoveryErr)
			break
		}
		req.Attempt = nextAttempt
	}
	if req.Execution != nil {
		failure := providers.NormalizeFailure(err)
		if completeErr := req.Execution.Complete(providers.InferenceOutcomeFailed, failure); completeErr != nil {
			return messages, errors.Join(err, completeErr)
		}
	}
	providers.DebugLogf("native compaction unavailable after attempt, using portable summary: %v", err)
	return CompactWithBudgetAndOptions(ctx, messages, client, model, budget, options.ProviderOptions)
}

func finishNativeCompaction(req providers.ChatRequest, err error) error {
	if err == nil || req.Execution == nil {
		return err
	}
	failure := providers.NormalizeFailure(err)
	outcome := providers.InferenceOutcomeFailed
	if failure.Category == providers.FailureCanceled || failure.Category == providers.FailureDeadline {
		outcome = providers.InferenceOutcomeCanceled
	}
	if completeErr := req.Execution.Complete(outcome, failure); completeErr != nil {
		return errors.Join(err, completeErr)
	}
	return err
}

type compactionPlan struct {
	systemPrefix                   []providers.ChatMessage
	previousSummary                string
	previousSummaryDiscoveredTools []providers.LoadableToolDefinition
	conversation                   []providers.ChatMessage
	keepStart                      int
	protectedCurrentUser           *providers.ChatMessage
}

func planCompaction(messages []providers.ChatMessage, model string, budget Budget) (compactionPlan, bool) {
	if len(messages) <= 2 {
		return compactionPlan{}, false
	}
	systemPrefix, previousSummary, previousSummaryDiscoveredTools, conversation := splitLeadingSystemMessages(messages)
	if len(conversation) <= 2 {
		return compactionPlan{}, false
	}

	conversationForCompact := stripHistoricalImages(conversation)
	tailBudget := compactTailBudgetForBudget(model, budget)
	if budget.TargetTokens > 0 {
		tailBudget = compactTargetTailBudget(systemPrefix, budget)
	}
	keepStart := len(conversationForCompact)
	if tailBudget > 0 {
		keepStart = compactKeepStart(conversationForCompact, tailBudget)
	}
	if keepStart <= 0 || keepStart > len(conversationForCompact) {
		return compactionPlan{}, false
	}
	var protectedCurrentUser *providers.ChatMessage
	if budget.TargetTokens > 0 {
		// The newest user query is fixed request surface, even when a long
		// single-turn tool run forces us to summarize some later messages from
		// that same turn. Reinsert the raw query ahead of the retained tail.
		if currentTurnStart := lastUserMessageIndex(conversationForCompact); currentTurnStart >= 0 && keepStart > currentTurnStart {
			queryTokens := EstimateMessagesTokens(conversationForCompact[currentTurnStart : currentTurnStart+1])
			keepStart = compactKeepStart(conversationForCompact, max(1, tailBudget-queryTokens))
			cloned := providers.CloneChatMessage(conversationForCompact[currentTurnStart])
			protectedCurrentUser = &cloned
		}
	}

	return compactionPlan{
		systemPrefix:                   systemPrefix,
		previousSummary:                previousSummary,
		previousSummaryDiscoveredTools: previousSummaryDiscoveredTools,
		conversation:                   conversationForCompact,
		keepStart:                      keepStart,
		protectedCurrentUser:           protectedCurrentUser,
	}, true
}

func summarizeCompactHistory(ctx context.Context, client providers.Client, model string, budget Budget, options map[string]any, messages []providers.ChatMessage, previousSummary string) (string, error) {
	if len(messages) == 0 {
		return strings.TrimSpace(previousSummary), nil
	}
	inputBudget := compactSummaryInputBudgetForBudget(model, budget)
	summary := strings.TrimSpace(previousSummary)
	remaining := messages
	for len(remaining) > 0 {
		chunkBudget := inputBudget
		var n int
		var next string
		var err error
		for overflowAttempt := 0; ; overflowAttempt++ {
			n = compactSummaryChunkSize(remaining, summary, chunkBudget)
			if n <= 0 {
				n = 1
			}
			chunk := remaining[:n]
			next, err = summarizeCompactChunkWithInputBudget(ctx, client, model, budget, options, chunk, summary, chunkBudget, false)
			if err == nil {
				break
			}
			if !providers.IsContextOverflow(err) || overflowAttempt >= maxCompactRetries {
				return "", err
			}
			// The first operational estimate was too large for this provider/model.
			// Halve the whole chunk budget rather than silently dropping individual
			// old messages from the summary input. The successful size becomes the
			// ceiling for subsequent chunks in this pass.
			chunkBudget = max(1, chunkBudget/2)
		}
		if chunkBudget < inputBudget {
			inputBudget = chunkBudget
		}
		summary = FormatSummary(next)
		remaining = remaining[n:]
	}
	return summary, nil
}

func summarizeCompactChunk(ctx context.Context, client providers.Client, model string, budget Budget, options map[string]any, messages []providers.ChatMessage, previousSummary string) (string, error) {
	return summarizeCompactChunkWithInputBudget(
		ctx,
		client,
		model,
		budget,
		options,
		messages,
		previousSummary,
		compactSummaryInputBudgetForBudget(model, budget),
		true,
	)
}

func summarizeCompactChunkWithInputBudget(ctx context.Context, client providers.Client, model string, budget Budget, options map[string]any, messages []providers.ChatMessage, previousSummary string, inputBudget int, defensiveTrim bool) (string, error) {
	toSummarize := messages
	maxTokens := compactSummaryMaxTokensForBudget(budget)
	outputLimitCount := 0
	for overflowAttempt := 0; ; overflowAttempt++ {
		prompt := buildSummaryPromptForBudget(toSummarize, previousSummary, inputBudget)
		for {
			attemptPrompt := prompt
			if outputLimitCount > 0 {
				attemptPrompt += compactLengthRecoveryInstruction
			}
			summaryReq := compactSummaryRequest(model, attemptPrompt, maxTokens, options)
			resp, err := summarizeCompact(ctx, client, summaryReq)
			if err == nil {
				return resp.Content, nil
			}
			if errors.Is(err, errCompactSummaryOutputLimit) {
				outputLimitCount++
				if outputLimitCount <= maxCompactLengthRetries {
					continue
				}
				return "", fmt.Errorf("compact summary failed after %d output attempts with max_tokens=%d: %w", outputLimitCount, maxTokens, err)
			}
			// If the summary request itself overflowed the model's context
			// window, drop the oldest message from this chunk and try again.
			// The chunker prevents normal huge histories from reaching this
			// path; this remains a backstop for provider-specific counting.
			if defensiveTrim && providers.IsContextOverflow(err) && outputLimitCount == 0 && overflowAttempt < maxCompactRetries && len(toSummarize) > 1 {
				toSummarize = toSummarize[1:]
				break
			}
			return "", fmt.Errorf("compact summary failed: %w", err)
		}
	}
}

func compactSummaryMaxTokensForBudget(budget Budget) int {
	preferred := compactReservedMaxTokens * 4 / 5
	if budget.OutputReserveTokens <= 0 {
		return compactSummaryFallbackMaxTokens
	}
	return min(preferred, budget.OutputReserveTokens)
}

// SummaryReserveTokens returns the model-visible durable-history allowance
// reserved for a replacement summary, including its stable handoff wrapper.
// It is operational compaction budgeting, not a claim about model capability.
func SummaryReserveTokens(budget Budget) int {
	return compactSummaryMaxTokensForBudget(budget) + EstimateTokens(BuildSummaryContent(""))
}

func compactSummaryRequest(model, prompt string, maxTokens int, options map[string]any) providers.ChatRequest {
	return providers.ChatRequest{
		Model:     model,
		Operation: providers.NewInferenceOperation(providers.InferenceOperationCompaction, providers.InferenceProfileContinuationCritical),
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "You summarize coding-agent conversations for context compaction. Follow the user's required format exactly. Do not call tools."},
			{Role: "user", Content: prompt},
		},
		Temperature:     0.3,
		MaxTokens:       maxTokens,
		ProviderOptions: compactSummaryProviderOptions(options),
	}
}

func compactSummaryProviderOptions(options map[string]any) map[string]any {
	out := provideroptions.Clone(options)
	if out == nil {
		out = make(map[string]any, 1)
	}
	out["textVerbosity"] = "low"
	return out
}

func summarizeCompact(ctx context.Context, client providers.Client, req providers.ChatRequest) (providers.ChatResponse, error) {
	return streamCompactSummary(ctx, providers.AdaptStreamClient(client), req)
}

func streamCompactSummary(ctx context.Context, client providers.StreamClient, req providers.ChatRequest) (providers.ChatResponse, error) {
	req.Operation = providers.EnsureInferenceOperation(req.Operation, providers.InferenceOperationCompaction, providers.InferenceProfileContinuationCritical)
	var err error
	req, err = providers.EnsureInferenceExecutionContext(ctx, req, providers.InferenceOperationCompaction, providers.InferenceProfileContinuationCritical)
	if err != nil {
		return providers.ChatResponse{}, err
	}
	reliableClient := providers.NewReliableStreamClient(client, nil)
	ch, err := reliableClient.StreamChat(ctx, req)
	if err != nil {
		return providers.ChatResponse{}, finishCompactFailure(req.Execution, err)
	}

	var content strings.Builder
	var usage *providers.TokenUsage
	var finishReason providers.FinishReason
	stopReason := ""
	truncated := false
	done := false
	for event := range ch {
		switch event.Type {
		case providers.EventContentDelta:
			content.WriteString(event.Content)
		case providers.EventLifecycle:
			if event.Lifecycle != nil && event.Lifecycle.Phase == providers.StreamPhaseReconnecting && event.Lifecycle.ResetPartial {
				content.Reset()
				usage = nil
				finishReason = ""
				stopReason = ""
				truncated = false
				done = false
			}
		case providers.EventError:
			if event.Error != nil {
				if resp, ok, ferr := recoverCompactStream(ctx, client, req, event.Error); ok {
					return resp, ferr
				}
				return providers.ChatResponse{}, finishCompactFailure(req.Execution, event.Error)
			}
			err := errors.New("compact summary stream error")
			return providers.ChatResponse{}, finishCompactFailure(req.Execution, err)
		case providers.EventDone:
			done = true
			if event.Usage != nil {
				usage = event.Usage
			}
			finishReason = event.FinishReason
			stopReason = event.StopReason
			truncated = event.Truncated
		}
	}
	if err := ctx.Err(); err != nil {
		return providers.ChatResponse{}, finishCompactFailure(req.Execution, err)
	}
	if !done {
		err := providers.NewIncompleteStreamError("compact summary stream closed before done")
		if resp, ok, ferr := recoverCompactStream(ctx, client, req, err); ok {
			return resp, ferr
		}
		return providers.ChatResponse{}, finishCompactFailure(req.Execution, err)
	}
	if finishReason == "" {
		finishReason = providers.NormalizeFinishReason(stopReason, truncated, false)
	}
	resp := providers.ChatResponse{
		Content:      content.String(),
		Usage:        usage,
		FinishReason: finishReason,
		StopReason:   stopReason,
		Truncated:    truncated,
	}
	if err := validateCompactResponse(resp); err != nil {
		return providers.ChatResponse{}, finishCompactFailure(req.Execution, err)
	}
	if err := req.Execution.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
		return providers.ChatResponse{}, err
	}
	return resp, nil
}

func recoverCompactStream(ctx context.Context, client providers.Client, req providers.ChatRequest, streamErr error) (providers.ChatResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return providers.ChatResponse{}, true, finishCompactFailure(req.Execution, err)
	}
	if !isIncompleteCompactStream(streamErr) {
		return providers.ChatResponse{}, false, nil
	}
	priorAttempt := req.Execution.LatestAttempt()
	nextAttempt, err := priorAttempt.PrepareRecoveryAttempt(ctx, providers.RecoveryPlan{
		Action: providers.RecoverySwitchTransport,
		Reason: "compaction stream incomplete",
	}, time.Time{})
	if err != nil {
		wrapped := fmt.Errorf("record compact fallback: %w", err)
		return providers.ChatResponse{}, true, finishCompactFailure(req.Execution, wrapped)
	}
	req.Attempt = nextAttempt
	resp, executed, err := providers.ExecuteChatAttempt(ctx, client, req, req.Operation.Kind, req.Operation.WorkloadProfile)
	if err != nil {
		execution := executed.Execution
		if execution == nil {
			execution = req.Execution
		}
		wrapped := fmt.Errorf("compact summary stream incomplete and chat fallback failed: %w", err)
		return providers.ChatResponse{}, true, finishCompactFailure(execution, wrapped)
	}
	if err := validateCompactResponse(resp); err != nil {
		return providers.ChatResponse{}, true, finishCompactFailure(executed.Execution, err)
	}
	if err := executed.Execution.Complete(providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}); err != nil {
		return providers.ChatResponse{}, true, err
	}
	return resp, true, nil
}

func finishCompactFailure(execution *providers.InferenceExecution, err error) error {
	if err == nil || execution == nil {
		return err
	}
	failure := providers.NormalizeFailure(err)
	outcome := providers.InferenceOutcomeFailed
	if failure.Category == providers.FailureCanceled || failure.Category == providers.FailureDeadline {
		outcome = providers.InferenceOutcomeCanceled
	}
	if journalErr := execution.Complete(outcome, failure); journalErr != nil {
		return errors.Join(err, journalErr)
	}
	return err
}

func validateCompactResponse(resp providers.ChatResponse) error {
	finish := resp.FinishReason
	if finish == "" {
		finish = providers.NormalizeFinishReason(resp.StopReason, resp.Truncated, len(resp.ToolCalls) > 0)
	}
	if resp.Truncated || finish == providers.FinishReasonLength {
		return errCompactSummaryOutputLimit
	}
	if strings.TrimSpace(resp.Content) == "" {
		return errors.New("compact summary was empty")
	}
	return nil
}

func isIncompleteCompactStream(err error) bool {
	var streamErr *providers.StreamError
	if !errors.As(err, &streamErr) {
		return false
	}
	if streamErr.ContextOverflow || streamErr.Auth {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(streamErr.Message))
	return strings.Contains(msg, "before done") ||
		strings.Contains(msg, "before [done]") ||
		strings.Contains(msg, "before message_stop") ||
		strings.Contains(msg, "before completion") ||
		strings.Contains(msg, "before response.completed")
}

func splitLeadingSystemMessages(messages []providers.ChatMessage) ([]providers.ChatMessage, string, []providers.LoadableToolDefinition, []providers.ChatMessage) {
	i := 0
	systemPrefix := make([]providers.ChatMessage, 0)
	previousSummary := ""
	var previousSummaryDiscoveredTools []providers.LoadableToolDefinition
	for i < len(messages) && strings.EqualFold(messages[i].Role, "system") {
		msg := messages[i]
		if IsConversationSummaryContent(msg.Content) {
			previousSummary = SummaryBodyFromContent(msg.Content)
			previousSummaryDiscoveredTools = providers.MergeLoadableToolDefinitions(previousSummaryDiscoveredTools, msg.DiscoveredTools)
			i++
			continue
		}
		systemPrefix = append(systemPrefix, msg)
		i++
	}
	return systemPrefix, previousSummary, previousSummaryDiscoveredTools, messages[i:]
}

// FormatSummary turns the model's compact response into the content that will
// be replayed later. The current prompt asks for markdown only, but this still
// strips legacy XML wrappers from older compaction prompts and tests.
func FormatSummary(raw string) string {
	summary := strings.TrimSpace(raw)
	summary = stripXMLBlock(summary, "analysis")
	if extracted, ok := extractXMLBlock(summary, "summary"); ok {
		summary = extracted
	}
	summary = strings.TrimSpace(summary)
	summary = strings.ReplaceAll(summary, "\r\n", "\n")
	summary = collapseBlankLines(summary)
	return summary
}

// BuildSummaryContent wraps a cleaned summary in the stable persisted handoff
// format used by load/resume and cache-hint detection.
func BuildSummaryContent(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ConversationSummaryPrefix
	}
	return fmt.Sprintf("%s\n%s\n\n%s\n%s", ConversationSummaryPrefix, summaryContinuationNote, summarySectionHeader, summary)
}

// IsConversationSummaryContent reports whether content is a persisted compact
// summary. It accepts both the current handoff format and the older bare
// "[Conversation summary]" format for existing sessions.
func IsConversationSummaryContent(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), ConversationSummaryPrefix)
}

// SummaryBodyFromContent extracts the summary body from a persisted compact
// summary message, stripping the stable handoff wrapper. Returns "" for
// content that is not a conversation summary. This is the same extraction
// the runtime uses when re-summarizing an existing summary, and it is also
// what surfaces the compacted context to the client.
func SummaryBodyFromContent(content string) string {
	text := strings.TrimSpace(content)
	if !IsConversationSummaryContent(text) {
		return ""
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, ConversationSummaryPrefix))
	text = strings.TrimSpace(strings.TrimPrefix(text, summaryContinuationNote))
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, summarySectionHeader)
	return strings.TrimSpace(text)
}

func stripXMLBlock(text, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(text, open)
	if start < 0 {
		return text
	}
	end := strings.Index(text[start+len(open):], close)
	if end < 0 {
		return text
	}
	end += start + len(open)
	return strings.TrimSpace(text[:start] + text[end+len(close):])
}

func extractXMLBlock(text, tag string) (string, bool) {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(text, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	end := strings.Index(text[start:], close)
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(text[start : start+end]), true
}

func collapseBlankLines(text string) string {
	var b strings.Builder
	blank := false
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			b.WriteByte('\n')
			continue
		}
		blank = false
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func compactTailBudget(model string, maxContextTokens int) int {
	return compactTailBudgetForBudget(model, Budget{ContextTokens: maxContextTokens})
}

func compactSummaryInputBudgetForBudget(model string, budget Budget) int {
	if budget.SummaryInputTokens > 0 {
		if budget.SummaryInputTokens > SummaryInputMaxTokens {
			return SummaryInputMaxTokens
		}
		return budget.SummaryInputTokens
	}
	usable := compactUsableInputWindow(model, budget)
	if usable <= 0 {
		return compactSummaryInputMinTokens
	}
	inputBudget := int(float64(usable) * compactSummaryInputFraction)
	if inputBudget > SummaryInputMaxTokens {
		return SummaryInputMaxTokens
	}
	if inputBudget < compactSummaryInputMinTokens {
		return min(usable, compactSummaryInputMinTokens)
	}
	return inputBudget
}

func compactTargetTailBudget(systemPrefix []providers.ChatMessage, budget Budget) int {
	target := budget.TargetTokens
	if target <= 0 {
		return 0
	}
	target -= EstimateMessagesTokens(systemPrefix)
	// The replacement summary becomes part of durable history. Reserve its
	// maximum requested output plus the stable handoff wrapper before assigning
	// the remainder to recent raw turns.
	target -= SummaryReserveTokens(budget)
	if target < 0 {
		target = 0
	}
	if budget.KeepRecentTokens > 0 && target > budget.KeepRecentTokens {
		target = budget.KeepRecentTokens
	}
	return target
}

func compactTailBudgetForBudget(model string, budget Budget) int {
	tailBudget := compactDefaultKeepRecentTokens
	if budget.KeepRecentTokens > 0 {
		tailBudget = budget.KeepRecentTokens
	}
	if usable := compactTailUsableWindow(model, budget); usable > 0 {
		maxTail := int(float64(usable) * compactSummaryInputFraction)
		if maxTail <= 0 {
			maxTail = usable
		}
		if tailBudget > maxTail {
			return maxTail
		}
	}
	return tailBudget
}

func compactTailUsableWindow(model string, budget Budget) int {
	usable := compactUsableInputWindow(model, budget)
	if usable > 0 {
		return usable
	}
	if budget.InputTokens > 0 {
		return budget.InputTokens
	}
	return budget.ContextTokens
}

func compactUsableInputWindow(model string, budget Budget) int {
	window := budget.ContextTokens
	if window <= 0 {
		return 0
	}
	outputReserve := budget.OutputReserveTokens
	if budget.InputTokens > 0 {
		reserved := outputReserve
		if reserved > compactReservedMaxTokens {
			reserved = compactReservedMaxTokens
		}
		if reserved <= 0 {
			return budget.InputTokens
		}
		return max(0, budget.InputTokens-reserved)
	}
	if outputReserve <= 0 {
		return window
	}
	return max(0, window-outputReserve)
}

func compactSummaryChunkSize(messages []providers.ChatMessage, previousSummary string, inputBudget int) int {
	if len(messages) <= 1 {
		return len(messages)
	}
	if inputBudget <= 0 {
		inputBudget = compactSummaryInputMinTokens
	}
	limits := compactPromptLimitsForSummary(previousSummary, inputBudget)
	total := EstimateTokens(buildSummaryPromptWithLimits(nil, previousSummary, limits))
	keep := 0
	for keep < len(messages) {
		next := compactSummaryPromptMessageTokens(messages[keep], limits)
		if keep > 0 && total+next > inputBudget {
			break
		}
		total += next
		keep++
	}
	if keep == 0 {
		return 1
	}
	return compactSummaryChunkBoundary(messages, keep)
}

func compactSummaryChunkBoundary(messages []providers.ChatMessage, keep int) int {
	if keep <= 0 || keep >= len(messages) {
		return keep
	}
	// Prefer ending immediately before a user turn so each summary request sees
	// complete conversational turns. If the first turn alone exceeds the chunk
	// budget, fall back to the narrower tool-lifecycle invariant below.
	for boundary := keep; boundary > 0; boundary-- {
		if strings.EqualFold(messages[boundary].Role, "user") {
			return boundary
		}
	}

	toolSafe := adjustToolBoundary(messages, keep)
	if toolSafe > 0 {
		return toolSafe
	}
	// A single oversized turn began with an assistant tool call. Keep its whole
	// contiguous result block in one chunk; per-message prompt projection still
	// bounds large tool-result bodies.
	end := keep
	for end < len(messages) && strings.EqualFold(messages[end].Role, "tool") {
		end++
	}
	return max(1, end)
}

func compactSummaryPromptMessageTokens(msg providers.ChatMessage, limits compactPromptLimits) int {
	var b strings.Builder
	writeSummaryPromptMessageWithLimits(&b, msg, limits)
	return EstimateTokens(b.String())
}

type compactTurn struct {
	start int
	end   int
}

// compactKeepStart returns the index where the un-compacted tail should begin.
// It keeps recent user-anchored turns within the token budget instead of
// imposing a fixed turn count, matching Pi's compaction boundary shape. Long
// single-turn tool runs only have one user message at the front, so those fall
// back to a token-budgeted raw tail instead of refusing to compact. A return
// value equal to len(messages) means summarize everything and keep no raw tail;
// -1 means no compaction.
func compactKeepStart(messages []providers.ChatMessage, tailBudgetTokens int) int {
	if len(messages) <= 1 {
		return -1
	}
	if tailBudgetTokens <= 0 {
		tailBudgetTokens = compactDefaultKeepRecentTokens
	}

	turns := compactTurns(messages)
	if len(turns) == 0 {
		return compactTailStartOrSummaryAll(messages, compactFallbackTailStart(messages, tailBudgetTokens))
	}
	if len(turns) == 1 && turns[0].start == 0 {
		return compactTailStartOrSummaryAll(messages, compactFallbackTailStart(messages, tailBudgetTokens))
	}

	total := 0
	keepStart := -1
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		size := EstimateMessagesTokens(messages[turn.start:turn.end])
		if total+size <= tailBudgetTokens {
			total += size
			keepStart = turn.start
			continue
		}

		remaining := tailBudgetTokens - total
		if split, ok := compactSplitTurnStart(messages, turn, remaining); ok {
			keepStart = split
		}
		break
	}
	if keepStart <= 0 {
		if len(turns) > compactTailAllFitFallbackTurns {
			return adjustToolBoundary(messages, turns[len(turns)-compactTailAllFitFallbackTurns].start)
		}
		return len(messages)
	}
	return adjustToolBoundary(messages, keepStart)
}

func compactTailStartOrSummaryAll(messages []providers.ChatMessage, start int) int {
	start = adjustToolBoundary(messages, start)
	if start <= 0 {
		return len(messages)
	}
	return start
}

func compactTurns(messages []providers.ChatMessage) []compactTurn {
	turns := make([]compactTurn, 0)
	for i, msg := range messages {
		if !strings.EqualFold(msg.Role, "user") {
			continue
		}
		turns = append(turns, compactTurn{
			start: i,
			end:   len(messages),
		})
	}
	for i := 0; i < len(turns)-1; i++ {
		turns[i].end = turns[i+1].start
	}
	return turns
}

func compactSplitTurnStart(messages []providers.ChatMessage, turn compactTurn, tailBudgetTokens int) (int, bool) {
	if tailBudgetTokens <= 0 || turn.end-turn.start <= 1 {
		return 0, false
	}
	for start := turn.start + 1; start < turn.end; start++ {
		if EstimateMessagesTokens(messages[start:turn.end]) > tailBudgetTokens {
			continue
		}
		return adjustToolBoundary(messages, start), true
	}
	return 0, false
}

func compactFallbackTailStart(messages []providers.ChatMessage, tailBudgetTokens int) int {
	start := len(messages) - 1
	for candidate := start - 1; candidate > 0; candidate-- {
		if EstimateMessagesTokens(messages[candidate:]) > tailBudgetTokens {
			break
		}
		start = candidate
	}
	return start
}

func lastUserMessageIndex(messages []providers.ChatMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(messages[i].Role, "user") {
			return i
		}
	}
	return -1
}

func adjustToolBoundary(messages []providers.ChatMessage, start int) int {
	if start <= 0 || start >= len(messages) || !strings.EqualFold(messages[start].Role, "tool") {
		return start
	}

	// Boundary landed inside a tool-result block. Shift left to include every
	// contiguous tool result and the assistant tool_calls turn that started it.
	for start > 0 && strings.EqualFold(messages[start-1].Role, "tool") {
		start--
	}
	if start > 0 && strings.EqualFold(messages[start-1].Role, "assistant") && len(messages[start-1].ToolCalls) > 0 {
		start--
	}
	return start
}

func stripHistoricalImages(messages []providers.ChatMessage) []providers.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	latestUser := lastUserMessageIndex(messages)
	out := make([]providers.ChatMessage, len(messages))
	copy(out, messages)
	for i := range out {
		if len(out[i].Images) == 0 && len(out[i].Files) == 0 {
			continue
		}
		if i == latestUser && strings.EqualFold(out[i].Role, "user") {
			continue
		}
		out[i].Content = appendAttachmentOmissionNote(out[i].Content, out[i].Images, out[i].Files)
		out[i].Images = nil
		out[i].Files = nil
	}
	return out
}

func appendAttachmentOmissionNote(content string, images []providers.InputImage, files []providers.InputFile) string {
	if len(images) == 0 && len(files) == 0 {
		return content
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(content))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	for i, image := range images {
		if i > 0 {
			b.WriteByte('\n')
		}
		mediaType := strings.TrimSpace(image.MediaType)
		if mediaType == "" {
			mediaType = "image"
		}
		fmt.Fprintf(&b, "[Image attachment omitted from compacted history: %s, %s.]", mediaType, compactMediaEvidence(image.Data, true))
	}
	for i, file := range files {
		if len(images) > 0 || i > 0 {
			b.WriteByte('\n')
		}
		mediaType := strings.TrimSpace(file.MediaType)
		if mediaType == "" {
			mediaType = "file"
		}
		name := strings.TrimSpace(file.Filename)
		if name == "" {
			name = "file"
		}
		fmt.Fprintf(&b, "[File attachment omitted from compacted history: %s, %s, %s.]", mediaType, name, compactMediaEvidence(file.Data, false))
	}
	return b.String()
}

// compactInstructionPrompt is the framing wuu wraps every
// summarization request in. It keeps the existing single-user-message
// compact flow, but tightens the handoff discipline so the generated
// summary can safely serve as the only continuation context.
//
// The load-bearing requirements are: no tool calls, no hidden reasoning, and
// enough concrete state for the next turn to continue without the deleted
// history. Keep this prompt concise: it runs only after the normal request is
// near or over the context limit.
const compactInstructionPrompt = `You are summarizing a coding-agent conversation to preserve context for continuing the work later.

This summary is used to resume after older messages are removed. Include enough detail that the next agent can continue without asking the user to repeat context or guessing missing state.

Respond with text only. Do not call tools, request tool use, or name a tool as part of your own process. Tool calls will fail this task.

Respond with a markdown summary only. Do not include an analysis block, hidden reasoning, XML tags, preamble, or outro. Keep the summary terse but complete enough to continue the task.

Cover these sections:

## Task objective
- Current user objective and success criteria

## Constraints & Preferences
- User instructions, project rules, style constraints, and important non-goals

## Progress
- Done
- In progress
- Blocked

## External State
- Files changed, commands/processes run, browser state, remote systems touched, and any external side effects that remain current

## Verification State
- Checks passed, failed, skipped, or still needed; include exact failure status without overstating confidence

## Key Decisions
- Product or engineering decisions already made and why

## Next Steps
- Concrete next actions in order

## Critical Context
- Exact errors, commands, logs, IDs, state, assumptions, and anything easy to lose

## Evidence Pointers
- Exact files, commands, logs, result ids, artifact paths, screenshots, or other evidence needed to resume

## Relevant Files
- Exact paths and why each matters

Tone: brief a teammate taking over mid-task. Include enough detail that they can continue without asking the user to repeat anything. No filler. No emojis.
`

// buildSummaryPrompt is the inner formatting helper extracted so the
// retry loop above doesn't have to duplicate the string-builder code.
func buildSummaryPrompt(toSummarize []providers.ChatMessage, previousSummary string) string {
	return buildSummaryPromptWithLimits(toSummarize, previousSummary, compactPromptLimitsForInputBudget(0))
}

func buildSummaryPromptForBudget(toSummarize []providers.ChatMessage, previousSummary string, inputBudget int) string {
	return buildSummaryPromptWithLimits(toSummarize, previousSummary, compactPromptLimitsForSummary(previousSummary, inputBudget))
}

type compactPromptLimits struct {
	messageBytes    int
	toolResultBytes int
	toolArgsBytes   int
}

func compactPromptLimitsForInputBudget(inputBudget int) compactPromptLimits {
	messageBytes := compactPromptMessageMaxBytes
	if inputBudget > 0 {
		// A conservative three-byte allowance per input token leaves room for
		// framing and the anchored summary while still retaining far more than
		// the old fixed 500-byte projection on small context windows.
		budgetBytes := inputBudget * 3
		if budgetBytes < messageBytes {
			messageBytes = budgetBytes
		}
	}
	if messageBytes < 1 {
		messageBytes = 1
	}
	return compactPromptLimits{
		messageBytes:    messageBytes,
		toolResultBytes: min(compactPromptToolResultMaxBytes, messageBytes),
		toolArgsBytes:   min(compactPromptToolArgsMaxBytes, messageBytes),
	}
}

func compactPromptLimitsForSummary(previousSummary string, inputBudget int) compactPromptLimits {
	if inputBudget <= 0 {
		return compactPromptLimitsForInputBudget(0)
	}
	baseTokens := EstimateTokens(buildSummaryPromptWithLimits(nil, previousSummary, compactPromptLimitsForInputBudget(0)))
	return compactPromptLimitsForInputBudget(max(1, inputBudget-baseTokens))
}

func buildSummaryPromptWithLimits(toSummarize []providers.ChatMessage, previousSummary string, limits compactPromptLimits) string {
	var b strings.Builder
	b.WriteString(compactInstructionPrompt)
	previousSummary = strings.TrimSpace(previousSummary)
	if previousSummary != "" {
		b.WriteString("\n--- Previous anchored summary ---\n\n")
		b.WriteString(previousSummary)
		b.WriteString("\n\nUpdate the anchored summary above using the new conversation below. Preserve details that are still true, remove stale details, and merge new facts. Return one complete replacement summary with the required sections.\n\n")
	}
	b.WriteString("--- Conversation to summarize ---\n\n")
	for _, msg := range toSummarize {
		writeSummaryPromptMessageWithLimits(&b, msg, limits)
	}
	return b.String()
}

func writeSummaryPromptMessageWithLimits(b *strings.Builder, msg providers.ChatMessage, limits compactPromptLimits) {
	content := msg.Content
	if msg.ToolResult != nil && len(msg.ToolResult.Content) == 0 && len(strings.TrimSpace(string(msg.ToolResult.StructuredContent))) > compactPromptStructuredMaxBytes {
		content = "[Structured tool result omitted from summary body; see semantic index below.]"
	}
	contentLimit := limits.messageBytes
	if msg.ToolResult != nil || strings.EqualFold(msg.Role, "tool") {
		contentLimit = limits.toolResultBytes
	}
	fmt.Fprintf(b, "[%s]: %s\n", msg.Role, compactPromptExcerpt(content, contentLimit))
	writeSummaryPromptToolResultIndex(b, msg.ToolResult)
	for _, image := range msg.Images {
		mediaType := strings.TrimSpace(image.MediaType)
		if mediaType == "" {
			mediaType = "image"
		}
		fmt.Fprintf(b, "  [image omitted: %s, %s]\n", mediaType, compactMediaEvidence(image.Data, true))
	}
	for _, file := range msg.Files {
		mediaType := strings.TrimSpace(file.MediaType)
		if mediaType == "" {
			mediaType = "file"
		}
		name := strings.TrimSpace(file.Filename)
		if name == "" {
			name = "file"
		}
		fmt.Fprintf(b, "  [file omitted: %s, %s, %s]\n", mediaType, name, compactMediaEvidence(file.Data, false))
	}
	for _, tc := range msg.ToolCalls {
		fmt.Fprintf(b, "  -> tool_call: %s(%s)\n", tc.Name, compactPromptExcerpt(tc.Arguments, limits.toolArgsBytes))
	}
	if msg.ToolCallID != "" {
		fmt.Fprintf(b, "  (result for tool call %s)\n", msg.ToolCallID)
	}
	b.WriteString("\n")
}

func writeSummaryPromptToolResultIndex(b *strings.Builder, result *toolresult.Result) {
	if result == nil {
		return
	}
	writeSummaryPromptStructuredResultIndex(b, result.StructuredContent)
	for _, part := range result.Content {
		switch part.Type {
		case toolresult.ContentTypeImage, toolresult.ContentTypeAudio, toolresult.ContentTypeFile:
			name := strings.TrimSpace(part.Name)
			if name == "" {
				name = part.Type
			}
			mediaType := strings.TrimSpace(part.MIMEType)
			if mediaType == "" {
				mediaType = part.Type
			}
			if uri := strings.TrimSpace(part.URI); uri != "" {
				fmt.Fprintf(b, "  [tool %s index: %s, %s, uri=%s]\n", part.Type, name, mediaType, truncate(uri, compactPromptToolArgsMaxBytes))
			} else {
				fmt.Fprintf(b, "  [tool %s index: %s, %s, %s]\n", part.Type, name, mediaType, compactMediaEvidence(part.Data, part.Type == toolresult.ContentTypeImage))
			}
		case toolresult.ContentTypeResource:
			name := strings.TrimSpace(part.Name)
			if name == "" {
				name = "resource"
			}
			fmt.Fprintf(b, "  [tool resource index: %s, %d JSON characters]\n", name, len(strings.TrimSpace(string(part.Resource))))
		case toolresult.ContentTypeResourceLink:
			name := strings.TrimSpace(part.Name)
			if name == "" {
				name = "resource"
			}
			fmt.Fprintf(b, "  [tool resource link: %s, uri=%s]\n", name, truncate(strings.TrimSpace(part.URI), compactPromptToolArgsMaxBytes))
		}
	}
	if result.Activity != nil {
		fmt.Fprintf(b, "  [tool activity index: kind=%s, id=%s, state=%s, preview=%s]\n",
			truncate(strings.TrimSpace(result.Activity.Kind), compactPromptToolArgsMaxBytes),
			truncate(strings.TrimSpace(result.Activity.ID), compactPromptToolArgsMaxBytes),
			truncate(strings.TrimSpace(result.Activity.State), compactPromptToolArgsMaxBytes),
			truncate(strings.TrimSpace(result.Activity.PreviewURI), compactPromptToolArgsMaxBytes),
		)
	}
}

func compactPromptExcerpt(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	if maxBytes <= 0 {
		return fmt.Sprintf("[content omitted from compact summary input; original_bytes=%d]", len(text))
	}
	headBytes := maxBytes * 3 / 4
	tailBytes := maxBytes - headBytes
	marker := fmt.Sprintf("\n\n[... middle omitted from compact summary input; original_bytes=%d ...]\n\n", len(text))
	return stringutil.HeadTail(text, headBytes, tailBytes, marker)
}

func writeSummaryPromptStructuredResultIndex(b *strings.Builder, raw json.RawMessage) {
	if index := toolresult.StructuredContentIndexJSON(raw); index != "" {
		fmt.Fprintf(b, "  [tool structured result index: %s]\n", index)
	}
}

func truncate(s string, maxLen int) string {
	return stringutil.Truncate(s, maxLen, "...")
}
