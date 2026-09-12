package agent

import (
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const yieldTurnToolName = "yield_turn"

func acceptedTurnYield(results []providers.ChatMessage) bool {
	// A failed call or a batch with other work must return to the model so
	// it can inspect the results before deciding whether to end the turn.
	if len(results) != 1 {
		return false
	}
	message := results[0]
	if message.Role != "tool" || message.Name != yieldTurnToolName || message.ToolResult == nil || message.ToolResult.IsError {
		return false
	}
	var result struct {
		Yielded bool   `json:"yielded"`
		Reason  string `json:"reason"`
	}
	return json.Unmarshal([]byte(message.ToolResult.TextProjection()), &result) == nil && result.Yielded && strings.TrimSpace(result.Reason) != ""
}

func emptyAnswerRecoveryContext() ContextSegment {
	return RequestOnlyContextMessages([]providers.ChatMessage{{
		Role: "user", Hidden: true,
		Content: "Your previous response ended without a reply or tool call. Continue any actionable work and report its result or blocker. If no action or useful reply is needed, explicitly call yield_turn alone with a reason. Do not repeat work or messages already completed. This is the only empty-response recovery attempt.",
	}})[0]
}
