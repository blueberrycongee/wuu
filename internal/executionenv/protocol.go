package executionenv

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
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
	ShellEarlyHandoffDisabled bool               `json:"shell_early_handoff_disabled,omitempty"`
	GitAttributionEnabled     bool               `json:"git_attribution_enabled,omitempty"`
	Call                      providers.ToolCall `json:"call"`
	PermissionMode            string             `json:"permission_mode"`
	Actor                     string             `json:"actor"`
}

// Executor routes workspace tools to the selected environment. Host policy is
// evaluated before this interface; the worker independently checks it again.
type Executor interface {
	Execute(context.Context, ToolRequest) (toolresult.Result, error)
}

func WorkspaceTool(name string) bool {
	switch name {
	case "read_file", "write_file", "edit_file", "apply_patch", "list_files", "grep", "glob", "bash", "git", "present_artifact":
		return true
	default:
		return false
	}
}

type CodeRequest struct {
	// CodeJournal is set by the host from the current scoped executor, never
	// accepted as authority from the program's input.
	CodeJournal           bool                `json:"code_journal,omitempty"`
	GitAttributionEnabled bool                `json:"git_attribution_enabled,omitempty"`
	Program               codemode.RunRequest `json:"program"`
	TimeoutMS             int                 `json:"timeout_ms"`
	PermissionMode        string              `json:"permission_mode"`
	Actor                 string              `json:"actor"`
}

// CodeJournalCapability promises journal callbacks and deterministic replay in
// the worker. Older workers remain usable for ordinary, non-durable programs.
const CodeJournalCapability = "run_code.journal.v1"

// CodeJournalCallback extends the nested callback channel without changing the
// existing ToolCall representation (whose kind field has a different meaning).
type CodeJournalCallback struct {
	Kind    string                     `json:"callback_kind"`
	Request toolctx.CodeJournalRequest `json:"request"`
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
