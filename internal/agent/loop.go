package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// maxTruncationRecoveries caps how many times the loop will ask the
// model to keep going after hitting its output token cap. Aligned with
// Claude Code's MAX_OUTPUT_TOKENS_RECOVERY_LIMIT.
const maxTruncationRecoveries = 3

// truncationContinuePrompt is sent after the model is cut off by its
// output token limit. Lifted verbatim from Claude Code's recovery flow
// — terse and emphatic so the model resumes mid-thought instead of
// re-introducing the topic.
const truncationContinuePrompt = "Output token limit hit. Resume directly — no apology, no recap of what you were doing. Pick up mid-thought if that is where the cut happened. Break remaining work into smaller pieces."

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

// RunToolLoop drives the shared multi-step tool-use loop both Runner
// and StreamRunner depend on. It is transport-agnostic: callers
// supply a Step that knows how to perform one model round-trip
// (Chat for Runner, SSE consumption for StreamRunner) and a
// LoopConfig describing the rest.
//
// Behavior:
//   - Loops up to cfg.MaxSteps rounds (0 = unlimited).
//   - On context-overflow errors from the step, calls cfg.Compact
//     once and re-issues the step. Subsequent overflows propagate.
//   - On output truncation (StepResult.Truncated with no tool calls),
//     appends a "continue" prompt and re-issues, capped at
//     maxTruncationRecoveries attempts. The accumulated partial
//     content is concatenated into the final result.
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
) (LoopResult, error) {
	if step == nil {
		return LoopResult{}, errors.New("agent: step is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return LoopResult{}, errors.New("agent: model is required")
	}

	messages := make([]providers.ChatMessage, len(history))
	copy(messages, history)
	startLen := len(messages)

	// Output token escalation: start with DefaultMaxTokens, escalate
	// to EscalatedMaxTokens after the first truncation recovery.
	// Aligned with Claude Code's "start low, escalate on truncation".
	const defaultEscalatedMaxTokens = 65536
	currentMaxTokens := cfg.DefaultMaxTokens // 0 = provider default

	var (
		totalIn, totalOut int
		// Accumulates partial assistant text across truncation-recovery
		// rounds. Concatenated into the final answer when the model
		// finally returns a non-truncated response.
		truncatedBuf         strings.Builder
		truncationRecoveries int
		// Reactive auto-compact (overflow recovery) runs at most once
		// per Run; if a single compaction isn't enough, surfacing the
		// error is more honest than silently looping. Proactive
		// compact is allowed to run multiple times per Run since each
		// pass shrinks the conversation and the next API call's usage
		// gives us a fresh ground truth.
		overflowCompacted bool
		historyRewritten  bool
		// Tracks current context fill so we can decide whether to
		// proactively compact before the next round. Uses
		// response.usage as ground truth + delta estimation for
		// messages added since the last successful response.
		usage = cfg.UsageTracker
	)
	if usage == nil {
		usage = NewUsageTracker()
		// Without caller-owned cross-turn state, seed this run from a
		// local estimate. Pre-request proactive compact still waits
		// for a real response.usage baseline before firing.
		usage.RecordPendingMessages(history)
	}
	threshold := proactiveCompactThreshold(cfg)
	appendMessage := func(msg providers.ChatMessage) {
		messages = append(messages, msg)
		if cfg.OnMessage != nil {
			cfg.OnMessage(msg)
		}
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
		if cfg.Compact != nil && threshold > 0 && usage.HasGroundTruth() && usage.EstimateCurrent() >= threshold {
			before := usage.EstimateCurrent()
			msgsBefore := len(messages)
			if compacted, cerr := cfg.Compact(ctx, messages); cerr == nil && len(compacted) < len(messages) {
				messages = compacted
				historyRewritten = true
				usage.Reset()
				usage.RecordPendingMessages(messages)
				if cfg.OnCompact != nil {
					cfg.OnCompact(CompactInfo{
						Reason:         CompactReasonProactive,
						TokensBefore:   before,
						MessagesBefore: msgsBefore,
						MessagesAfter:  len(messages),
					})
				}
			}
		}
		req := providers.ChatRequest{
			Model:       cfg.Model,
			Messages:    messages,
			Temperature: cfg.Temperature,
			MaxTokens:   currentMaxTokens,
			CacheHint:   buildCacheHint(messages),
			Effort:      cfg.Effort,
		}
		if cfg.Tools != nil {
			req.Tools = cfg.Tools.Definitions()
		}

		result, err := step.Execute(ctx, req)
		if err != nil {
			// Context window exceeded — try a one-shot compaction of
			// older history and re-issue. Provider-agnostic; the
			// CompactFn carries whatever client/model knowledge it
			// needs. This is the reactive backstop for the case
			// where our proactive estimate undercounted.
			if cfg.Compact != nil && providers.IsContextOverflow(err) && !overflowCompacted {
				overflowCompacted = true // gate first; never retry twice
				before := usage.EstimateCurrent()
				msgsBefore := len(messages)
				if compacted, cerr := cfg.Compact(ctx, messages); cerr == nil {
					messages = compacted
					historyRewritten = true
					usage.Reset()
					usage.RecordPendingMessages(messages)
					if cfg.OnCompact != nil {
						cfg.OnCompact(CompactInfo{
							Reason:         CompactReasonOverflow,
							TokensBefore:   before,
							MessagesBefore: msgsBefore,
							MessagesAfter:  len(messages),
						})
					}
					continue
				}
			}
			return LoopResult{
				NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten: historyRewritten,
				InputTokens:      totalIn,
				OutputTokens:     totalOut,
			}, err
		}

		if result.Usage != nil {
			totalIn += result.Usage.InputTokens
			totalOut += result.Usage.OutputTokens
			if cfg.OnUsage != nil {
				cfg.OnUsage(result.Usage.InputTokens, result.Usage.OutputTokens)
			}
			// Fold the precise per-call usage into the tracker. This
			// collapses any pending estimate into ground truth.
			usage.RecordResponse(result.Usage)
		}

		// Output-token truncation recovery: model hit max_tokens
		// (Anthropic) / finish_reason=length (OpenAI) without finishing
		// its thought. Append the partial text, ask it to continue,
		// loop back. Tool-call rounds bypass this — those go through
		// the normal tool execution path below.
		if result.Truncated && len(result.ToolCalls) == 0 && truncationRecoveries < maxTruncationRecoveries {
			truncatedBuf.WriteString(result.Content)
			appendMessage(providers.ChatMessage{
				Role:             "assistant",
				Content:          result.Content,
				ReasoningContent: result.ReasoningContent,
				ReasoningBlocks:  cloneReasoningBlocks(result.ReasoningBlocks),
			})
			appendMessage(providers.ChatMessage{
				Role:    "user",
				Content: truncationContinuePrompt,
			})
			// Escalate output token cap on first truncation so the
			// continuation has room to finish. Matches Claude Code's
			// "start low (16K), escalate to 64K on truncation" pattern.
			if truncationRecoveries == 0 {
				esc := cfg.EscalatedMaxTokens
				if esc <= 0 {
					esc = defaultEscalatedMaxTokens
				}
				currentMaxTokens = esc
			}
			truncationRecoveries++
			continue
		}

		assistant := providers.ChatMessage{
			Role:             "assistant",
			Content:          result.Content,
			ReasoningContent: result.ReasoningContent,
			ReasoningBlocks:  cloneReasoningBlocks(result.ReasoningBlocks),
			ToolCalls:        result.ToolCalls,
		}
		if shouldPersistAssistantMessage(assistant) {
			appendMessage(assistant)
		}

		// No tool calls → model is done. Return concatenated content.
		if len(result.ToolCalls) == 0 {
			finalContent := truncatedBuf.String() + result.Content
			if strings.TrimSpace(finalContent) == "" {
				if isLegitimateEmptyCompletion(result.StopReason) {
					return LoopResult{
						Content:          "",
						NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
						HistoryRewritten: historyRewritten,
						InputTokens:      totalIn,
						OutputTokens:     totalOut,
					}, nil
				}
				return LoopResult{
					NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
					HistoryRewritten: historyRewritten,
					InputTokens:      totalIn,
					OutputTokens:     totalOut,
				}, &EmptyAnswerError{StopReason: result.StopReason}
			}
			return LoopResult{
				Content:          finalContent,
				NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten: historyRewritten,
				InputTokens:      totalIn,
				OutputTokens:     totalOut,
			}, nil
		}

		if cfg.Tools == nil {
			return LoopResult{
				NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
				HistoryRewritten: historyRewritten,
				InputTokens:      totalIn,
				OutputTokens:     totalOut,
			}, errors.New("model requested tools but none are configured")
		}

		// Execute tool calls. Read-only tools that are concurrency-
		// safe run in parallel; write tools run serially. This is
		// aligned with Claude Code's partitionToolCalls architecture.
		//
		// The tool's execution context carries the current `messages`
		// slice (via withHistory) so tools that need to read what the
		// parent agent has done so far — fork_agent in particular —
		// can extract it via HistoryFromContext. Sub-agent loops do
		// not inject this key, which is how worker isolation for
		// fork_agent stays enforced without a separate gate.
		toolCtx := withHistory(ctx, messages)
		batches := partitionToolCalls(cfg.Tools, result.ToolCalls)
		for _, batch := range batches {
			toolMessages := executeBatch(toolCtx, cfg.Tools, batch, cfg.OnToolResult, result.PrecomputedResults)
			enforceAggregateResultBudget(toolMessages)
			for _, toolMsg := range toolMessages {
				appendMessage(toolMsg)
			}
			usage.RecordPendingMessages(toolMessages)
		}
	}

	return LoopResult{
		NewMessages:      newMessagesForReturn(messages, startLen, historyRewritten),
		HistoryRewritten: historyRewritten,
		InputTokens:      totalIn,
		OutputTokens:     totalOut,
	}, fmt.Errorf("max steps exceeded (%d)", cfg.MaxSteps)
}

// proactiveCompactThreshold returns the absolute token count at which
// the loop should run a proactive compact pass, or 0 if proactive
// compact is disabled (caller didn't supply a window).
func proactiveCompactThreshold(cfg LoopConfig) int {
	if cfg.MaxContextTokens <= 0 {
		return 0
	}
	pct := cfg.CompactThresholdPct
	if pct <= 0 || pct >= 1 {
		pct = defaultCompactThresholdPct
	}
	return int(float64(cfg.MaxContextTokens) * pct)
}

// copyMessages returns an independent copy of msgs so callers can
// safely retain it after the loop's working slice is reused.
func copyMessages(msgs []providers.ChatMessage) []providers.ChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]providers.ChatMessage, len(msgs))
	copy(out, msgs)
	return out
}

