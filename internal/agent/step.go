// Package agent: Step is the transport-agnostic abstraction the
// shared tool-use loop drives. Both Runner and StreamRunner execute
// through Step (Runner adapts providers.Client to StreamClient first),
// so the actual loop logic — step counting, tool execution,
// truncation recovery, context-overflow auto-compact — lives in
// exactly one place. See loop.go.
package agent

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// StepResult is the outcome of one model round-trip, normalized so
// the loop doesn't care whether the response came from a one-shot
// Chat or a fully-consumed SSE stream.
type StepResult struct {
	// Content is the assistant's text for this round (concatenation
	// of all content deltas in the streaming case).
	Content string
	// ReasoningContent is provider-emitted hidden reasoning for this
	// round. Some OpenAI-compatible providers require replaying it
	// verbatim on follow-up assistant tool-call messages.
	ReasoningContent string
	// ToolCalls is the ordered list of tool invocations the model
	// requested in this round, fully assembled (arguments included).
	ToolCalls []providers.ToolCall
	// Truncated is true when the provider signalled an output-token
	// cap (Anthropic stop_reason=max_tokens, OpenAI finish_reason=
	// length). The loop uses this to drive the "continue" recovery.
	Truncated bool
	// StopReason is the lowercase normalized stop signal. Surfaced
	// for diagnostics; the loop's behavior is driven by Truncated.
	StopReason string
	// Usage is the per-round token consumption when the provider
	// reports it. nil is allowed.
	Usage *providers.TokenUsage
}

// Step performs exactly one model round-trip and returns the result
// in normalized form. Implementations encapsulate the transport
// (one-shot vs streaming) and any per-round live-rendering side
// effects; the loop above doesn't observe those.
type Step interface {
	Execute(ctx context.Context, req providers.ChatRequest) (StepResult, error)
}

// CompactFn compresses a conversation that has overflowed the model's
// context window. The loop calls it once when the underlying step
// returns a context-overflow error, then re-issues the same step.
//
// Implementations are expected to wrap whatever provider-side
// summarization they need; the loop is intentionally agnostic.
type CompactFn func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error)

// CompactReason classifies why the loop ran a compact pass.
type CompactReason string

const (
	// CompactReasonProactive means the loop hit its proactive
	// fill-rate threshold (CompactThresholdPct of MaxContextTokens)
	// and ran a compact preemptively to avoid overflow.
	CompactReasonProactive CompactReason = "proactive"
	// CompactReasonOverflow means a step.Execute returned a
	// context-overflow error and the loop ran compact reactively as
	// the recovery path.
	CompactReasonOverflow CompactReason = "overflow"
)

// CompactInfo describes a compact pass that just ran. Surfaced via
// LoopConfig.OnCompact so callers (e.g. the TUI) can let the user
// know what just happened.
type CompactInfo struct {
	Reason         CompactReason
	TokensBefore   int
	MessagesBefore int
	MessagesAfter  int
}

// LoopConfig bundles every knob the shared loop needs. All callbacks
// are optional. Tools is required if the model is allowed to call any.
type LoopConfig struct {
	// Tools is the executor used to run model-requested tool calls.
	// May be nil if the caller knows the model has no tools available.
	Tools ToolExecutor
	// Model is the model identifier passed through to the provider.
	Model string
	// Temperature is the sampling temperature; 0 means provider default.
	Temperature float64
	// MaxSteps caps the number of model round-trips per Run. Zero
	// means unlimited (aligned with Claude Code's default), positive
	// values act as a runaway safety net.
	MaxSteps int
	// Compact is invoked when the loop wants to summarize the older
	// conversation (proactive fill-rate trigger or reactive
	// context-overflow recovery). nil disables both auto-compact
	// paths; the overflow error is propagated to the caller as-is.
	Compact CompactFn
	// MaxContextTokens is the model's context window. When non-zero,
	// the loop tracks usage from response.usage and proactively
	// triggers a compact pass once the conversation exceeds
	// CompactThresholdPct of this value. Zero disables proactive
	// compact (the reactive overflow path still works).
	MaxContextTokens int
	// CompactThresholdPct is the fraction of MaxContextTokens that
	// triggers a proactive compact. Defaults to 0.9 (90%) when zero.
	// Aligned with Codex CLI's auto_compact_token_limit default.
	CompactThresholdPct float64
	// BeforeStep, when set, is called at the start of each model
	// round. Any returned messages are appended to the live history
	// before the next provider request is built. This is used by
	// sub-agent follow-up messaging: send_message_to_agent queues
	// user-role messages that are injected on the next round.
	BeforeStep func() []providers.ChatMessage
	// OnUsage is invoked once per LLM round-trip with the per-call
	// token counts when the provider reports them. The loop also
	// accumulates totals into LoopResult.
	OnUsage func(input, output int)
	// OnMessage is invoked whenever the loop appends a semantic chat
	// message to its live history. Streaming callers use it to persist
	// assistant/tool/internal follow-up messages incrementally instead of
	// waiting for the whole turn to finish.
	OnMessage func(msg providers.ChatMessage)
	// OnToolResult is invoked after each tool execution with the
	// (call, JSON result) pair. Used by streaming callers to feed
	// live tool-result rendering into the TUI.
	OnToolResult func(call providers.ToolCall, result string)
	// OnCompact is invoked once per compact pass (proactive or
	// reactive). Optional; the TUI uses it to render a status line.
	OnCompact func(info CompactInfo)
	// UsageTracker, when non-nil, is the caller-owned conversation
	// usage state to reuse across runs. This lets the loop make the
	// same compact decision before the first request of a new turn
	// that it would make mid-run after receiving fresh usage.
	UsageTracker *UsageTracker

	// DefaultMaxTokens is the output token cap sent on every request.
	// Zero means the provider's default (e.g. 16 384 for Anthropic).
	// Aligned with Claude Code's initial max_tokens.
	DefaultMaxTokens int
	// EscalatedMaxTokens is the output token cap used after the first
	// truncation recovery. Zero defaults to 65 536. Aligned with
	// Claude Code's "start low, escalate on truncation" strategy.
	EscalatedMaxTokens int
}

// defaultCompactThresholdPct is the proactive trigger if the caller
// didn't set one. 90% matches Codex CLI; CC uses an effectively
// equivalent "window − 13k" buffer.
const defaultCompactThresholdPct = 0.90

// LoopResult is what RunToolLoop returns on success.
type LoopResult struct {
	// Content is the model's final assistant message after any
	// truncation-recovery rounds have been concatenated.
	Content string
	// NewMessages is the slice of messages produced during this run
	// (assistant turns + tool result turns) in order. When a compact
	// pass rewrote the live history mid-run, this becomes the full
	// replacement history snapshot and HistoryRewritten is true.
	NewMessages []providers.ChatMessage
	// HistoryRewritten reports whether a compact pass replaced the
	// live history slice mid-run. Callers that persist conversations
	// should replace stored history instead of append-only extending
	// it when this is true.
	HistoryRewritten bool
	// InputTokens / OutputTokens are the cumulative usage across
	// every round in this run, including any compact + recovery
	// rounds. Zero when the provider doesn't report usage.
	InputTokens  int
	OutputTokens int
}
