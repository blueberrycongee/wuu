package agent

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ToolExecutor executes model-requested tool calls.
type ToolExecutor interface {
	Definitions() []providers.ToolDefinition
	Execute(ctx context.Context, call providers.ToolCall) (string, error)
}

// ContextualToolDefinitions preserves selected registry failures before model I/O.
type ContextualToolDefinitions interface {
	DefinitionsContext(context.Context) ([]providers.ToolDefinition, error)
}

func toolDefinitions(ctx context.Context, executor ToolExecutor) ([]providers.ToolDefinition, error) {
	if executor == nil {
		return nil, nil
	}
	if contextual, ok := executor.(ContextualToolDefinitions); ok {
		return contextual.DefinitionsContext(ctx)
	}
	return executor.Definitions(), nil
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

// ContextualToolSupport keeps selected-provider resolution within its caller's
// lifetime. Failure must not fall back to a different tool surface.
type ContextualToolSupport interface {
	SupportsToolContext(context.Context, string) (bool, error)
}

func ResolveToolSupport(ctx context.Context, executor ToolExecutor, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var supported bool
	var err error
	if provider, ok := executor.(ContextualToolSupport); ok {
		supported, err = provider.SupportsToolContext(ctx, name)
	} else if provider, ok := executor.(ToolSupportProvider); ok {
		supported = provider.SupportsTool(name)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return supported, err
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

// ContextualToolMetadata exposes scheduling metadata without detaching remote
// resolution from the admission context.
type ContextualToolMetadata interface {
	ToolMetadataContext(context.Context, providers.ToolCall) (ToolMetadata, bool, error)
}

func ResolveToolMetadata(ctx context.Context, executor ToolExecutor, call providers.ToolCall) (ToolMetadata, bool, error) {
	if err := ctx.Err(); err != nil {
		return ToolMetadata{}, false, err
	}
	var metadata ToolMetadata
	var found bool
	var err error
	if provider, ok := executor.(ContextualToolMetadata); ok {
		metadata, found, err = provider.ToolMetadataContext(ctx, call)
	} else if provider, ok := executor.(ToolMetadataProvider); ok {
		metadata, found = provider.ToolMetadata(call)
	}
	if err := ctx.Err(); err != nil {
		return ToolMetadata{}, false, err
	}
	return metadata, found, err
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

// ContextualToolDisplay allows optional display enrichment to stop with the
// stream or turn that requested it.
type ContextualToolDisplay interface {
	ToolDisplayContext(context.Context, providers.ToolCall) (providers.ToolCallDisplay, bool, error)
}

func ResolveToolDisplay(ctx context.Context, executor ToolExecutor, call providers.ToolCall) (providers.ToolCallDisplay, bool, error) {
	if err := ctx.Err(); err != nil {
		return providers.ToolCallDisplay{}, false, err
	}
	var display providers.ToolCallDisplay
	var found bool
	var err error
	if provider, ok := executor.(ContextualToolDisplay); ok {
		display, found, err = provider.ToolDisplayContext(ctx, call)
	} else if provider, ok := executor.(ToolDisplayProvider); ok {
		display, found = provider.ToolDisplay(call)
	}
	if err := ctx.Err(); err != nil {
		return providers.ToolCallDisplay{}, false, err
	}
	return display, found, err
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
