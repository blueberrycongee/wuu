package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ---------------------------------------------------------------------------
// read_file
// ---------------------------------------------------------------------------

type ReadFileTool struct{ env *Env }

func NewReadFileTool(env *Env) *ReadFileTool { return &ReadFileTool{env: env} }

func (t *ReadFileTool) Name() string            { return "read_file" }
func (t *ReadFileTool) IsReadOnly() bool         { return true }
func (t *ReadFileTool) IsConcurrencySafe() bool  { return true }

func (t *ReadFileTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "read_file",
		Description: "Reads a file from the workspace. " +
			"Assume this tool can read any file in the workspace.\n\n" +
			"Usage:\n" +
			"- The path parameter is relative to the workspace root\n" +
			"- Returns file content with size metadata and truncation info\n" +
			"- Large files (>256KB) are truncated at a valid UTF-8 boundary\n" +
			"- This tool can only read files, not directories — use list_files for directories\n" +
			"- Binary files are not supported; use run_shell for binary inspection",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative file path in workspace.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadFileTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("read_file requires path")
	}

	resolved, err := t.env.ResolvePath(args.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	fullSize := len(content)
	returned := content
	truncated := false
	if fullSize > defaultMaxFileBytes {
		cut := defaultMaxFileBytes
		for cut > 0 && content[cut-1]&0xC0 == 0x80 {
			cut--
		}
		returned = content[:cut]
		truncated = true
	}

	result := map[string]any{
		"path":          t.env.NormalizeDisplayPath(resolved),
		"size":          fullSize,
		"returned_size": len(returned),
		"truncated":     truncated,
		"content":       string(returned),
	}
	return mustJSON(result)
}

// ---------------------------------------------------------------------------
// write_file
// ---------------------------------------------------------------------------

type WriteFileTool struct{ env *Env }

func NewWriteFileTool(env *Env) *WriteFileTool { return &WriteFileTool{env: env} }

func (t *WriteFileTool) Name() string            { return "write_file" }
func (t *WriteFileTool) IsReadOnly() bool         { return false }
func (t *WriteFileTool) IsConcurrencySafe() bool  { return false }

func (t *WriteFileTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "write_file",
		Description: "Writes full file content to the workspace. Creates parent directories automatically.\n\n" +
			"Usage:\n" +
			"- Prefer edit_file for modifying existing files — it only sends the diff\n" +
			"- Only use this tool to create new files or for complete rewrites\n" +
			"- Returns a structured diff showing what changed",
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
	}
}

func (t *WriteFileTool) Execute(_ context.Context, argsJSON string) (string, error) {
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

	resolved, err := t.env.ResolvePath(args.Path)
	if err != nil {
		return "", err
	}

	oldContent, _ := os.ReadFile(resolved)

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", fmt.Errorf("create parent directory: %w", err)
	}
	if err := os.WriteFile(resolved, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	if t.env.OnFileChanged != nil {
		t.env.OnFileChanged(resolved)
	}

	result := map[string]any{
		"path":          t.env.NormalizeDisplayPath(resolved),
		"written_bytes": len(args.Content),
	}

	if len(oldContent) > 0 {
		result["diff"] = computeDiff(string(oldContent), args.Content, 3)
	} else {
		lineCount := strings.Count(args.Content, "\n")
		if len(args.Content) > 0 && !strings.HasSuffix(args.Content, "\n") {
			lineCount++
		}
		result["diff"] = DiffResult{NewFile: true, Lines: lineCount}
	}
	return mustJSON(result)
}

// ---------------------------------------------------------------------------
// list_files
// ---------------------------------------------------------------------------

type ListFilesTool struct{ env *Env }

func NewListFilesTool(env *Env) *ListFilesTool { return &ListFilesTool{env: env} }

func (t *ListFilesTool) Name() string            { return "list_files" }
func (t *ListFilesTool) IsReadOnly() bool         { return true }
func (t *ListFilesTool) IsConcurrencySafe() bool  { return true }

func (t *ListFilesTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "list_files",
		Description: "Lists entries under a directory in the workspace.\n\n" +
			"Usage:\n" +
			"- Returns name, is_dir, and size for each entry\n" +
			"- Defaults to workspace root when path is omitted\n" +
			"- Truncated at 1000 entries for large directories",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative directory path, default is current workspace root.",
				},
			},
		},
	}
}

func (t *ListFilesTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}

	resolved, err := t.env.ResolvePath(args.Path)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("list directory: %w", err)
	}

	limit := defaultMaxEntries

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
		"path":      t.env.NormalizeDisplayPath(resolved),
		"total":     len(entries),
		"truncated": len(entries) > limit,
		"entries":   resultEntries,
	}
	return mustJSON(result)
}

// ---------------------------------------------------------------------------
// edit_file
// ---------------------------------------------------------------------------

type EditFileTool struct{ env *Env }

func NewEditFileTool(env *Env) *EditFileTool { return &EditFileTool{env: env} }

func (t *EditFileTool) Name() string            { return "edit_file" }
func (t *EditFileTool) IsReadOnly() bool         { return false }
func (t *EditFileTool) IsConcurrencySafe() bool  { return false }

func (t *EditFileTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "edit_file",
		Description: "Performs exact string replacement in a file.\n\n" +
			"Usage:\n" +
			"- You should read the file before editing to understand its content\n" +
			"- Provide old_text (must match exactly once in the file) and new_text\n" +
			"- The edit will FAIL if old_text is not unique — provide more surrounding context to disambiguate\n" +
			"- Use empty new_text to delete a section\n" +
			"- Prefer this over write_file for modifications — it only sends the diff\n" +
			"- Returns a structured diff showing what changed",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Relative file path in workspace.",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "Exact text to find and replace. Must match exactly once in the file.",
				},
				"new_text": map[string]any{
					"type":        "string",
					"description": "Text to replace old_text with. Use empty string to delete.",
				},
			},
			"required": []string{"path", "old_text", "new_text"},
		},
	}
}

func (t *EditFileTool) Execute(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", errors.New("edit_file requires path")
	}
	if args.OldText == "" {
		return "", errors.New("edit_file requires old_text")
	}

	resolved, err := t.env.ResolvePath(args.Path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	text := string(content)
	count := strings.Count(text, args.OldText)
	if count == 0 {
		return "", errors.New("old_text not found in file")
	}
	if count > 1 {
		return "", fmt.Errorf("old_text matches %d times, must be unique", count)
	}

	newContent := strings.Replace(text, args.OldText, args.NewText, 1)
	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	if t.env.OnFileChanged != nil {
		t.env.OnFileChanged(resolved)
	}

	diff := computeDiff(text, newContent, 3)
	result := map[string]any{
		"path": t.env.NormalizeDisplayPath(resolved),
		"diff": diff,
	}
	return mustJSON(result)
}
