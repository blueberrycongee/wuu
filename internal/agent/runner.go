package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ToolExecutor executes model-requested tool calls.
type ToolExecutor interface {
	Definitions() []providers.ToolDefinition
	Execute(ctx context.Context, call providers.ToolCall) (string, error)
}

func toolDefinitions(executor ToolExecutor) []providers.ToolDefinition {
	if executor == nil {
		return nil
	}
	return executor.Definitions()
}

type RichToolExecutor interface {
	ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error)
}

// ToolResultFinalizer settles recoverable model text before persistence and
// callbacks, including for plugin results and normalized execution errors.
// Executors without artifact storage can omit it; their evidence stays intact.
type ToolResultFinalizer interface {
	FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result
}

// ToolSupportProvider lets an executor report whether a tool belongs to the
// current model surface, even when its schema is deferred and must be loaded
// through discovery before execution.
type ToolSupportProvider interface {
	SupportsTool(name string) bool
}

// ToolMetadata describes a tool's scheduling and policy characteristics.
type ToolMetadata struct {
	// Orchestrator tools coordinate child calls but perform no leaf operations
	// themselves. Their children, not the parent, consume execution slots.
	Orchestrator    bool
	ReadOnly        bool
	ConcurrencySafe bool
	Destructive     bool
	Risk            string
	Reason          string
}

// ToolMetadataProvider is an optional interface a ToolExecutor can
// implement to expose per-tool metadata (read-only, concurrency-safe).
// The agent loop uses this to partition tool calls: read-only tools run
// concurrently, write tools run serially.
type ToolMetadataProvider interface {
	ToolMetadata(call providers.ToolCall) (ToolMetadata, bool)
}

// ToolAuthorizationGate lets executor decorators route dynamically provided
// tools through the same authority check as built-in tools.
type ToolAuthorizationGate interface {
	AuthorizeTool(ctx context.Context, call providers.ToolCall, metadata ToolMetadata) error
}

// ToolDisplayProvider is an optional interface a ToolExecutor can implement
// to provide user-facing labels for tool calls. UI clients should treat this
// as display metadata only; it is not sent to model providers.
type ToolDisplayProvider interface {
	ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool)
}

// ToolContextProvider is an optional interface a ToolExecutor can
// implement to return additional context alongside tool results.
// Hook systems use this to expose request-only context after
// PostToolUse hooks run.
type ToolContextProvider interface {
	// LastAdditionalContext returns the additional context string
	// from the most recent Execute call, if any. Callers should check
	// this after each Execute and project non-empty values into the next
	// provider request without appending them to durable history.
	LastAdditionalContext() string
}

// ToolCallContextProvider exposes request-only context for a specific tool
// call. Concurrent executors should implement this interface instead of
// sharing a single "last result" slot, which cannot preserve call ownership
// when multiple tools finish out of order.
type ToolCallContextProvider interface {
	// TakeAdditionalContext returns and clears additional context produced by
	// the specified tool call.
	TakeAdditionalContext(call providers.ToolCall) string
}

// ToolDiscoveryProvider is an optional interface a ToolExecutor can implement
// to attach provider-native deferred tool schemas to the tool result message
// that made those tools available.
type ToolDiscoveryProvider interface {
	DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition
}

