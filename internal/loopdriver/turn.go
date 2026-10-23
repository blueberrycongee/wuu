package loopdriver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// TurnGateway exposes individual operations to a driver that owns its control
// flow. Calling a model never executes tools or decides whether to continue.
// Operations are serialized within a turn and reject calls after completion or
// cancellation. Model and tool events use the turn's existing event stream.
type TurnGateway interface {
	ReadTurn(context.Context) (providers.ChatRequest, error)
	CallModel(context.Context, providers.ChatRequest) (ModelRound, error)
	CallTools(context.Context, ToolBatch) ([]providers.ChatMessage, error)
	CompleteTurn(context.Context, TurnResult) (ModelLoopReceipt, error)
}

// ModelRound includes durable input contributions admitted before the request,
// followed by its assistant message. Request-only context is not included.
type ModelRound struct {
	Messages []providers.ChatMessage
	Usage    *providers.TokenUsage
}

// ToolBatch.ID is unique within an execution. A duplicate is rejected, including
// after a failed call; cancellation cannot undo external effects. A driver must
// checkpoint its intent before submitting effects and resolve an interrupted
// batch explicitly before attempting it again in a resumed execution.
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
