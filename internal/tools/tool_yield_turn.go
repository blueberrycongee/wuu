package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// YieldTurnTool records an explicit decision to end without an outward reply.
// The loop accepts it only after a successful, standalone tool call completes.
type YieldTurnTool struct{}

func NewYieldTurnTool() *YieldTurnTool         { return &YieldTurnTool{} }
func (*YieldTurnTool) Name() string            { return "yield_turn" }
func (*YieldTurnTool) IsReadOnly() bool        { return true }
func (*YieldTurnTool) IsConcurrencySafe() bool { return false }
func (*YieldTurnTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "yield_turn",
		Description: "End this turn without sending a final reply. Call alone, directly, with a short reason when there is no actionable work or useful reply, or an explicit message already delivered the result. Do not use this to skip a human request or conceal a failure; report blockers instead. This does not cancel background work. The reason is recorded privately.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"reason": map[string]any{"type": "string"}},
			"required":   []string{"reason"},
		},
	}
}
func (*YieldTurnTool) Execute(_ context.Context, input string) (string, error) {
	var args struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", err
	}
	args.Reason = strings.TrimSpace(args.Reason)
	if args.Reason == "" {
		return "", errors.New("yield_turn requires a reason")
	}
	return mustJSON(map[string]any{"yielded": true, "reason": args.Reason})
}
