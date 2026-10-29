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

func TestUnobservedReadDoesNotClaimNativeRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("contents\n"), 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "identity"})
	hello, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	require.Contains(t, string(hello), "read.unobserved.v1")
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}
	raw, _ := json.Marshal(map[string]any{"actor": "actor", "permission_mode": "read_only", "call": call, "suppress_read_observation": true})
	_, err = server.Handle(context.Background(), "execute", raw)
	require.NoError(t, err)
	actor, err := server.actor("actor", "read_only", false, false)
	require.NoError(t, err)
	_, ok := actor.kit.ActiveFilesContextBlock()
	require.False(t, ok, "unobserved read must not create a native active-file entry")
	raw, _ = json.Marshal(executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: call})
	_, err = server.Handle(context.Background(), "execute", raw)
	require.NoError(t, err)
	_, ok = actor.kit.ActiveFilesContextBlock()
	require.True(t, ok, "ordinary native reads keep their observation")
	raw, _ = json.Marshal(map[string]any{"actor": "actor", "permission_mode": "read_only", "call": call, "suppress_read_observation": true})
	_, err = server.Handle(context.Background(), "execute", raw)
	require.ErrorContains(t, err, "existing native read observation")
	_, ok = actor.kit.ActiveFilesContextBlock()
	require.True(t, ok, "failed replacement must retain the old native observation")
}
