package tools

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

type ListWorkspacesTool struct{ env *Env }

func (t *ListWorkspacesTool) Name() string            { return "list_workspaces" }
func (t *ListWorkspacesTool) IsReadOnly() bool        { return true }
func (t *ListWorkspacesTool) IsConcurrencySafe() bool { return true }
func (t *ListWorkspacesTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        t.Name(),
		Description: "Find the user's registered projects by name and absolute path when asked to work in a project, including from a conversation with no project selected. Choose the matching project, read its applicable AGENTS.md and other project instructions, and use absolute file paths and an explicit command working directory. Ask if the project name is ambiguous. Working in another project does not move or replace this conversation.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	}
}
func (t *ListWorkspacesTool) Execute(ctx context.Context, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	list := t.env.RegisteredWorkspaces
	if t.env.ExecutionEnvironment != nil {
		list = nil
	}
	if list == nil {
		list = []workspaces.Workspace{}
	}
	raw, err := json.Marshal(struct {
		Current    string                 `json:"current"`
		Workspaces []workspaces.Workspace `json:"workspaces"`
	}{t.env.RootDir, list})
	return string(raw), err
}
