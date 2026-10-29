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

func TestReadObservationBatchAtomicAndSingleActor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("same\n"), 0600))
	}
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	_, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	native := func(actor, name string) []byte {
		raw, _ := json.Marshal(executionenv.ToolRequest{Actor: actor, PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"` + name + `"}`}})
		return raw
	}
	prepare := func(actor, name string) string {
		raw, err := server.Handle(context.Background(), "read/prepare", native(actor, name))
		require.NoError(t, err)
		var v struct {
			Receipt string `json:"receipt"`
		}
		require.NoError(t, json.Unmarshal(raw, &v))
		return v.Receipt
	}
	for _, name := range []string{"a", "b"} {
		_, err = server.Handle(context.Background(), "execute", native("actor", name))
		require.NoError(t, err)
	}
	a, b, foreign := prepare("actor", "a"), prepare("actor", "b"), prepare("other", "a")
	batch := func(ids ...string) []byte {
		v := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			v = append(v, map[string]any{"receipt": id, "actor": "actor", "permission_mode": "read_only"})
		}
		raw, _ := json.Marshal(map[string]any{"operation_id": "batch-one", "worker_instance": server.instanceID, "receipts": v})
		return raw
	}
	_, err = server.Handle(context.Background(), "read/commit-batch", batch(a, foreign))
	require.Error(t, err)
	status, _ := json.Marshal(map[string]any{"receipt": a, "actor": "actor", "permission_mode": "read_only"})
	raw, err := server.Handle(context.Background(), "read/status", status)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"committed":false`)
	// A new identical read during the parent call must survive the batch.
	_, err = server.Handle(context.Background(), "execute", native("actor", "b"))
	require.NoError(t, err)
	raw, err = server.Handle(context.Background(), "read/commit-batch", batch(a, b))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"committed":true`)
	replay, err := server.Handle(context.Background(), "read/commit-batch", batch(a, b))
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(replay))
	changedBatch := batch(a, b)
	var changedMap map[string]any
	require.NoError(t, json.Unmarshal(changedBatch, &changedMap))
	changedMap["operation_id"] = "different-batch"
	changedBatch, _ = json.Marshal(changedMap)
	_, err = server.Handle(context.Background(), "read/commit-batch", changedBatch)
	require.Error(t, err, "a different batch must not inherit another batch's committed status")
	probe := func(name string) string {
		id := prepare("actor", name)
		raw, _ := json.Marshal(map[string]any{"receipt": id, "actor": "actor", "permission_mode": "read_only"})
		out, err := server.Handle(context.Background(), "read/commit", raw)
		require.NoError(t, err)
		return string(out)
	}
	require.Contains(t, probe("a"), `"changed":false`)
	require.Contains(t, probe("b"), `"changed":true`)
}

func TestReadObservationBatchRejectsIndividuallyCommittedReceipts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("x\n"), 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	_, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	req, _ := json.Marshal(executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}})
	receipts := make([]map[string]any, 0, 2)
	for i := 0; i < 2; i++ {
		raw, e := server.Handle(context.Background(), "read/prepare", req)
		require.NoError(t, e)
		var response struct {
			Receipt string `json:"receipt"`
		}
		require.NoError(t, json.Unmarshal(raw, &response))
		item := map[string]any{"receipt": response.Receipt, "actor": "actor", "permission_mode": "read_only"}
		single, _ := json.Marshal(item)
		_, e = server.Handle(context.Background(), "read/commit", single)
		require.NoError(t, e)
		receipts = append(receipts, item)
	}
	batch, _ := json.Marshal(map[string]any{"operation_id": "not-an-atomic-batch", "worker_instance": server.instanceID, "receipts": receipts})
	_, err = server.Handle(context.Background(), "read/commit-batch", batch)
	require.ErrorContains(t, err, "do not belong to this batch")
}

func TestReadObservationBatchPreparedStatusAndAbort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("x\n"), 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	_, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	req, _ := json.Marshal(executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}})
	_, err = server.Handle(context.Background(), "execute", req)
	require.NoError(t, err)
	raw, err := server.Handle(context.Background(), "read/prepare", req)
	require.NoError(t, err)
	var v struct {
		Receipt string `json:"receipt"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	prepare, _ := json.Marshal(map[string]any{"operation_id": "op", "worker_instance": server.instanceID, "actor": "actor", "receipts": []any{map[string]any{"receipt": v.Receipt, "actor": "actor", "permission_mode": "read_only"}}})
	raw, err = server.Handle(context.Background(), "read/batch-prepare", prepare)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"prepared":true`)
	status, _ := json.Marshal(map[string]any{"operation_id": "op", "worker_instance": server.instanceID, "actor": "actor"})
	raw, err = server.Handle(context.Background(), "read/batch-status", status)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"state":"prepared"`)
	require.NotContains(t, string(raw), v.Receipt)
	// A reserved member cannot be committed independently.
	member, _ := json.Marshal(map[string]any{"receipt": v.Receipt, "actor": "actor", "permission_mode": "read_only"})
	_, err = server.Handle(context.Background(), "read/commit", member)
	require.Error(t, err)
	_, err = server.Handle(context.Background(), "read/batch-abort", status)
	require.NoError(t, err)
	a, err := server.actor("actor", "read_only", false, false)
	require.NoError(t, err)
	_, ok := a.kit.ActiveFilesContextBlock()
	require.True(t, ok)
}

func TestReadObservationBatchPreparedCommitCanBeQueriedWithoutReceipts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("x\n"), 0600))
	server := New()
	defer server.Close()
	init, _ := json.Marshal(executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "env"})
	_, err := server.Handle(context.Background(), "initialize", init)
	require.NoError(t, err)
	req, _ := json.Marshal(executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}})
	_, err = server.Handle(context.Background(), "execute", req)
	require.NoError(t, err)
	raw, err := server.Handle(context.Background(), "read/prepare", req)
	require.NoError(t, err)
	var receipt struct {
		ID string `json:"receipt"`
	}
	require.NoError(t, json.Unmarshal(raw, &receipt))
	prepare, _ := json.Marshal(map[string]any{"operation_id": "recovery-op", "worker_instance": server.instanceID, "actor": "actor", "receipts": []any{map[string]any{"receipt": receipt.ID, "actor": "actor", "permission_mode": "read_only"}}})
	_, err = server.Handle(context.Background(), "read/batch-prepare", prepare)
	require.NoError(t, err)
	// The persisted coordinator identity contains no bearer receipt.
	identity, _ := json.Marshal(map[string]any{"operation_id": "recovery-op", "worker_instance": server.instanceID, "actor": "actor"})
	require.NotContains(t, string(identity), receipt.ID)
	raw, err = server.Handle(context.Background(), "read/batch-commit", identity)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"committed":true`)
	raw, err = server.Handle(context.Background(), "read/batch-status", identity)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"state":"committed"`)
	require.NotContains(t, string(raw), receipt.ID)
	raw, err = server.Handle(context.Background(), "read/batch-commit", identity)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"committed":true`)
	wrong, _ := json.Marshal(map[string]any{"operation_id": "recovery-op", "worker_instance": "different", "actor": "actor"})
	_, err = server.Handle(context.Background(), "read/batch-status", wrong)
	require.Error(t, err)
}