func newMessagesForReturn(messages []providers.ChatMessage, startLen int, historyRewritten bool) []providers.ChatMessage {
	if historyRewritten {
		return copyMessages(messages)
	}
	if startLen < 0 {
		startLen = 0
	}
	if startLen > len(messages) {
		startLen = len(messages)
	}
	return copyMessages(messages[startLen:])
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

func isLegitimateEmptyCompletion(stopReason string) bool {
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
	payload := map[string]any{"error": err.Error()}
	b, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(b)
}

// historyContextKey is the unexported key under which RunToolLoop
// threads the current parent-agent message slice into a tool's
// execution context. Only fork_agent reads this; everyone else can
// ignore it. Using an unexported zero-sized struct as the key
// guarantees no collisions with other ctx values.
type historyContextKey struct{}

// withHistory attaches a snapshot of the parent agent's current
// message history to ctx so a tool can later retrieve it via
// HistoryFromContext. The slice is shared by reference — tools must
// treat it as read-only and copy if they need to retain it past the
// Execute call.
func withHistory(ctx context.Context, history []providers.ChatMessage) context.Context {
	return context.WithValue(ctx, historyContextKey{}, history)
}

// HistoryFromContext returns the parent agent's current message
// history if RunToolLoop attached one (i.e. the tool is being called
// from the main interactive loop). Returns nil otherwise — sub-agent
// loops do not attach a history, which is how fork_agent's "main
// agent only" gate stays enforced without an extra check elsewhere.
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
// Aligned with Claude Code's partitionToolCalls / runToolsConcurrently:
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
		meta, ok := mp.ToolMetadata(call.Name)
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

// executeBatch runs a batch of tool calls. Concurrent batches run up
// to maxToolConcurrency calls in parallel; serial batches run each
// call in order. Results are returned in the original call order.
//
// precomputed, when non-nil, contains results for tools that were
// already executed during streaming. These are used as-is and the
// tool is not re-executed.
func executeBatch(
	ctx context.Context,
	executor ToolExecutor,
	batch toolBatch,
	onResult func(providers.ToolCall, string),
	precomputed map[string]string,
) []providers.ChatMessage {
	// Check if the executor supports additional context injection
	// (e.g. HookedExecutor propagating PostToolUse hook context).
	ctxProvider, hasCtxProvider := executor.(ToolContextProvider)

	if !batch.concurrent || len(batch.calls) == 1 {
		// Serial execution.
		msgs := make([]providers.ChatMessage, 0, len(batch.calls))
		for _, call := range batch.calls {
			// Check for precomputed results from streaming tool execution.
			result, ok := precomputed[call.ID]
			if !ok {
				var err error
				result, err = executor.Execute(ctx, call)
				if err != nil {
					result = errorJSON(err)
				}
			}
			if onResult != nil {
				onResult(call, result)
			}
			msgs = append(msgs, providers.ChatMessage{
				Role:       "tool",
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    result,
			})
			// Inject hook-provided additional context as a system
			// message right after the tool result, so the model sees
			// it in context. Aligned with Claude Code's
			// hook_additional_context attachment pattern.
			if hasCtxProvider {
				if extra := ctxProvider.LastAdditionalContext(); extra != "" {
					msgs = append(msgs, providers.ChatMessage{
						Role:    "user",
						Content: "[Hook context for " + call.Name + "]: " + extra,
					})
				}
			}
		}
		return msgs
	}

	// Concurrent execution with bounded parallelism.
	type indexedResult struct {
		idx    int
		result string
	}
	results := make([]indexedResult, len(batch.calls))
	sem := make(chan struct{}, maxToolConcurrency)
	var wg sync.WaitGroup

	for i, call := range batch.calls {
		wg.Add(1)
		go func(idx int, c providers.ToolCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			res, err := executor.Execute(ctx, c)
			if err != nil {
				res = errorJSON(err)
			}
			if onResult != nil {
				onResult(c, res)
			}
			results[idx] = indexedResult{idx: idx, result: res}
		}(i, call)
	}
	wg.Wait()

	msgs := make([]providers.ChatMessage, len(batch.calls))
	for i, call := range batch.calls {
		msgs[i] = providers.ChatMessage{
			Role:       "tool",
			Name:       call.Name,
			ToolCallID: call.ID,
			Content:    results[i].result,
		}
	}
	return msgs
}

// maxAggregateResultChars caps the total content of all tool-role
// messages in a single batch. Prevents N parallel tools × 50K each
// from bloating the prompt. Aligned with Claude Code's per-message
// 200K aggregate budget.
const maxAggregateResultChars = 200_000

// enforceAggregateResultBudget trims tool messages in-place so their
// total content stays within the aggregate budget. Trims the largest
// results first to minimize information loss.
func enforceAggregateResultBudget(msgs []providers.ChatMessage) {
	total := 0
	for _, m := range msgs {
		if m.Role == "tool" {
			total += len(m.Content)
		}
	}
	if total <= maxAggregateResultChars {
		return
	}
	// Trim the longest tool results first.
	for total > maxAggregateResultChars {
		maxIdx, maxLen := -1, 0
		for i, m := range msgs {
			if m.Role == "tool" && len(m.Content) > maxLen {
				maxIdx = i
				maxLen = len(m.Content)
			}
		}
		if maxIdx < 0 || maxLen <= 200 {
			break
		}
		excess := total - maxAggregateResultChars
		newLen := maxLen - excess
		if newLen < 200 {
			newLen = 200
		}
		msgs[maxIdx].Content = msgs[maxIdx].Content[:newLen] +
			fmt.Sprintf("\n[trimmed: original %d chars, aggregate budget %d]", maxLen, maxAggregateResultChars)
		total = total - maxLen + len(msgs[maxIdx].Content)
	}
}
