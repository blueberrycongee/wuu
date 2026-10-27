package tools

import (
	"context"
	"errors"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// PluginProcessScope adapts native configuration to the same host process
// gateway used by external tool providers. It does not export a tool registry.
func (t *Toolkit) PluginProcessScope() (*ProcessScope, error) {
	if t.env.ExecutionEnvironment != nil {
		return nil, errors.New("plugin processes require a local execution environment")
	}
	manager, err := t.env.ProcessManager()
	if err != nil {
		return nil, err
	}
	config := ProcessScopeConfig{Manager: manager, RootDir: t.env.RootDir, StateDir: t.env.StateDir, SessionDir: t.env.SessionDir, ThreadID: t.env.SessionID, ActorID: defaultProcessOwnerID(t.env), ActorPath: currentExecutionPath(t.env), PermissionMode: t.env.PermissionMode, Boundary: t.boundary, FileScopeRoots: t.env.FileScopeRoots, Authorizer: t.authorizer, SandboxProvider: t.env.ProcessSandboxProvider, Reviewer: t.reviewer, ApproveForMe: t.approveForMe, ExecutionRoot: t.env.ExecRootDir, Disabled: t.IsToolDisabled("bash")}
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
