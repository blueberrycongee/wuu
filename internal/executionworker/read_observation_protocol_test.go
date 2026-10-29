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
	otherMode, _ := json.Marshal(executionenv.ToolRequest{Actor: "a", PermissionMode: "standard", GitAttributionEnabled: true, Call: call})
	_, err = server.Handle(context.Background(), "execute", otherMode)
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
	// Same bytes in another mode: a versioned commit must preserve it.
	_, err = server.Handle(context.Background(), "execute", otherMode)
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

func TestReadObservationAbortAndCloseReleaseReceipts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("x\n"), 0600))
	server := New()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	_, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	req, _ := json.Marshal(executionenv.ToolRequest{Actor: "a", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}})
	for i := 0; i < readReceiptLimit+1; i++ {
		raw, err := server.Handle(context.Background(), "read/prepare", req)
		require.NoError(t, err)
		var response struct {
			Receipt string `json:"receipt"`
		}
		require.NoError(t, json.Unmarshal(raw, &response))
		abort, _ := json.Marshal(map[string]any{"actor": "a", "permission_mode": "read_only", "receipt": response.Receipt})
		if i%2 == 0 {
			_, err = server.Handle(context.Background(), "read/abort", abort)
		} else {
			_, err = server.Handle(context.Background(), "read/commit", abort)
			require.NoError(t, err)
			_, err = server.Handle(context.Background(), "read/release", abort)
		}
		require.NoError(t, err)
	}
	require.Empty(t, server.readReceipts)
	raw, err := server.Handle(context.Background(), "read/prepare", req)
	require.NoError(t, err)
	var response struct {
		Receipt string `json:"receipt"`
	}
	require.NoError(t, json.Unmarshal(raw, &response))
	require.NoError(t, server.Close())
	require.Empty(t, server.readReceipts)
	commit, _ := json.Marshal(map[string]any{"actor": "a", "permission_mode": "read_only", "receipt": response.Receipt})
	_, err = server.Handle(context.Background(), "read/commit", commit)
	require.Error(t, err)
}

func TestReadObservationWorkerInstanceIsStableAndUnique(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: t.TempDir(), Session: "thread", Identity: "env"})
	first := New()
	defer first.Close()
	second := New()
	defer second.Close()
	read := func(s *Server) string {
		raw, err := s.Handle(context.Background(), "initialize", init)
		require.NoError(t, err)
		var hello struct {
			Instance string `json:"worker_instance"`
		}
		require.NoError(t, json.Unmarshal(raw, &hello))
		require.NotEmpty(t, hello.Instance)
		return hello.Instance
	}
	original := read(first)
	require.Equal(t, original, read(first))
	require.NotEqual(t, original, read(second))
}

func TestReadObservationNativeRejectsByteAndImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("text\n"), 0600))
	// A valid one-pixel PNG exercises the native image path.
	png := []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0, 144, 119, 83, 222, 0, 0, 0, 12, 73, 68, 65, 84, 8, 215, 99, 248, 207, 192, 0, 0, 3, 1, 1, 0, 24, 221, 141, 176, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130}
	require.NoError(t, os.WriteFile(filepath.Join(root, "image.png"), png, 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	hello, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	require.Contains(t, string(hello), executionenv.NativeReadObservationCapability)
	for _, args := range []string{`{"path":"file.txt","byte_range":{"offset":0,"limit":2}}`, `{"path":"image.png"}`} {
		req, err := json.Marshal(executionenv.ToolRequest{Actor: "a", PermissionMode: "read_only", PrepareNativeObservation: true, Call: providers.ToolCall{Name: "read_file", Arguments: args}})
		require.NoError(t, err)
		_, err = server.Handle(context.Background(), "read/prepare", req)
		require.ErrorContains(t, err, "prepared read did not return a text observation")
		require.Empty(t, server.readReceipts)
	}
	actor, err := server.actor("a", "read_only", false, false)
	require.NoError(t, err)
	_, exists := actor.kit.ActiveFilesContextBlock()
	require.False(t, exists)
}
