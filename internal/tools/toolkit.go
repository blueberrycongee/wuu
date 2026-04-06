package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	defaultShellTimeoutSeconds = 60
	defaultMaxFileBytes        = 16 * 1024
	defaultMaxEntries          = 200
	maxToolOutputBytes         = 20 * 1024
)

// Toolkit executes local coding tools for the agent.
type Toolkit struct {
	rootDir string
}

// New creates a tool executor rooted in a workspace.
func New(rootDir string) (*Toolkit, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, errors.New("root directory is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root directory: %w", err)
	}
	return &Toolkit{rootDir: abs}, nil
}

// Definitions returns JSON-schema tool definitions.
func (t *Toolkit) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{
		{
			Name:        "run_shell",
			Description: "Run a shell command in the workspace and return output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "Shell command to execute.",
					},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Max runtime in seconds (1-300).",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "read_file",
			Description: "Read a file from workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative file path in workspace.",
					},
					"max_bytes": map[string]any{
						"type":        "integer",
						"description": "Max bytes to return.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Write full file content in workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative file path in workspace.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "File content.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "list_files",
			Description: "List entries under a directory in workspace.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Relative directory path, default is current workspace root.",
					},
					"max_entries": map[string]any{
						"type":        "integer",
						"description": "Maximum number of entries.",
					},
				},
			},
		},
	}
}

// Execute runs one tool call and returns JSON result.
func (t *Toolkit) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	switch call.Name {
	case "run_shell":
		return t.runShell(ctx, call.Arguments)
	case "read_file":
		return t.readFile(call.Arguments)
	case "write_file":
		return t.writeFile(call.Arguments)
	case "list_files":
		return t.listFiles(call.Arguments)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

func (t *Toolkit) runShell(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Command) == "" {
		return "", errors.New("run_shell requires command")
	}

	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultShellTimeoutSeconds
	}
	if timeout > 300 {
		timeout = 300
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", args.Command)
	cmd.Dir = t.rootDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			exitCode = 124
		} else {
			return "", fmt.Errorf("run command: %w", err)
		}
	}

	output := stdout.String() + stderr.String()
	trimmed, truncated := truncate(output, maxToolOutputBytes)

	result := map[string]any{
		"command":   args.Command,
		"exit_code": exitCode,
		"timed_out": errors.Is(runCtx.Err(), context.DeadlineExceeded),
		"truncated": truncated,
		"output":    trimmed,
	}
	return mustJSON(result)
}

func (t *Toolkit) readFile(argsJSON string) (string, error) {
	var args struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("read_file requires path")
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxFileBytes
	}
	if maxBytes > maxToolOutputBytes {
		maxBytes = maxToolOutputBytes
	}

	truncated := false
	if len(content) > maxBytes {
		content = content[:maxBytes]
		truncated = true
	}

	result := map[string]any{
		"path":      normalizeDisplayPath(t.rootDir, resolved),
		"size":      len(content),
		"truncated": truncated,
		"content":   string(content),
	}
	return mustJSON(result)
}

func (t *Toolkit) writeFile(argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("write_file requires path")
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", fmt.Errorf("create parent directory: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	result := map[string]any{
		"path":          normalizeDisplayPath(t.rootDir, resolved),
		"written_bytes": len(args.Content),
	}
	return mustJSON(result)
}

func (t *Toolkit) listFiles(argsJSON string) (string, error) {
	var args struct {
		Path       string `json:"path"`
		MaxEntries int    `json:"max_entries"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}

	resolved, err := t.resolvePath(args.Path)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("list directory: %w", err)
	}

	limit := args.MaxEntries
	if limit <= 0 {
		limit = defaultMaxEntries
	}
	if limit > 1000 {
		limit = 1000
	}

	resultEntries := make([]map[string]any, 0, min(limit, len(entries)))
	for i, entry := range entries {
		if i >= limit {
			break
		}

		item := map[string]any{
			"name":   entry.Name(),
			"is_dir": entry.IsDir(),
		}
		if !entry.IsDir() {
			info, statErr := entry.Info()
			if statErr == nil {
				item["size"] = info.Size()
			}
		}
		resultEntries = append(resultEntries, item)
	}

	result := map[string]any{
		"path":      normalizeDisplayPath(t.rootDir, resolved),
		"total":     len(entries),
		"truncated": len(entries) > limit,
		"entries":   resultEntries,
	}
	return mustJSON(result)
}

func (t *Toolkit) resolvePath(input string) (string, error) {
	candidate := strings.TrimSpace(input)
	if candidate == "" {
		candidate = "."
	}

	var abs string
	if filepath.IsAbs(candidate) {
		abs = filepath.Clean(candidate)
	} else {
		abs = filepath.Join(t.rootDir, candidate)
	}

	resolved, err := filepath.Abs(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	rel, err := filepath.Rel(t.rootDir, resolved)
	if err != nil {
		return "", fmt.Errorf("path relation check: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes workspace", input)
	}
	return resolved, nil
}

func decodeArgs(raw string, target any) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	if err := json.Unmarshal([]byte(trimmed), target); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func mustJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func truncate(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	return value[:maxBytes], true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func normalizeDisplayPath(rootDir, absPath string) string {
	rel, err := filepath.Rel(rootDir, absPath)
	if err != nil {
		return absPath
	}
	if rel == "." {
		return "."
	}
	return rel
}
