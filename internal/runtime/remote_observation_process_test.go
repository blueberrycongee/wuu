package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestRemoteObservationRealWorkerTransactions(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance script")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("same\n"), 0600))
	env := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: root}, "pinned", "thread", t.TempDir())
	defer env.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"file.txt"}`}}
	_, err := env.Execute(ctx, req)
	require.NoError(t, err)
	failed, err := env.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = failed.Commit(canceled)
	require.ErrorIs(t, err, context.Canceled)
	// A canceled/abandoned transaction leaves the native observation available.
	afterCancel, err := env.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := afterCancel.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	none, err := env.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err = none.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed)
	// Repeating an identical native read after preparation must survive ABA.
	_, err = env.Execute(ctx, req)
	require.NoError(t, err)
	old, err := env.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	_, err = env.Execute(ctx, req)
	require.NoError(t, err)
	changed, err = old.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed)
	fresh, err := env.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err = fresh.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = fresh.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "receipt replay is idempotent")
}
