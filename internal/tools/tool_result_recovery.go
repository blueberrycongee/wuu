package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type resultRecoveryContextKey struct{}

var archivedResultName = regexp.MustCompile(`^[a-f0-9]{64}\.txt$`)

// RecoverToolResultTool reads only result files in the current session. Cursor
// integrity checks detect stale content; they do not authenticate who issued it.
type RecoverToolResultTool struct{ toolkit *Toolkit }

func (t *RecoverToolResultTool) Name() string            { return "recover_tool_result" }
func (t *RecoverToolResultTool) IsReadOnly() bool        { return true }
func (t *RecoverToolResultTool) IsConcurrencySafe() bool { return true }
func (t *RecoverToolResultTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), Description: "Continue reading a tool result file in this session. Pass its continuation token unchanged. This reads archived tool output only; use read_file for workspace files.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"continuation": map[string]any{"type": "string", "description": "The continuation token returned for an archived tool result."}}, "required": []string{"continuation"}}}
}
func (t *RecoverToolResultTool) Execute(ctx context.Context, args string) (string, error) {
	result, err := t.ExecuteResult(ctx, args)
	return result.TextProjection(), err
}
func (t *RecoverToolResultTool) ExecuteResult(ctx context.Context, args string) (toolresult.Result, error) {
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	var request struct {
		Continuation string `json:"continuation"`
	}
	if err := decodeArgs(args, &request); err != nil {
		return toolresult.Result{}, err
	}
	cursor, err := decodeReadFileContinuation(request.Continuation)
	if err != nil || cursor.ByteOffset == nil || cursor.ByteEndOffset == nil || len(cursor.ExpectedSHA256) != 64 {
		return toolresult.Result{}, errors.New("invalid archived result continuation")
	}
	kit := t.toolkit
	if kit.env == nil || strings.TrimSpace(kit.env.SessionDir) == "" {
		return toolresult.Result{}, errors.New("result recovery requires a session")
	}
	resolved, _, managed, err := kit.env.ResolveReadPath(cursor.Path)
	resultDir := filepath.Join(kit.env.SessionDir, "tool-results")
	if err != nil || !managed || filepath.Clean(filepath.Dir(resolved)) != filepath.Clean(resultDir) || !archivedResultName.MatchString(filepath.Base(resolved)) || !pathWithinRoot(resultDir, resolved) {
		return toolresult.Result{}, errors.New("continuation is not a current-session result archive")
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return toolresult.Result{}, errors.New("result archive is unavailable")
	}
	if kit.IsToolDisabled("read_file") || !kit.SupportsTool("read_file") {
		return toolresult.Result{}, errors.New("result recovery requires read_file permission")
	}
	native := NewReadFileTool(kit.env)
	call := providers.ToolCall{Name: "read_file", Arguments: args}
	if err := kit.checkPermission(ctx, buildToolInfoForArgs(native, kit.toolExposure("read_file"), args), call); err != nil {
		return toolresult.Result{}, fmt.Errorf("result recovery: %w", err)
	}
	return native.ExecuteResult(context.WithValue(ctx, resultRecoveryContextKey{}, true), args)
}
