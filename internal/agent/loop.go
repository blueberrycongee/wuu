package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"context"

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

	var (
		totalIn, totalOut int
		// Accumulates partial assistant text across truncation-recovery
		// rounds. Concatenated into the final answer when the model
		// finally returns a non-truncated response.
		truncatedBuf         strings.Builder
		truncationRecoveries int
		// Auto-compact runs at most once per Run; if a single
		// compaction isn't enough, surfacing the error is more honest
		// than silently looping.
		overflowCompacted bool
	)

	for stepIdx := 0; cfg.MaxSteps == 0 || stepIdx < cfg.MaxSteps; stepIdx++ {
		req := providers.ChatRequest{
			Model:       cfg.Model,
			Messages:    messages,
			Temperature: cfg.Temperature,
		}
		if cfg.Tools != nil {
			req.Tools = cfg.Tools.Definitions()
		}

		result, err := step.Execute(ctx, req)
		if err != nil {
			// Context window exceeded — try a one-shot compaction of
			// older history and re-issue. Provider-agnostic; the
			// CompactFn carries whatever client/model knowledge it
			// needs.
			if cfg.Compact != nil && providers.IsContextOverflow(err) && !overflowCompacted {
				overflowCompacted = true // gate first; never retry twice
				if compacted, cerr := cfg.Compact(ctx, messages); cerr == nil {
					messages = compacted
					continue
				}
			}
			return LoopResult{
				NewMessages:  copyMessages(messages[startLen:]),
				InputTokens:  totalIn,
				OutputTokens: totalOut,
			}, err
		}

		if result.Usage != nil {
			totalIn += result.Usage.InputTokens
			totalOut += result.Usage.OutputTokens
			if cfg.OnUsage != nil {
				cfg.OnUsage(result.Usage.InputTokens, result.Usage.OutputTokens)
			}
		}

		// Output-token truncation recovery: model hit max_tokens
		// (Anthropic) / finish_reason=length (OpenAI) without finishing
		// its thought. Append the partial text, ask it to continue,
		// loop back. Tool-call rounds bypass this — those go through
		// the normal tool execution path below.
		if result.Truncated && len(result.ToolCalls) == 0 && truncationRecoveries < maxTruncationRecoveries {
			truncatedBuf.WriteString(result.Content)
			messages = append(messages, providers.ChatMessage{
				Role:    "assistant",
				Content: result.Content,
			})
			messages = append(messages, providers.ChatMessage{
				Role:    "user",
				Content: truncationContinuePrompt,
			})
			truncationRecoveries++
			continue
		}

		assistant := providers.ChatMessage{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: result.ToolCalls,
		}
		messages = append(messages, assistant)

		// No tool calls → model is done. Return concatenated content.
		if len(result.ToolCalls) == 0 {
			finalContent := truncatedBuf.String() + result.Content
			if strings.TrimSpace(finalContent) == "" {
				return LoopResult{
					NewMessages:  copyMessages(messages[startLen:]),
					InputTokens:  totalIn,
					OutputTokens: totalOut,
				}, errors.New("model returned empty answer")
			}
			return LoopResult{
				Content:      finalContent,
				NewMessages:  copyMessages(messages[startLen:]),
				InputTokens:  totalIn,
				OutputTokens: totalOut,
			}, nil
		}

		if cfg.Tools == nil {
			return LoopResult{
				NewMessages:  copyMessages(messages[startLen:]),
				InputTokens:  totalIn,
				OutputTokens: totalOut,
			}, errors.New("model requested tools but none are configured")
		}

		// Execute every requested tool call serially. Errors are
		// turned into JSON error payloads so the model can recover
		// from them on the next round instead of crashing the loop.
		for _, call := range result.ToolCalls {
			toolResult, execErr := cfg.Tools.Execute(ctx, call)
			if execErr != nil {
				toolResult = errorJSON(execErr)
			}
			if cfg.OnToolResult != nil {
				cfg.OnToolResult(call, toolResult)
			}
			messages = append(messages, providers.ChatMessage{
				Role:       "tool",
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolResult,
			})
		}
	}

	return LoopResult{
		NewMessages:  copyMessages(messages[startLen:]),
		InputTokens:  totalIn,
		OutputTokens: totalOut,
	}, fmt.Errorf("max steps exceeded (%d)", cfg.MaxSteps)
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
