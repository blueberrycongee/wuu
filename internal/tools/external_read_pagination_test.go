package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func TestExternalReadPaginationChecksRangesAndMakesHostCursor(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("source"), 0600))
	kit, err := New(root)
	require.NoError(t, err)
	scope, err := kit.PrepareExternalRead(context.Background(), `{"path":"file","offset":2,"limit":3}`, false)
	require.NoError(t, err)
	producer := toolresult.Result{StructuredContent: json.RawMessage(`{"text":"b\nc\n","total_lines":6,"next_cursor":"private-plugin-state"}`)}
	_, err = scope.Render(context.Background(), producer)
	require.Error(t, err, "older declarations cannot return plugin cursors")
	called := false
	result, err := scope.RenderPaginated(context.Background(), producer, nil, func(private string, next, remaining, total int) (string, error) {
		called = true
		require.Equal(t, "private-plugin-state", private)
		require.Equal(t, 4, next)
		require.Equal(t, 1, remaining)
		require.Equal(t, 6, total)
		return "wuu-read-v1:host-only", nil
	})
	require.NoError(t, err)
	require.True(t, called)
	var view map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TextProjection()), &view))
	continuation := view["continuation"].(map[string]any)["next"].(map[string]any)
	require.Equal(t, "wuu-read-v1:host-only", continuation["continuation"])
	require.NotContains(t, result.TextProjection(), "private-plugin-state")
	for _, invalid := range []string{
		`{"text":"","total_lines":6,"next_cursor":"x"}`,
		`{"text":"b\nc\n","total_lines":6}`,
		`{"text":"b\nc\nd\n","total_lines":6,"next_cursor":"x"}`,
		`{"text":"b\nc\n","total_lines":6,"next_cursor":""}`,
	} {
		_, err = scope.RenderPaginated(context.Background(), toolresult.Result{StructuredContent: json.RawMessage(invalid)}, nil, func(string, int, int, int) (string, error) { return "token", nil })
		require.Error(t, err, invalid)
	}
	canceled, cancel := context.WithCancel(context.Background())
	_, err = scope.RenderPaginated(canceled, producer, nil, func(string, int, int, int) (string, error) {
		cancel()
		return "wuu-read-v1:never-delivered", nil
	})
	require.ErrorIs(t, err, context.Canceled)
}