// Runner manages one multi-step coding turn. It is a thin wrapper
// around RunToolLoop that always executes through the streaming Step
// path; unary clients are adapted underneath via AdaptStreamClient.
type Runner struct {
	Client                 providers.Client
	ProviderName           string
	ProviderObservationKey string
	Tools                  ToolExecutor
	Model                  string
	SystemPrompt           string
	SystemPromptSections   []SystemPromptSectionInfo
	MaxSteps               int
	Temperature            float64
	// ContextWindowOverride is the resolved provider/model context window.
	// Zero disables proactive compaction when the limit is unknown.
	ContextWindowOverride int
	// MaxInputTokens pins the prompt/input budget when lower than the
	// model's total context window.
	MaxInputTokens int
	// OutputReserveTokens pins the output budget used for compact threshold
	// math without forcing a request max_tokens value.
	OutputReserveTokens int
	// CompactThresholdPct lets callers compact earlier than the default
	// usable-window calculation. Zero means auto.
	CompactThresholdPct float64
	// CompactKeepRecentTokens overrides the default recent raw-history budget
	// kept after compaction. Zero means use the default.
	CompactKeepRecentTokens int
	// NativeDeferredToolDiscovery lets provider adapters use native
	// deferred-tool declarations for tools marked DeferLoading.
	NativeDeferredToolDiscovery bool
	// ProviderOptions carries provider-specific model compatibility and variant
	// options into both agent requests and nested compaction requests.
	ProviderOptions  map[string]any
	Variant          string
	InferenceJournal providers.InferenceJournal
}

// RunResult is the structured outcome of a Runner.RunWithUsage call.
type RunResult struct {
	Content             string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	FinishReason        providers.FinishReason
	StopReason          string
	Truncated           bool
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
	ctx = providers.WithInferenceJournal(ctx, r.InferenceJournal)

	// Build the initial conversation: optional system prompt + the
	// user's request. The shared loop takes it from there.
	history := make([]providers.ChatMessage, 0, 2)
	if strings.TrimSpace(r.SystemPrompt) != "" {
		history = append(history, providers.ChatMessage{Role: "system", Content: r.SystemPrompt})
	}
	history = append(history, providers.ChatMessage{Role: "user", Content: prompt})

	maxCtx := r.ContextWindowOverride
	cfg := LoopConfig{
		Tools:                       r.Tools,
		Model:                       r.Model,
		ProviderName:                r.ProviderName,
		ProviderObservationKey:      r.ProviderObservationKey,
		ModelVariant:                r.Variant,
		InferenceOperationKind:      providers.InferenceOperationAgentRound,
		InferenceWorkloadProfile:    providers.InferenceProfileInteractive,
		Temperature:                 r.Temperature,
		MaxSteps:                    r.MaxSteps,
		MaxContextTokens:            maxCtx,
		MaxInputTokens:              r.MaxInputTokens,
		OutputReserveTokens:         r.OutputReserveTokens,
		CompactThresholdPct:         r.CompactThresholdPct,
		CompactKeepRecentTokens:     r.CompactKeepRecentTokens,
		NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
		ProviderOptions:             r.ProviderOptions,
		SystemPromptSections:        cloneSystemPromptSections(r.SystemPromptSections),
		OnUsage:                     onUsage,
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			budget, budgetErr := applyAdaptiveCompactBudget(ctx, messages, compact.Budget{
				ContextTokens:       maxCtx,
				InputTokens:         r.MaxInputTokens,
				OutputReserveTokens: r.OutputReserveTokens,
				KeepRecentTokens:    r.CompactKeepRecentTokens,
			})
			if budgetErr != nil {
				return messages, budgetErr
			}
			return compact.CompactWithNativeOrSummary(ctx, messages, r.Client, r.Model, budget, compact.NativeOptions{
				Provider:                    r.ProviderName,
				Tools:                       toolDefinitions(r.Tools),
				Temperature:                 r.Temperature,
				ProviderOptions:             r.ProviderOptions,
				NativeDeferredToolDiscovery: r.NativeDeferredToolDiscovery,
			})
		},
	}

	res, err := RunToolLoop(ctx, history, cfg, NewStreamStep(r.Client))
	return RunResult{
		Content:             res.Content,
		InputTokens:         res.InputTokens,
		OutputTokens:        res.OutputTokens,
		CacheCreationTokens: res.CacheCreationTokens,
		CacheReadTokens:     res.CacheReadTokens,
		FinishReason:        res.FinishReason,
		StopReason:          res.StopReason,
		Truncated:           res.Truncated,
	}, err
}
