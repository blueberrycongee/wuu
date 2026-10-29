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

func TestWorkerUnobservedReadSharesActorStateAcrossModes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	_, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	call := providers.ToolCall{Name: "read_file", Arguments: `{"path":"a.txt"}`}
	request := func(actor, mode string, attribution, shell, unobserved bool) []byte {
		data, _ := json.Marshal(executionenv.ToolRequest{Actor: actor, PermissionMode: mode, GitAttributionEnabled: attribution, ShellEarlyHandoffDisabled: shell, SuppressReadObservation: unobserved, Call: call})
		return data
	}
	_, err = server.Handle(context.Background(), "execute", request("a", "standard", false, false, false))
	require.NoError(t, err)
	for _, tc := range []struct {
		mode               string
		attribution, shell bool
	}{{"read_only", false, false}, {"standard", true, false}, {"standard", false, true}} {
		_, err = server.Handle(context.Background(), "execute", request("a", tc.mode, tc.attribution, tc.shell, true))
		require.ErrorContains(t, err, "existing native read observation")
	}
	_, err = server.Handle(context.Background(), "execute", request("b", "read_only", false, false, true))
	require.NoError(t, err, "different actors must remain isolated")
}
