package tools

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const newContextToolName = "new_context"

// NewContextTool is a model control signal. The agent loop observes the
// completed call at the tool-batch boundary and performs the actual context
// replacement there; executing the tool itself never mutates environment state.
type NewContextTool struct{}

func NewNewContextTool() *NewContextTool        { return &NewContextTool{} }
func (*NewContextTool) Name() string            { return newContextToolName }
func (*NewContextTool) IsReadOnly() bool        { return true }
func (*NewContextTool) IsConcurrencySafe() bool { return true }
func (*NewContextTool) Execute(context.Context, string) (string, error) {
	return `{"requested":true,"message":"Wuu will evaluate the context-window transition after this tool batch. Environment state is unchanged."}`, nil
}

func (*NewContextTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		DirectOnly: true,
		Name:       newContextToolName,
		Description: "Request a fresh context window after this tool batch. Save a recovery checkpoint with notes first: the host releases the active transcript without summarizing it. " +
			"Files, processes and permissions remain unchanged. Read the checkpoint and recover missing history after the switch.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{},
		},
	}
}
