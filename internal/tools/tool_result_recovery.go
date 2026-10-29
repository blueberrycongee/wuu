package tools

import (
	"context"
	"errors"
	"fmt"
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
	native := NewReadFileTool(kit.env)
	exposure := kit.nativeReadExposure(native)
	if kit.IsToolDisabled("read_file") || exposure == ToolExposureHidden {
		return toolresult.Result{}, errors.New("result recovery requires read_file permission")
	}
	call := providers.ToolCall{Name: "read_file", Arguments: args}
	if err := kit.checkPermission(ctx, buildToolInfoForArgs(native, exposure, args), call); err != nil {
		return toolresult.Result{}, fmt.Errorf("result recovery: %w", err)
	}
	if kit.env.sessionArtifactRoot == nil {
		return toolresult.Result{}, errors.New("result archive is unavailable")
	}
	file, err := kit.env.sessionArtifactRoot.open(filepath.Join("tool-results", filepath.Base(resolved)))
	if err != nil {
		return toolresult.Result{}, errors.New("result archive is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return toolresult.Result{}, errors.New("result archive is unavailable")
	}
	display := kit.env.normalizeSessionDisplayPath(resolved)
	page, err := readFileByteWindowFromOpen(context.WithValue(ctx, resultRecoveryContextKey{}, true), kit.env, file, display, *cursor.ByteOffset, cursor.Limit, *cursor.ByteEndOffset, strings.TrimSpace(cursor.ExpectedSHA256), info.Size())
	if err != nil {
		return toolresult.Result{}, err
	}
	return toolresult.FromText(page), nil
}

// Native read admission depends on its own surface, not extension health.
func (t *Toolkit) nativeReadExposure(native Tool) ToolExposure {
	if t.IsToolDisabled("read_file") {
		return ToolExposureHidden
	}
	surface := t.activeCompiledSurface()
	if surface.ProfileName == "" {
		return ToolExposureDirect
	}
	if !activeSurfaceAllowsKnownTool(surface, native) {
		return ToolExposureHidden
	}
	return activeSurfaceToolExposure(surface, "read_file")
}
