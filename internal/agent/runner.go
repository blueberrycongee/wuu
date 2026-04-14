package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ToolExecutor executes model-requested tool calls.
type ToolExecutor interface {
	Definitions() []providers.ToolDefinition
	Execute(ctx context.Context, call providers.ToolCall) (string, error)
}

// ToolMetadata describes a tool's concurrency characteristics.
type ToolMetadata struct {
	ReadOnly        bool
	ConcurrencySafe bool
}

// ToolMetadataProvider is an optional interface a ToolExecutor can
// implement to expose per-tool metadata (read-only, concurrency-safe).
// The agent loop uses this to partition tool calls — read-only tools
// run concurrently, write tools run serially. Aligned with Claude
// Code's partitionToolCalls / runToolsConcurrently architecture.
type ToolMetadataProvider interface {
	ToolMetadata(name string) (ToolMetadata, bool)
}

// ToolContextProvider is an optional interface a ToolExecutor can
// implement to return additional context alongside tool results.
// Hook systems use this to inject context into the conversation
// after PostToolUse hooks run. Aligned with Claude Code's
// additionalContext hook output mechanism.
type ToolContextProvider interface {
	// LastAdditionalContext returns the additional context string
	// from the most recent Execute call, if any. Callers should
	// check this after each Execute and inject non-empty values
	// as system messages.
	LastAdditionalContext() string
}

// Runner manages one multi-step coding turn. It is a thin wrapper
// around RunToolLoop that always executes through the streaming Step
// path; unary clients are adapted underneath via AdaptStreamClient.
type Runner struct {
	Client       providers.Client
	Tools        ToolExecutor
	Model        string
	SystemPrompt string
	MaxSteps     int
	Temperature  float64
	// ContextWindowOverride pins the context window for this run
	// instead of consulting providers.ContextWindowFor. Zero falls
	// back to the registry. Used by sub-agents whose model isn't in
	// the registry but whose owner knows the right number.
	ContextWindowOverride int
}

// RunResult is the structured outcome of a Runner.RunWithUsage call.
type RunResult struct {
	Content      string
	InputTokens  int
	OutputTokens int
}

// Run executes one prompt with optional tool-use loop.
func (r *Runner) Run(ctx context.Context, prompt string) (string, error) {
	res, err := r.RunWithUsage(ctx, prompt, nil)
	if err != nil {
		return "", err
	}
	return res.Content, nil
}

// RunWithUsage is like Run but reports per-call token usage to the
// optional onUsage callback (called once per LLM round-trip) and
// returns cumulative totals in the result.
func (r *Runner) RunWithUsage(ctx context.Context, prompt string, onUsage func(input, output int)) (RunResult, error) {
	if r.Client == nil {
		return RunResult{}, errors.New("client is required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return RunResult{}, errors.New("model is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return RunResult{}, errors.New("prompt is required")
	}

	// Build the initial conversation: optional system prompt + the
	// user's request. The shared loop takes it from there.
	history := make([]providers.ChatMessage, 0, 2)
	if strings.TrimSpace(r.SystemPrompt) != "" {
		history = append(history, providers.ChatMessage{Role: "system", Content: r.SystemPrompt})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: prompt})

	maxCtx := r.ContextWindowOverride
	if maxCtx <= 0 {
		maxCtx = providers.ContextWindowFor(r.Model)
	}
	cfg := LoopConfig{
		Tools:            r.Tools,
		Model:            r.Model,
		Temperature:      r.Temperature,
		MaxSteps:         r.MaxSteps,
		MaxContextTokens: maxCtx,
		OnUsage:          onUsage,
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return compact.Compact(ctx, messages, r.Client, r.Model)
		},
	}

	res, err := RunToolLoop(ctx, history, cfg, &streamStep{client: providers.AdaptStreamClient(r.Client)})
	return RunResult{
		Content:      res.Content,
		InputTokens:  res.InputTokens,
		OutputTokens: res.OutputTokens,
	}, err
}
