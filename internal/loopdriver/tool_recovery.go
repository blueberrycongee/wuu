package loopdriver

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ToolRecovery names the original host admission. It carries no replay policy,
// result or permission grant. The host compares every call with its own receipt.
type ToolRecovery struct {
	OriginalExecutionID string
	BatchID             string
	Calls               []providers.ToolCall
}

// ToolRecoveryGateway is optional. Missing support must not fall back to tools.
type ToolRecoveryGateway interface {
	RecoverTools(context.Context, ToolRecovery) ([]providers.ChatMessage, error)
}
