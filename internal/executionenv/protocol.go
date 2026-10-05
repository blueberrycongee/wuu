package executionenv

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type Init struct {
	RequireExistingWorkspace bool              `json:"require_existing_workspace,omitempty"`
	RemoveWorkspaceOnExit    bool              `json:"remove_workspace_on_exit,omitempty"`
	Identity                 string            `json:"identity,omitempty"`
	Environment              map[string]string `json:"environment,omitempty"`
	Version                  int               `json:"version"`
	Root                     string            `json:"root"`
	Session                  string            `json:"session"`
}

type ToolRequest struct {
	GitAttributionEnabled bool               `json:"git_attribution_enabled,omitempty"`
	Call                  providers.ToolCall `json:"call"`
	PermissionMode        string             `json:"permission_mode"`
	Actor                 string             `json:"actor"`
}

// Executor routes workspace tools to the selected environment. Host policy is
// evaluated before this interface; the worker independently checks it again.
type Executor interface {
	Execute(context.Context, ToolRequest) (toolresult.Result, error)
}

func WorkspaceTool(name string) bool {
	switch name {
	case "read_file", "write_file", "edit_file", "list_files", "grep", "glob", "bash", "process", "git", "present_artifact":
		return true
	default:
		return false
	}
}

type CodeRequest struct {
	GitAttributionEnabled bool                `json:"git_attribution_enabled,omitempty"`
	Program               codemode.RunRequest `json:"program"`
	TimeoutMS             int                 `json:"timeout_ms"`
	PermissionMode        string              `json:"permission_mode"`
	Actor                 string              `json:"actor"`
}
