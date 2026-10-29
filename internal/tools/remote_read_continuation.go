package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ContinueRemoteReadOnly rechecks the original host policy and dispatches the
// exact captured call to the selected remote worker. It is not a generic plugin
// RPC: only a host-owned continuation may supply the captured call and tool.
func (t *Toolkit) ContinueRemoteReadOnly(ctx context.Context, original Tool, call providers.ToolCall) (toolresult.Result, error) {
	if t == nil || t.env == nil {
		return toolresult.Result{}, errors.New("remote execution environment is unavailable")
	}
	actor, actorPath := t.ExecutionActor()
	return t.ContinueRemoteReadOnlyOn(ctx, t.env.ExecutionEnvironment, actor, actorPath, original, call)
}

// ContinueRemoteReadOnlyOn uses the environment captured when the host admitted
// the call; plugin input cannot select or replace this executor.
func (t *Toolkit) ContinueRemoteReadOnlyOn(ctx context.Context, remote executionenv.Executor, actor, actorPath string, original Tool, call providers.ToolCall) (toolresult.Result, error) {
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if t == nil || t.env == nil || remote == nil {
		return toolresult.Result{}, errors.New("remote execution environment is unavailable")
	}
	currentActor, currentPath := t.ExecutionActor()
	if actor == "" || currentActor != actor || currentPath != actorPath {
		return toolresult.Result{}, errors.New("remote continuation actor changed")
	}
	location, ok := remote.(interface {
		Root() string
		Backend() string
		Identity() string
	})
	if !ok || location.Root() == "" || location.Backend() == "" || location.Identity() == "" {
		return toolresult.Result{}, errors.New("authenticated remote workspace unavailable")
	}
	ctx = context.WithValue(ctx, permissionWorkspaceContextKey{}, location.Root())
	if original == nil || original.Name() != call.Name || (call.Name != "read_file" && call.Name != "grep") {
		return toolresult.Result{}, errors.New("unsupported remote read continuation")
	}
	if t.IsToolDisabled(call.Name) {
		return toolresult.Result{}, fmt.Errorf("tool %q is disabled", call.Name)
	}
	if err := validateToolArgumentsJSON(call.Arguments); err != nil {
		return toolresult.Result{}, err
	}
	if validator, ok := original.(InputValidatingTool); ok {
		if err := validator.ValidateInput(call.Arguments); err != nil {
			return toolresult.Result{}, err
		}
	}
	if err := t.checkPermission(ctx, t.toolInfoForCall(original, call.Arguments), call); err != nil {
		return toolresult.Result{}, err
	}
	request := executionenv.ToolRequest{Call: call, PermissionMode: t.env.PermissionMode, Actor: actor, ShellEarlyHandoffDisabled: t.env.ShellEarlyHandoffDisabled, GitAttributionEnabled: !t.env.GitAttributionDisabled}
	if call.Name == "read_file" {
		executor, ok := remote.(executionenv.UnobservedReadExecutor)
		if !ok {
			return toolresult.Result{}, errors.New("remote execution environment does not support unobserved reads")
		}
		return executor.ExecuteUnobservedRead(ctx, request)
	}
	return remote.Execute(ctx, request)
}

// AuthorizeRemoteTool applies plugin authority to the captured remote root.
func (t *Toolkit) AuthorizeRemoteTool(ctx context.Context, remote executionenv.Executor, call providers.ToolCall, metadata agent.ToolMetadata) error {
	location, ok := remote.(interface {
		Root() string
		Backend() string
		Identity() string
	})
	if !ok || location.Root() == "" || location.Backend() == "" || location.Identity() == "" {
		return errors.New("authenticated remote workspace unavailable")
	}
	return t.AuthorizeTool(context.WithValue(ctx, permissionWorkspaceContextKey{}, location.Root()), call, metadata)
}
