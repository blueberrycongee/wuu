package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// SetSessionWorkspaceTool explicitly moves the current main-agent session to
// an existing linked worktree. Ordinary shell cwd changes intentionally do not
// use this path and therefore remain temporary.
type SetSessionWorkspaceTool struct{ env *Env }

func NewSetSessionWorkspaceTool(env *Env) *SetSessionWorkspaceTool {
	return &SetSessionWorkspaceTool{env: env}
}

func (t *SetSessionWorkspaceTool) Name() string            { return "set_session_workspace" }
func (t *SetSessionWorkspaceTool) IsReadOnly() bool        { return false }
func (t *SetSessionWorkspaceTool) IsConcurrencySafe() bool { return false }

func (t *SetSessionWorkspaceTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		DirectOnly: true,
		Name:       "set_session_workspace",
		Description: "Persistently bind the current session to an existing linked Git worktree after intentionally moving the task there, " +
			"or back to the project root. Before removing the session's own worktree, first move the session back to the project root. " +
			"This updates subsequent tool roots and the desktop Environment panel. Do not use it for a temporary shell cd or command-specific cwd.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"root": map[string]any{
					"type":        "string",
					"description": "Required absolute path to the linked Git worktree or project root that now owns this session's task.",
				},
			},
			"required": []string{"root"},
		},
	}
}

func (t *SetSessionWorkspaceTool) Execute(_ context.Context, argsJSON string) (string, error) {
	if t != nil && t.env != nil && t.env.ExecutionEnvironment != nil {
		return "", errors.New("execution environment workspaces are pinned; use paths inside the selected environment")
	}
	if t == nil || t.env == nil || t.env.OnSessionWorkspaceChanged == nil {
		return "", errors.New("session workspace rebinding is unavailable")
	}
	var args struct {
		Root string `json:"root"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	root := strings.TrimSpace(args.Root)
	if root == "" {
		return "", errors.New("root is required")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("root must be absolute")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("inspect workspace root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("workspace root must be a directory")
	}
	// Match the symlink-resolved roots New and CloneForRoot use, so paths
	// under the new root pass the same containment checks.
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	commitRuntime, err := t.env.prepareSessionWorkspaceChange(root)
	if err != nil {
		return "", err
	}
	if err := t.env.OnSessionWorkspaceChanged(root); err != nil {
		return "", err
	}
	commitRuntime()
	result, _ := json.Marshal(map[string]string{"root": root})
	return string(result), nil
}
