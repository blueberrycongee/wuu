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

// PluginManagementRequest carries scoped source paths and explicit evaluator argv.
// The host chooses the management executable, configuration and plugin storage.
type PluginManagementRequest struct {
	Action          string   `json:"action"`
	Path            string   `json:"path,omitempty"`
	ID              string   `json:"id,omitempty"`
	CandidateID     string   `json:"candidate_id,omitempty"`
	ExpectedCurrent *string  `json:"expected_current,omitempty"`
	CandidateIDs    []string `json:"candidate_ids,omitempty"`
	Command         []string `json:"command,omitempty"`
	Concurrency     int      `json:"concurrency,omitempty"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	CWD             string   `json:"-"`
}

type PluginManager func(context.Context, PluginManagementRequest) (any, error)

type PluginManagerTool struct{ env *Env }

func (t *PluginManagerTool) Name() string            { return "plugin_manager" }
func (t *PluginManagerTool) IsReadOnly() bool        { return false }
func (t *PluginManagerTool) IsConcurrencySafe() bool { return false }
func (t *PluginManagerTool) Classify(raw string) ToolClassification {
	var args PluginManagementRequest
	if json.Unmarshal([]byte(raw), &args) == nil && (args.Action == "status" || args.Action == "candidates") {
		return ToolClassification{ReadOnly: true, ConcurrencySafe: true, Risk: ToolRiskLow}
	}
	return ToolClassification{Risk: ToolRiskHigh, Reason: "executes trusted host plugin code or changes activation outside the workspace sandbox"}
}
func (t *PluginManagerTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), Description: "Manage Wuu plugins in this conversation. apply publishes a workspace TypeScript file or plugin directory. capture archives an immutable candidate without publishing; candidates lists this workspace's captures. eval runs explicit command argv against captured candidates with fresh WUU_HOME and scratch directories, bounded concurrency and timeouts, and saved evidence; run status determines success. Command argv and output are saved; do not include secrets. These are trusted host processes, not security sandboxes. select/rollback require the original source to already have development authorization, and publish an exact candidate_id only if expected_current matches the current published fingerprint (empty means none). Rollback changes code only; it does not undo business state or external effects. Capture, evaluation and publication may execute host code and require the existing host authorization in confined sessions. New generations are adopted at the next idle turn boundary. status separates published and current-conversation generations; enable never grants missing trust.", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{
			"action":           map[string]any{"type": "string", "enum": []string{"status", "apply", "enable", "disable", "capture", "candidates", "eval", "select", "rollback"}},
			"path":             map[string]any{"type": "string", "description": "Workspace-local TypeScript file or plugin directory for apply/capture."},
			"id":               map[string]any{"type": "string", "description": "Exact plugin ID for enable/disable/eval/select/rollback; optional for status/candidates."},
			"candidate_id":     map[string]any{"type": "string", "description": "Exact archived candidate ID for select/rollback."},
			"expected_current": map[string]any{"type": "string", "description": "Required for select/rollback. Exact current published fingerprint; empty string explicitly expects plugin absent."},
			"candidate_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 32, "description": "Exact candidate IDs to evaluate for this plugin."},
			"command":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 256, "description": "Evaluation executable and arguments, passed directly without a shell. Runs in a fresh scratch directory with an isolated WUU_HOME."},
			"concurrency":      map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "description": "Parallel evaluation limit; defaults to 2."},
			"timeout_seconds":  map[string]any{"type": "integer", "minimum": 1, "maximum": 1800, "description": "Per-run evaluation deadline; defaults to 60 seconds."},
		}, "required": []string{"action"}, "additionalProperties": false}}
}
func (t *PluginManagerTool) Execute(ctx context.Context, raw string) (string, error) {
	var args PluginManagementRequest
	if err := decodeArgs(raw, &args); err != nil {
		return "", err
	}
	args.ID = strings.TrimSpace(args.ID)
	args.CandidateID = strings.TrimSpace(args.CandidateID)
	hasEvaluation := len(args.CandidateIDs) != 0 || len(args.Command) != 0 || args.Concurrency != 0 || args.TimeoutSeconds != 0
	hasSelection := args.CandidateID != "" || args.ExpectedCurrent != nil
	switch args.Action {
	case "status", "candidates":
		if args.Path != "" || hasSelection || hasEvaluation {
			return "", fmt.Errorf("%s accepts only an optional id", args.Action)
		}
	case "apply", "capture":
		if strings.TrimSpace(args.Path) == "" || args.ID != "" || hasSelection || hasEvaluation {
			return "", fmt.Errorf("%s requires path and accepts no experiment options or id", args.Action)
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
		// A newly authored checkout file need not exist in the parent.
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
			return "", fmt.Errorf("%s requires a regular TypeScript .ts file or plugin directory", args.Action)
		}
		args.Path = path
	case "enable", "disable":
		if args.ID == "" || args.Path != "" || hasSelection || hasEvaluation {
			return "", fmt.Errorf("%s requires id and accepts no path or experiment options", args.Action)
		}
	case "select", "rollback":
		if args.ID == "" || args.CandidateID == "" || args.ExpectedCurrent == nil || args.Path != "" || hasEvaluation {
			return "", fmt.Errorf("%s requires id, candidate_id, and expected_current only", args.Action)
		}
	case "eval":
		if args.ID == "" || len(args.CandidateIDs) == 0 || len(args.CandidateIDs) > 32 || len(args.Command) == 0 || len(args.Command) > 256 || args.Path != "" || hasSelection {
			return "", fmt.Errorf("eval requires id, 1-32 candidate_ids, and command argv; path and selection options are unsupported")
		}
		for _, id := range args.CandidateIDs {
			if strings.TrimSpace(id) == "" {
				return "", fmt.Errorf("candidate_ids must not contain an empty id")
			}
		}
		commandBytes := 0
		for _, arg := range args.Command {
			commandBytes += len(arg)
			if strings.ContainsRune(arg, 0) {
				return "", fmt.Errorf("command arguments must not contain NUL")
			}
		}
		if strings.TrimSpace(args.Command[0]) == "" || commandBytes > 16*1024 {
			return "", fmt.Errorf("command requires a nonempty executable and at most 16 KiB of arguments")
		}
		if args.Concurrency == 0 {
			args.Concurrency = 2
		}
		if args.TimeoutSeconds == 0 {
			args.TimeoutSeconds = 60
		}
		if args.Concurrency < 1 || args.Concurrency > 8 || args.TimeoutSeconds < 1 || args.TimeoutSeconds > 1800 {
			return "", fmt.Errorf("eval concurrency must be 1-8 and timeout_seconds must be 1-1800")
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
