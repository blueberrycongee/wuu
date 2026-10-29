package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// PluginProcessScope adapts native configuration to the same host process
// gateway used by external tool providers. It does not export a tool registry.
func (t *Toolkit) PluginProcessScope() (*ProcessScope, error) {
	var manager *proc.Manager
	if t.env.ExecutionEnvironment != nil {
		remote, ok := t.env.ExecutionEnvironment.(executionenv.ProcessExecutor)
		if !ok {
			return nil, errors.New("selected execution environment does not support plugin processes")
		}
		manager = remote.ProcessManager()
	} else {
		var err error
		manager, err = t.env.ProcessManager()
		if err != nil {
			return nil, err
		}
	}
	config := ProcessScopeConfig{Manager: manager, ExecutionEnvironment: t.env.ExecutionEnvironment, GitAttributionEnabled: t.env.gitAttributionEnabled(), RootDir: t.env.RootDir, StateDir: t.env.StateDir, SessionDir: t.env.SessionDir, ThreadID: t.env.SessionID, ActorID: defaultProcessOwnerID(t.env), ActorPath: currentExecutionPath(t.env), PermissionMode: t.env.PermissionMode, Boundary: t.boundary, FileScopeRoots: t.env.FileScopeRoots, ProtectedPaths: t.env.ProtectedPaths, Authorizer: t.authorizer, SandboxProvider: t.env.ProcessSandboxProvider, Reviewer: t.reviewer, ApproveForMe: t.approveForMe, ExecutionRoot: t.env.ExecRootDir, Disabled: t.IsToolDisabled("bash")}
	if t.permissionRequestHook != nil {
		config.BeforePermission = func(ctx context.Context, info ToolInfo, call providers.ToolCall) error {
			return t.permissionRequestHook(ctx, t, info, call)
		}
	}
	if t.env.gitAttributionEnabled() {
		config.CommandPrefix = t.env.gitAttributionShellPrefix
	}
	return NewProcessScope(config), nil
}

// StartPluginProcess uses the host process gateway. The extension host owns
// cancellation after this tool invocation ends.
func (t *Toolkit) StartPluginProcess(ctx context.Context, command, cwd string) (*proc.Process, *proc.Manager, error) {
	scope, err := t.PluginProcessScope()
	if err != nil {
		return nil, nil, err
	}
	return scope.Start(ctx, command, cwd)
}

func (t *Toolkit) RedactProcessOutput(value string) string { return RedactToolOutput(value) }

// StartEnvironmentProcess applies the same worker-side permission gate as
// ExecuteEnvironmentResult. The authenticated transport has already applied
// the host's selected policy; worker paths and sandbox are still enforced here.
func (t *Toolkit) StartEnvironmentProcess(ctx context.Context, command, cwd, operationID string) (*proc.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.env.ExecutionEnvironment != nil {
		return nil, errors.New("execution worker cannot delegate its process authority")
	}
	scope, err := t.PluginProcessScope()
	if err != nil {
		return nil, err
	}
	if scope.config.Disabled || scope.config.ThreadID == "" || scope.config.ThreadID == "session-pending" || scope.config.ActorID == "" {
		return nil, errors.New("process start requires enabled shell, thread and actor")
	}
	raw, err := json.Marshal(bashArgs{Command: command, CWD: cwd, RunInBackground: true})
	if err != nil {
		return nil, err
	}
	call := providers.ToolCall{Name: "bash", Arguments: string(raw)}
	if err := t.checkPermission(ctx, buildToolInfoForArgs(&BashTool{}, ToolExposureDirect, call.Arguments), call); err != nil {
		return nil, err
	}
	started, _, err := scope.startAuthorized(ctx, command, cwd, operationID)
	return started, err
}
