package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// PluginManagementRequest carries only scoped source paths and operations.
// The host chooses its executable, configuration and plugin storage.
type PluginManagementRequest struct {
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	ID     string `json:"id,omitempty"`
	CWD    string `json:"-"`
}

type PluginManager func(context.Context, PluginManagementRequest) (any, error)

type PluginManagerTool struct{ env *Env }

func (t *PluginManagerTool) Name() string            { return "plugin_manager" }
func (t *PluginManagerTool) IsReadOnly() bool        { return false }
func (t *PluginManagerTool) IsConcurrencySafe() bool { return false }
func (t *PluginManagerTool) Classify(raw string) ToolClassification {
	var args PluginManagementRequest
	if json.Unmarshal([]byte(raw), &args) == nil && args.Action == "status" {
		return ToolClassification{ReadOnly: true, ConcurrencySafe: true, Risk: ToolRiskLow}
	}
	return ToolClassification{Risk: ToolRiskHigh, Reason: "changes trusted host plugin code or activation outside the workspace sandbox"}
}
func (t *PluginManagerTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), Description: "Manage Wuu plugins in this conversation. Author a TypeScript file with write_file, then apply its workspace path to publish a trusted development generation. apply may execute source code and package build scripts on the host; a confined session requires configured Approve for me authorization. status reports published and current-conversation generations separately. New tools become available at the next idle turn boundary, not midway through this turn. enable/disable address an existing plugin ID; enable never grants missing trust. This does not grant general access to WUU_HOME.", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "apply", "enable", "disable"}}, "path": map[string]any{"type": "string", "description": "Workspace-local TypeScript file or plugin directory for apply."}, "id": map[string]any{"type": "string", "description": "Exact plugin ID, required for enable/disable and optional for status."}}, "required": []string{"action"}, "additionalProperties": false}}
}
func (t *PluginManagerTool) Execute(ctx context.Context, raw string) (string, error) {
	var args PluginManagementRequest
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	args.ID = strings.TrimSpace(args.ID)
	switch args.Action {
	case "status":
		if args.Path != "" {
			return "", fmt.Errorf("status does not accept path")
		}
	case "apply":
		if strings.TrimSpace(args.Path) == "" || args.ID != "" {
			return "", fmt.Errorf("apply requires path and does not accept id")
		}
		if t.env.ExecutionEnvironment != nil {
			return "", fmt.Errorf("plugin source must be in this host's workspace; remote execution environments are not supported")
		}
		path, err := t.env.ResolvePath(args.Path)
		if err != nil {
			return "", err
		}
		if err := rejectSensitiveReadPath(t.env, t.Name(), path); err != nil {
			return "", err
		}
		// Resolve the logical workspace path before rebasing, just like write_file.
		// In particular, a newly authored checkout file need not exist in the parent.
		path, err = t.env.ExecPath(ctx, path)
		if err != nil {
			return "", err
		}
		path, err = resolveReadTarget(ctx, t.env, t.Name(), path, false)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() && (!info.Mode().IsRegular() || strings.ToLower(filepath.Ext(path)) != ".ts") {
			return "", fmt.Errorf("apply requires a regular TypeScript .ts file or plugin directory")
		}
		args.Path = path
	case "enable", "disable":
		if args.ID == "" || args.Path != "" {
			return "", fmt.Errorf("%s requires id and does not accept path", args.Action)
		}
	default:
		return "", fmt.Errorf("unsupported plugin action %q", args.Action)
	}
	if t.env.PluginManager == nil {
		return "", fmt.Errorf("plugin management is unavailable in this runtime")
	}
	cwd, err := t.env.ExecRootDir(ctx)
	if err != nil {
		return "", err
	}
	args.CWD = cwd
	result, err := t.env.PluginManager(ctx, args)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
func (t *Toolkit) SetPluginManager(manager PluginManager) {
	t.env.PluginManager = manager
	t.rebuildRegistry()
}
