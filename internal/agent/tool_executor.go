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

// ToolFinalization keeps post-settlement additions with the specific result.
// Additions compose with execution metadata; later discovery definitions win.
type ToolFinalization struct {
	Result            toolresult.Result
	DiscoveredTools   []providers.LoadableToolDefinition
	AdditionalContext string
}

// ToolOutcomeFinalizer preserves metadata supplied while settling successes or
// normalized errors. The runtime captures it once before publishing completion.
type ToolOutcomeFinalizer interface {
	FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) ToolFinalization
}

// FinalizeToolOutcome captures invocation-owned additions after settlement,
// including additions left by execution errors. Service adapters and the local
// runtime use the same ordering; a shared last-context slot is never attributed.
func FinalizeToolOutcome(executor ToolExecutor, call providers.ToolCall, result toolresult.Result) ToolFinalization {
	settled := ToolFinalization{Result: result}
	if finalizer, ok := executor.(ToolOutcomeFinalizer); ok {
		settled = finalizer.FinalizeToolOutcome(call, result)
	} else if finalizer, ok := executor.(ToolResultFinalizer); ok {
		settled.Result = finalizer.FinalizeToolResult(call, result)
	}
	var discovered []providers.LoadableToolDefinition
	if provider, ok := executor.(ToolDiscoveryProvider); ok {
		discovered = provider.DiscoveredTools(call)
	}
	var additional string
	if provider, ok := executor.(ToolCallContextProvider); ok {
		additional = provider.TakeAdditionalContext(call)
	}
	if settled.AdditionalContext != "" {
		if additional != "" {
			additional += "\n\n"
		}
		additional += settled.AdditionalContext
	}
	settled.DiscoveredTools = providers.MergeLoadableToolDefinitions(discovered, settled.DiscoveredTools)
	settled.AdditionalContext = additional
	return settled
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

// ToolMetadataContextProvider can cancel a nested metadata lookup and report
// failures instead of silently treating them as missing metadata.
type ToolMetadataContextProvider interface {
	ToolMetadataContext(context.Context, providers.ToolCall) (ToolMetadata, bool, error)
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
// This legacy shared slot is supported only by non-streaming direct serial
// leaf-tool consumers. Hook decorators, service adapters and selected batches require
// ToolCallContextProvider instead; they cannot safely attribute a last-result
// value to an invocation.
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
