package tools

import (
	"context"
	"encoding/json"
	"errors"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// StartPluginProcess uses the calling conversation's shell policy and process
// manager. The extension host owns cancellation after this tool invocation ends.
func (t *Toolkit) StartPluginProcess(ctx context.Context, command, cwd string) (*proc.Process, *proc.Manager, error) {
	if t.env.ExecutionEnvironment != nil {
		return nil, nil, errors.New("plugin processes require a local execution environment")
	}
	args := bashArgs{Command: command, CWD: cwd, RunInBackground: true}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, nil, err
	}
	call := providers.ToolCall{Name: "bash", Arguments: string(raw)}
	tool := t.registry.Lookup("bash")
	if tool == nil || t.IsToolDisabled("bash") {
		return nil, nil, errors.New("bash is unavailable in this conversation")
	}
	// Approval of the outer extension tool does not approve an arbitrary shell command.
	ctx = context.WithValue(ctx, permissionCheckedContextKey{}, false)
	if err := t.checkPermission(ctx, buildToolInfoForArgs(tool, t.toolExposure("bash"), call.Arguments), call); err != nil {
		return nil, nil, err
	}
	manager, err := t.env.ProcessManager()
	if err != nil {
		return nil, nil, err
	}
	p, err := (&BashTool{env: t.env}).startBackground(ctx, args, false, proc.CompletionModeDetached)
	return p, manager, err
}

// RedactProcessOutput applies the same output boundary as the shell tools.
func (t *Toolkit) RedactProcessOutput(value string) string {
	return t.env.RedactToolOutput(value)
}
