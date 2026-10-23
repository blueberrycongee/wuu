package loopdriver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// TurnGateway exposes individual operations to a driver that owns its control
// flow. CallModel never executes tools. CallModelWithTools explicitly opts into
// safe streaming tool execution; neither operation decides whether to continue.
// Operations are serialized within a turn and reject calls after completion or
// cancellation. Model and tool events use the turn's existing event stream.
type TurnGateway interface {
	ReadTurn(context.Context) (providers.ChatRequest, error)
	AdmitInput(context.Context) (InputAdmission, error)
	ReadRequestContext(context.Context) ([]ContextSegment, error)
	TransformRequest(context.Context, providers.ChatRequest) (providers.ChatRequest, error)
	ReadContext(context.Context) (TurnContext, error)
	ReplaceContext(context.Context, ContextReplacement) (TurnContext, error)
	CallModel(context.Context, providers.ChatRequest) (ModelRound, error)
	CallModelWithTools(context.Context, providers.ChatRequest, string) (ModelRound, error)
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

// InputAdmission contains new durable input and the resulting active context.
// Admission runs only when requested by the driver, before its compaction or
// request-building policy. It does not call a model or transform a request.
type InputAdmission struct {
	Messages []providers.ChatMessage
	Context  TurnContext
}

// ModelRound includes the assistant response. Model calls do not admit queued
// input, assemble request context, or run request transforms. When configured,
// the execution's receipt store commits the final input before dispatch; a write
// failure prevents model and streaming-tool execution. A committed receipt does
// not prove submission, since cancellation may occur immediately afterward.
type ModelRound struct {
	Messages []providers.ChatMessage
	Usage    *providers.TokenUsage
	// CallModelWithTools reserves a unique batch before dispatch and permits
	// safe prefix tools to start during streaming. On success, a nonempty ID
	// must be joined through CallTools with exactly the returned calls before
	// admitting input, submitting other effects, replacing context, or completing.
	// A failed model drains its work; its batch cannot be reused.
	// ToolWorkStarted also survives
	// model failure and is evidence against blind replay.
	ToolBatchID     string
	ToolWorkStarted bool
}

// ModelRoundError retains the classified failure of a model operation. The
// accompanying ModelRound still contains partial output.
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
// An ID returned by CallModelWithTools instead joins that entire model batch;
// its call identities and arguments must match. It never executes them twice.
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
