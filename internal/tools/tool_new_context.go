package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
)

const newContextToolName = "new_context"

// NewContextTool is a model control signal. The agent loop observes the
// completed call at the tool-batch boundary and performs the actual context
// replacement there; executing the tool itself never mutates environment state.
type NewContextTool struct{ env *Env }

func NewNewContextTool(env *Env) *NewContextTool { return &NewContextTool{env: env} }
func (*NewContextTool) Name() string             { return newContextToolName }
func (*NewContextTool) IsReadOnly() bool         { return true }
func (*NewContextTool) IsConcurrencySafe() bool  { return true }
func (t *NewContextTool) Execute(ctx context.Context, arguments string) (string, error) {
	var input struct {
		Checkpoint *workingnotes.Checkpoint `json:"checkpoint"`
	}
	if err := decodeArgs(arguments, &input); err != nil {
		return "", err
	}
	if checkpoint := input.Checkpoint; checkpoint != nil {
		if strings.TrimSpace(checkpoint.Path) == "" || strings.TrimSpace(checkpoint.Revision) == "" {
			return "", errors.New("checkpoint requires path and revision from the latest notes result")
		}
		read, _ := json.Marshal(notesArguments{Action: "read", Path: checkpoint.Path, Revision: &checkpoint.Revision, Limit: 1})
		if _, err := NewNotesTool(t.env).Execute(ctx, string(read)); err != nil {
			return "", fmt.Errorf("validate recovery checkpoint: %w", err)
		}
	}
	result := map[string]any{
		"requested": true,
		"message":   "Wuu will evaluate the context-window transition after this tool batch. Environment state is unchanged.",
	}
	if input.Checkpoint != nil {
		result["checkpoint"] = input.Checkpoint
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

func (*NewContextTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		DirectOnly: true,
		Name:       newContextToolName,
		Description: "Request a fresh context window after this tool batch. Save a recovery checkpoint with notes first: the host releases the active transcript without summarizing it. " +
			"Pass its path and the latest notes revision as checkpoint so the next window can read it directly. Files, processes and permissions remain unchanged. Read the checkpoint and recover missing history after the switch.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"checkpoint": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"path", "revision"},
					"properties": map[string]any{
						"path":     map[string]any{"type": "string", "description": "Saved recovery note path in this session."},
						"revision": map[string]any{"type": "string", "description": "Latest notes collection revision; stale or missing checkpoints are rejected."},
					},
				},
			},
		},
	}
}
