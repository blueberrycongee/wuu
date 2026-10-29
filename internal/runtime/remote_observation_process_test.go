package runtime

import (
	"context"
	"encoding/json"
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

func TestRemoteObservationRealWorkerBatch(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("same\n"), 0600))
	}
	worker := executionenv.NewClient([]string{binary, "execution-worker"}, os.Environ())
	defer worker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hello, err := worker.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "batch"})
	require.NoError(t, err)
	var instance struct {
		ID string `json:"worker_instance"`
	}
	require.NoError(t, json.Unmarshal(hello, &instance))
	require.NotEmpty(t, instance.ID)
	req := func(actor, name string) executionenv.ToolRequest {
		return executionenv.ToolRequest{Actor: actor, PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"` + name + `"}`}}
	}
	for _, name := range []string{"a", "b"} {
		_, err = worker.Call(ctx, "execute", req("actor", name))
		require.NoError(t, err)
	}
	prepare := func(actor, name string) string {
		raw, e := worker.Call(ctx, "read/prepare", req(actor, name))
		require.NoError(t, e)
		var value struct {
			Receipt string `json:"receipt"`
		}
		require.NoError(t, json.Unmarshal(raw, &value))
		return value.Receipt
	}
	a, b, foreign := prepare("actor", "a"), prepare("actor", "b"), prepare("other", "a")
	request := func(tokens ...string) map[string]any {
		items := make([]map[string]any, 0, len(tokens))
		for _, token := range tokens {
			items = append(items, map[string]any{"receipt": token, "actor": "actor", "permission_mode": "read_only"})
		}
		return map[string]any{"operation_id": "batch-one", "worker_instance": instance.ID, "receipts": items}
	}
	_, err = worker.Call(ctx, "read/commit-batch", request(a, foreign))
	require.Error(t, err)
	_, err = worker.Call(ctx, "execute", req("actor", "b"))
	require.NoError(t, err)
	result, err := worker.Call(ctx, "read/commit-batch", request(a, b))
	require.NoError(t, err)
	require.Contains(t, string(result), `"committed":true`)
	result, err = worker.Call(ctx, "read/commit-batch", request(a, b))
	require.NoError(t, err)
	require.Contains(t, string(result), `"committed":true`)
	probe := func(name string) string {
		id := prepare("actor", name)
		out, e := worker.Call(ctx, "read/commit", map[string]any{"receipt": id, "actor": "actor", "permission_mode": "read_only"})
		require.NoError(t, e)
		return string(out)
	}
	require.Contains(t, probe("a"), `"changed":false`)
	require.Contains(t, probe("b"), `"changed":true`)
}

func TestRemoteObservationWorkerRestartRejectsOldReceipt(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("x\n"), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	init := executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "same-profile"}
	first := executionenv.NewClient([]string{binary, "execution-worker"}, os.Environ())
	hello, err := first.Call(ctx, "initialize", init)
	require.NoError(t, err)
	var a struct {
		Instance string `json:"worker_instance"`
	}
	require.NoError(t, json.Unmarshal(hello, &a))
	require.NotEmpty(t, a.Instance)
	req := executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}}
	raw, err := first.Call(ctx, "read/prepare", req)
	require.NoError(t, err)
	var prepared struct {
		Receipt string `json:"receipt"`
	}
	require.NoError(t, json.Unmarshal(raw, &prepared))
	require.NotEmpty(t, prepared.Receipt)
	require.NoError(t, first.Close())
	second := executionenv.NewClient([]string{binary, "execution-worker"}, os.Environ())
	defer second.Close()
	hello, err = second.Call(ctx, "initialize", init)
	require.NoError(t, err)
	var b struct {
		Instance string `json:"worker_instance"`
	}
	require.NoError(t, json.Unmarshal(hello, &b))
	require.NotEqual(t, a.Instance, b.Instance)
	_, err = second.Call(ctx, "read/commit", map[string]any{"receipt": prepared.Receipt, "actor": "actor", "permission_mode": "read_only"})
	require.Error(t, err)
}
