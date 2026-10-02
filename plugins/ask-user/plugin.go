package askuser

import (
	"context"
	"encoding/json"
	"fmt"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type arguments struct {
	Questions []pluginapi.UserQuestion `json:"questions"`
}

func Handler() pluginapi.Handler {
	return pluginapi.Handler{
		Definition: pluginapi.Definition{
			RequiredServices: pluginapi.RequireHostServices(pluginapi.KernelUserQuestionOfferService),
			Tools: []pluginapi.Tool{{
				ID:          "ask_user",
				DirectOnly:  true,
				Description: "Offer the user one or more focused questions without pausing the current turn. Prefer clear choices. A later user reply, if any, arrives as ordinary steering input rather than this tool result.",
				InputSchema: questionInputSchema(),
				Display:     &pluginapi.ToolDisplay{Kind: "ask-user", Text: "Asked the user", Capability: "interaction"},
			}},
		},
		ExecuteTool: executeTool,
	}
}

func executeTool(ctx context.Context, host pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	if call.ToolID != "ask_user" {
		return pluginapi.ToolResult{}, fmt.Errorf("unknown ask user tool %q", call.ToolID)
	}
	var input arguments
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return pluginapi.ToolResult{}, fmt.Errorf("invalid tool arguments: %w", err)
	}
	offer, err := pluginapi.OfferUserQuestions(ctx, host, input.Questions)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	encoded, err := json.Marshal(offer)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(string(encoded)), nil
}

func questionInputSchema() map[string]any {
	option := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label":       map[string]any{"type": "string", "description": "Choice label shown to the user."},
			"description": map[string]any{"type": "string", "description": "Optional short explanation of this choice."},
		},
		"required":             []string{"label"},
		"additionalProperties": false,
	}
	question := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string", "description": "Stable ID unique within this call."},
			"question":     map[string]any{"type": "string", "description": "The focused question to answer."},
			"header":       map[string]any{"type": "string", "description": "Optional compact heading."},
			"detail":       map[string]any{"type": "string", "description": "Optional context needed to decide."},
			"options":      map[string]any{"type": "array", "items": option},
			"multi_select": map[string]any{"type": "boolean", "description": "Allow choosing more than one option."},
			"allow_custom": map[string]any{"type": "boolean", "description": "Allow a free-text answer."},
		},
		"required":             []string{"id", "question"},
		"additionalProperties": false,
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{"type": "array", "minItems": 1, "items": question},
		},
		"required":             []string{"questions"},
		"additionalProperties": false,
	}
}
