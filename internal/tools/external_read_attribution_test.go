package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

type attributionReplacement struct{ Tool }

func (r attributionReplacement) NativeToolForCall(args string) (Tool, bool) {
	var request struct {
		Continuation string `json:"continuation"`
	}
	_ = json.Unmarshal([]byte(args), &request)
	return r.Tool, request.Continuation != ""
}

func TestExternalReadArchivedPagesKeepUnverifiedSource(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "source.txt"), []byte("native"), 0600))
	kit, err := New(root)
	require.NoError(t, err)
	require.NoError(t, kit.SetSessionDir(t.TempDir()))
	defer kit.CloseSessionArtifacts()
	require.NoError(t, kit.SetNativeToolReplacements(map[string]func(Tool) Tool{"read_file": func(native Tool) Tool { return attributionReplacement{Tool: native} }}))
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"source.txt"}`)
	require.NoError(t, err)
	payload, _ := json.Marshal(map[string]any{"text": strings.Repeat("外部内容🙂", 16000), "total_lines": 1})
	result, err := scope.Render(toolresult.Result{StructuredContent: payload})
	require.NoError(t, err)
	call := providers.ToolCall{ID: "external", Name: "read_file", Arguments: `{"path":"source.txt"}`}
	settled := kit.FinalizeToolResult(call, result)
	require.NotNil(t, settled.StructuredContent)
	var structured map[string]any
	require.NoError(t, json.Unmarshal(settled.StructuredContent, &structured))
	require.Equal(t, "extension", structured["source"])
	require.Equal(t, false, structured["content_verified"])
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(settled.TextProjection()), &first))
	require.Equal(t, "extension", first["source"])
	require.Equal(t, false, first["content_verified"])
	artifactArgs, _ := json.Marshal(map[string]string{"path": first["artifact_ref"].(string)})
	artifact, err := NewReadFileTool(kit.env).ExecuteResult(context.Background(), string(artifactArgs))
	require.NoError(t, err)
	var artifactView map[string]any
	require.NoError(t, json.Unmarshal([]byte(artifact.TextProjection()), &artifactView))
	require.Equal(t, "tool_result_archive", artifactView["source"])
	require.Equal(t, false, artifactView["content_verified"])
	next := first["continuation"].(map[string]any)["next"].(map[string]any)
	require.NotEmpty(t, next)
	for _, name := range []string{"read_file", "recover_tool_result"} {
		t.Run(name, func(t *testing.T) {
			current := next
			pages := 0
			for {
				args, _ := json.Marshal(current)
				page, err := kit.Execute(context.Background(), providers.ToolCall{ID: name, Name: name, Arguments: string(args)})
				require.NoError(t, err)
				var view map[string]any
				require.NoError(t, json.Unmarshal([]byte(page), &view))
				require.Equal(t, "extension", view["source"])
				require.Equal(t, false, view["content_verified"])
				pages++
				cont := view["continuation"].(map[string]any)
				if cont["has_more"] != true {
					break
				}
				current = cont["next"].(map[string]any)
			}
			require.Greater(t, pages, 1)
		})
	}
	// Native continuation tokens are not authenticated. Removing a provenance
	// field must never make a recovered archive claim verified native content.
	token := next["continuation"].(string)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	var cursor map[string]any
	require.NoError(t, json.Unmarshal(raw, &cursor))
	delete(cursor, "result_source")
	raw, _ = json.Marshal(cursor)
	args, _ := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
	page, err := kit.Execute(context.Background(), providers.ToolCall{ID: "forged", Name: "recover_tool_result", Arguments: string(args)})
	require.NoError(t, err)
	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(page), &view))
	require.NotEqual(t, "native", view["source"])
	require.Equal(t, false, view["content_verified"])
}
