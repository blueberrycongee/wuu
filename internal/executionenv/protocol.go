package executionenv

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/process"
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

const UnobservedReadCapability = "read.unobserved.v1"
const ReadObservationCommitCapability = "read.observation-commit.v1"

// CodeStatelessCapability enforces disabled PTC session state for a whole program.
const CodeStatelessCapability = "code.stateless.v1"

type ToolRequest struct {
	SuppressReadObservation   bool               `json:"suppress_read_observation,omitempty"`
	ShellEarlyHandoffDisabled bool               `json:"shell_early_handoff_disabled,omitempty"`
	GitAttributionEnabled     bool               `json:"git_attribution_enabled,omitempty"`
	Call                      providers.ToolCall `json:"call"`
	PermissionMode            string             `json:"permission_mode"`
	Actor                     string             `json:"actor"`
}

// Executor routes workspace tools to the selected environment. Host policy is
// evaluated before this interface; the worker independently checks it again.
// PreparedRead retains its private worker receipt in a host-only closure.
// Once Commit is sent, a lost acknowledgement is reported as unknown.
type PreparedRead struct {
	handle         *preparedReadHandle
	PermissionMode string
	Result         toolresult.Result
	Commit         func(context.Context) (bool, error)
	Abort          func(context.Context) error
	Release        func(context.Context) error
}
type PreparedReadExecutor interface {
	PrepareUnobservedRead(context.Context, ToolRequest) (PreparedRead, error)
}

type UnobservedReadExecutor interface {
	ExecuteUnobservedRead(context.Context, ToolRequest) (toolresult.Result, error)
}

type Executor interface {
	Execute(context.Context, ToolRequest) (toolresult.Result, error)
}

func WorkspaceTool(name string) bool {
	switch name {
	case "read_file", "write_file", "edit_file", "apply_patch", "list_files", "grep", "glob", "bash", "process", "git", "present_artifact":
		return true
	default:
		return false
	}
}

type CodeRequest struct {
	DisableState          bool                `json:"disable_state,omitempty"`
	GitAttributionEnabled bool                `json:"git_attribution_enabled,omitempty"`
	Program               codemode.RunRequest `json:"program"`
	TimeoutMS             int                 `json:"timeout_ms"`
	PermissionMode        string              `json:"permission_mode"`
	Actor                 string              `json:"actor"`
}

// ProcessStartCapability promises operation IDs in start results and durable
// process records, allowing reconciliation without replaying an uncertain start.
const ProcessStartCapability = "process.start.v1"

// ProcessRequest carries host-authorized input to the same worker authority as
// workspace tools. Session/root identity comes from the initialized transport,
// never from extension-supplied process arguments.
type ProcessRequest struct {
	OperationID           string `json:"operation_id"`
	Command               string `json:"command"`
	CWD                   string `json:"cwd,omitempty"`
	PermissionMode        string `json:"permission_mode"`
	Actor                 string `json:"actor"`
	GitAttributionEnabled bool   `json:"git_attribution_enabled,omitempty"`
}

// ProcessExecutor is implemented by execution environments that can own a
// detached process. A selected executor without this capability fails closed.
type ProcessExecutor interface {
	Executor
	Root() string
	Backend() string
	StartProcess(context.Context, ProcessRequest) (*process.Process, error)
	ProcessManager() *process.Manager
}
