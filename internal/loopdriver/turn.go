package loopdriver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// TurnGateway exposes individual operations to a driver that owns its control
// flow. Calling a model never executes tools or decides whether to continue.
// Operations are serialized within a turn and reject calls after completion or
// cancellation. Model and tool events use the turn's existing event stream.
type TurnGateway interface {
	ReadTurn(context.Context) (providers.ChatRequest, error)
	ReadContext(context.Context) (TurnContext, error)
	ReplaceContext(context.Context, ContextReplacement) (TurnContext, error)
	CallModel(context.Context, providers.ChatRequest) (ModelRound, error)
	CallTools(context.Context, ToolBatch) ([]providers.ChatMessage, error)
	CompleteTurn(context.Context, TurnResult) (ModelLoopReceipt, error)
}

// TurnContext is the active model history, independent of the append-only
// durable transcript. Revision belongs to this execution and changes whenever
// messages or their durable addresses change. CanReplace requires both archive
// and atomic context-checkpoint services; there is no volatile fallback.
type TurnContext struct {
	Messages    []providers.ChatMessage
	Revision    uint64
	ArchiveHead int
	CanReplace  bool
}

// ContextReplacement replaces only active model context. The service archives
// accepted originals first, then commits the replacement and any concurrent
// durable tail before returning. Revision must match ReadContext. A failed
// commit keeps the prior active context; a successful archive remains durable.
// Cancellation after commit does not undo the committed replacement.
type ContextReplacement struct {
	Revision uint64
	Messages []providers.ChatMessage
}

// ModelRound includes durable input contributions admitted before the request,
// followed by its assistant message. Request-only context is not included.
type ModelRound struct {
	Messages []providers.ChatMessage
	Usage    *providers.TokenUsage
}

// ModelRoundError retains the classified failure of a model operation. The
// accompanying ModelRound still contains accepted input and partial output.
// Transport and authorization errors do not imply a completed model operation.
type ModelRoundError struct {
	Failure pluginprotocol.DriverModelFailure
}

func (e *ModelRoundError) Error() string { return e.Failure.Message }

// ToolBatch.ID is unique within an execution. A duplicate is rejected, including
// after a failed call; cancellation cannot undo external effects. A driver must
// checkpoint its intent before submitting effects and resolve an interrupted
// batch explicitly before attempting it again in a resumed execution. Call IDs
// must also be unique within the execution. Calls may be chosen directly by the
// driver; a missing assistant declaration is recorded before execution. Subsets
// of the current pending declaration can be submitted in separate batches.
type ToolBatch struct {
	ID       string
	Messages []providers.ChatMessage
	Calls    []providers.ToolCall
}

// TurnResult contains the driver's final output and optional additional messages.
// Model responses and tool results are already recorded and must not be repeated.
// It cannot overwrite the input transcript or discard accepted effects. Usage
// comes from execution services, not from caller-supplied counters.
type TurnResult struct {
	Content  string
	Messages []providers.ChatMessage
}
