package executionworker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestReadObservationProtocolBindsActorAndVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("same\n"), 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	hello, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	require.Contains(t, string(hello), "read.observation-commit.v1")
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}
	native, _ := json.Marshal(executionenv.ToolRequest{Actor: "a", PermissionMode: "read_only", Call: call})
	_, err = server.Handle(context.Background(), "execute", native)
	require.NoError(t, err)
	prep, err := server.Handle(context.Background(), "read/prepare", native)
	require.NoError(t, err)
	var response struct {
		Receipt string          `json:"receipt"`
		Result  json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(prep, &response))
	require.NotEmpty(t, response.Receipt)
	actor, err := server.actor("a", "read_only", false, false)
	require.NoError(t, err)
	_, ok := actor.kit.ActiveFilesContextBlock()
	require.True(t, ok)
	wrong, _ := json.Marshal(map[string]any{"actor": "b", "permission_mode": "read_only", "receipt": response.Receipt})
	_, err = server.Handle(context.Background(), "read/commit", wrong)
	require.Error(t, err)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	commit, _ := json.Marshal(map[string]any{"actor": "a", "permission_mode": "read_only", "receipt": response.Receipt})
	_, err = server.Handle(canceled, "read/commit", commit)
	require.Error(t, err)
	_, ok = actor.kit.ActiveFilesContextBlock()
	require.True(t, ok)
	// Same bytes, new native read: a versioned commit must preserve it.
	_, err = server.Handle(context.Background(), "execute", native)
	require.NoError(t, err)
	result, err := server.Handle(context.Background(), "read/commit", commit)
	require.NoError(t, err)
	require.Contains(t, string(result), `"committed":true`)
	require.Contains(t, string(result), `"changed":false`)
	_, ok = actor.kit.ActiveFilesContextBlock()
	require.True(t, ok)
	replay, err := server.Handle(context.Background(), "read/commit", commit)
	require.NoError(t, err)
	require.JSONEq(t, string(result), string(replay))
	next, err := server.Handle(context.Background(), "read/prepare", native)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(next, &response))
	commit, _ = json.Marshal(map[string]any{"actor": "a", "permission_mode": "read_only", "receipt": response.Receipt})
	result, err = server.Handle(context.Background(), "read/commit", commit)
	require.NoError(t, err)
	require.Contains(t, string(result), `"changed":true`)
	_, ok = actor.kit.ActiveFilesContextBlock()
	require.False(t, ok)
}
