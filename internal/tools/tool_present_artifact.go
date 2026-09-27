package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ArtifactPublishRequest contains a path accepted by the tool's read boundary.
// Session identity is supplied by the host, never by model arguments.
type ArtifactPublishRequest struct {
	Path, ThreadID, StateDir, CWD, CallID string
}

type ArtifactPublisher func(context.Context, ArtifactPublishRequest) (toolresult.ContentPart, error)

type PresentArtifactTool struct{ env *Env }

func NewPresentArtifactTool(env *Env) *PresentArtifactTool { return &PresentArtifactTool{env: env} }
func (t *PresentArtifactTool) Name() string                { return "present_artifact" }

// Like read_file's result archive, this only writes host-owned session storage.
// It never changes the source file or the workspace.
func (t *PresentArtifactTool) IsReadOnly() bool        { return true }
func (t *PresentArtifactTool) IsConcurrencySafe() bool { return true }

func (t *PresentArtifactTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        t.Name(),
		Description: "Present an existing local file to the user as a standalone artifact, separate from file-change diffs. Saves an immutable snapshot: images (including SVG) appear inline with click-to-enlarge; other files appear as output cards. After creating a requested image, diagram, chart, or document with code, call this tool with its path. Use only for intended deliverables, not every file you read or edit. This displays the artifact to the user; it does not inspect the image for you. Do not duplicate the preview with Markdown images. Maximum file size: 256 MiB.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Existing file, relative to the workspace root or an absolute path within the allowed file scope."},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (t *PresentArtifactTool) Execute(ctx context.Context, args string) (string, error) {
	result, err := t.ExecuteResult(ctx, args)
	return result.TextProjection(), err
}

func (t *PresentArtifactTool) ExecuteResult(ctx context.Context, args string) (toolresult.Result, error) {
	return t.ExecuteResultCall(ctx, providers.ToolCall{Name: t.Name(), Arguments: args})
}

func (t *PresentArtifactTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(call.Arguments, &args); err != nil {
		return toolresult.Result{}, err
	}
	if strings.TrimSpace(args.Path) == "" {
		return toolresult.Result{}, fmt.Errorf("present_artifact requires path")
	}
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if t.env.ArtifactPublisher == nil || strings.TrimSpace(t.env.SessionID) == "" {
		return toolresult.Result{}, fmt.Errorf("artifact storage is unavailable for this session")
	}
	resolved, _, managed, err := t.env.ResolveReadPath(args.Path)
	if err != nil {
		return toolresult.Result{}, err
	}
	if !managed {
		if err := rejectSensitiveReadPath(t.env, t.Name(), resolved); err != nil {
			return toolresult.Result{}, err
		}
		resolved, err = t.env.ExecPath(ctx, resolved)
		if err != nil {
			return toolresult.Result{}, err
		}
	}
	resolved, err = resolveReadTarget(ctx, t.env, t.Name(), resolved, managed)
	if err != nil {
		return toolresult.Result{}, err
	}
	part, err := t.env.ArtifactPublisher(ctx, ArtifactPublishRequest{
		Path: resolved, ThreadID: t.env.SessionID, StateDir: t.env.StateDir, CWD: t.env.RootDir, CallID: call.ID,
	})
	if err != nil {
		return toolresult.Result{}, err
	}
	return toolresult.Result{Content: []toolresult.ContentPart{part}}, nil
}

func (t *Toolkit) SetArtifactPublisher(publish ArtifactPublisher) {
	t.env.ArtifactPublisher = publish
	t.rebuildRegistry()
}
