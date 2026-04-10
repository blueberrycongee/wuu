// Package agent: Step is the transport-agnostic abstraction the
// shared tool-use loop drives. Both the non-streaming Runner (which
// calls providers.Client.Chat) and the streaming StreamRunner (which
// consumes SSE events) implement Step, so the actual loop logic —
// step counting, tool execution, truncation recovery, context-overflow
// auto-compact — lives in exactly one place. See loop.go.
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
	// Compact is invoked once per Run when the underlying step returns
	// a context-overflow error. nil disables auto-compact, in which
	// case the error is propagated to the caller as-is.
	Compact CompactFn
	// OnUsage is invoked once per LLM round-trip with the per-call
	// token counts when the provider reports them. The loop also
	// accumulates totals into LoopResult.
	OnUsage func(input, output int)
	// OnToolResult is invoked after each tool execution with the
	// (call, JSON result) pair. Used by streaming callers to feed
	// live tool-result rendering into the TUI.
	OnToolResult func(call providers.ToolCall, result string)
}

// LoopResult is what RunToolLoop returns on success.
type LoopResult struct {
	// Content is the model's final assistant message after any
	// truncation-recovery rounds have been concatenated.
	Content string
	// NewMessages is the slice of messages produced during this run
	// (assistant turns + tool result turns) in order. Callers that
	// need to persist or relay the conversation use this to extend
	// their stored history.
	NewMessages []providers.ChatMessage
	// InputTokens / OutputTokens are the cumulative usage across
	// every round in this run, including any compact + recovery
	// rounds. Zero when the provider doesn't report usage.
	InputTokens  int
	OutputTokens int
}
