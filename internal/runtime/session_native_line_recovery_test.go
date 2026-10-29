package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestSessionNativeLineRecovery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	session := filepath.Join(t.TempDir(), "session")
	kit, err := tools.New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("actor")
	kit.SetAgentIdentity("actor", "root")
	require.NoError(t, kit.SetSessionDir(session))
	defer kit.CloseSessionArtifacts()
	ctx := context.Background()
	content := []byte("first\nsecond\nthird\n")
	sum := sha256.Sum256(content)
	makeBatch := func(name string) *executionenv.PreparedReadBatch {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(session, name), content, 0600))
		raw, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/" + name, "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum)})
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
		require.NoError(t, err)
		call := providers.ToolCall{Name: "read_file", Arguments: string(args)}
		restored, managed, line, err := kit.RestoreRemoteNativeRead(call)
		require.NoError(t, err)
		require.True(t, managed)
		require.True(t, line)
		actor, path := kit.ExecutionActor()
		p, err := kit.PrepareSessionNativeRead(ctx, actor, path, kit.LookupTool("read_file"), call, restored)
		require.NoError(t, err)
		batch, err := tools.PrepareSessionReadBatch(ctx, []tools.PreparedSessionRead{p})
		require.NoError(t, err)
		return batch
	}
	stage := func(id executionenv.ReadBatchIdentity) *toolledger.Ledger {
		t.Helper()
		ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "actor")
		require.NoError(t, err)
		group, err := ledger.BeginBatch(ctx, "turn", 0)
		require.NoError(t, err)
		call, err := ledger.Prepare(ctx, group, providers.ToolCall{ID: "call", Name: "run_code", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
		require.NoError(t, err)
		require.NoError(t, ledger.FinalizeBatch(ctx, group))
		require.NoError(t, ledger.Start(ctx, call.ID))
		intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity}
		require.NoError(t, ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("final result")))
		return ledger
	}
	pending := makeBatch("pending.txt")
	pendingLedger := stage(pending.Identity)
	require.NoError(t, pendingLedger.Reconcile(ctx))
	require.NoError(t, executionenv.RecoverLedgerObservations(ctx, pendingLedger, kit.ObservationRecoverer()))
	_, exists := kit.ActiveFilesContextBlock()
	require.False(t, exists)
	require.NoError(t, pendingLedger.ValidateContinuation(ctx))
	committed := makeBatch("committed.txt")
	committedLedger := stage(committed.Identity)
	require.NoError(t, committed.Commit(ctx))
	require.NoError(t, committedLedger.Reconcile(ctx))
	require.NoError(t, executionenv.RecoverLedgerObservations(ctx, committedLedger, kit.ObservationRecoverer()))
	block, exists := kit.ActiveFilesContextBlock()
	require.True(t, exists)
	require.Contains(t, block.Content, "current=1")
	projection, err := committedLedger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, projection, 1)
	require.Equal(t, "final result", projection[0].Result.TextProjection())
	unknown := makeBatch("unknown.txt")
	unknownLedger := stage(unknown.Identity)
	require.NoError(t, unknown.Commit(ctx))
	require.NoError(t, unknownLedger.Reconcile(ctx))
	other, err := tools.New(t.TempDir())
	require.NoError(t, err)
	other.SetSessionID("actor")
	other.SetAgentIdentity("actor", "root")
	require.NoError(t, other.SetSessionDir(session))
	defer other.CloseSessionArtifacts()
	require.Error(t, executionenv.RecoverLedgerObservations(ctx, unknownLedger, other.ObservationRecoverer()))
	require.Error(t, unknownLedger.ValidateContinuation(ctx))
	require.NoError(t, kit.CloseSessionArtifacts())
	require.Error(t, executionenv.RecoverLedgerObservations(ctx, unknownLedger, kit.ObservationRecoverer()))
	require.Error(t, unknownLedger.ValidateContinuation(ctx))
	require.NoError(t, kit.SetSessionDir(session))
	require.Error(t, executionenv.RecoverLedgerObservations(ctx, unknownLedger, kit.ObservationRecoverer()), "reopening the same path is not the original root binding")
	require.Error(t, unknownLedger.ValidateContinuation(ctx))
}

// Exercise an actual host process exit with a pending or committed receipt.
// A new process has a different in-memory observation owner and must not guess.
func TestSessionNativeLineRecoveryAfterHostExit(t *testing.T) {
	if os.Getenv("WUU_SESSION_RECEIPT_HELPER") != "" {
		return
	}
	for _, state := range []string{"prepared", "committed"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSessionNativeLineReceiptHelper$")
			cmd.Env = append(os.Environ(), "WUU_SESSION_RECEIPT_HELPER="+state, "WUU_SESSION_RECEIPT_DIR="+dir, "WUU_HOME="+filepath.Join(dir, "home"))
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			ctx := context.Background()
			ledger, err := toolledger.New(testSessionDocuments(t, dir), "actor")
			require.NoError(t, err)
			require.NoError(t, ledger.Reconcile(ctx))
			kit, err := tools.New(filepath.Join(dir, "workspace"))
			require.NoError(t, err)
			kit.SetSessionID("actor")
			kit.SetAgentIdentity("actor", "root")
			require.NoError(t, kit.SetSessionDir(filepath.Join(dir, "session")))
			defer kit.CloseSessionArtifacts()
			require.Error(t, executionenv.RecoverLedgerObservations(ctx, ledger, kit.ObservationRecoverer()))
			require.Error(t, ledger.ValidateContinuation(ctx))
			projection, err := ledger.PendingProjection(ctx)
			require.NoError(t, err)
			require.Empty(t, projection)
		})
	}
}

func TestSessionNativeLineReceiptHelper(t *testing.T) {
	state := os.Getenv("WUU_SESSION_RECEIPT_HELPER")
	if state == "" {
		t.Skip("subprocess helper")
	}
	dir := os.Getenv("WUU_SESSION_RECEIPT_DIR")
	workspace, session := filepath.Join(dir, "workspace"), filepath.Join(dir, "session")
	require.NoError(t, os.MkdirAll(workspace, 0700))
	kit, err := tools.New(workspace)
	require.NoError(t, err)
	kit.SetSessionID("actor")
	kit.SetAgentIdentity("actor", "root")
	require.NoError(t, kit.SetSessionDir(session))
	defer kit.CloseSessionArtifacts()
	content := []byte("first\nsecond\n")
	require.NoError(t, os.WriteFile(filepath.Join(session, "file"), content, 0600))
	sum := sha256.Sum256(content)
	raw, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/file", "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", sum)})
	require.NoError(t, err)
	args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
	require.NoError(t, err)
	call := providers.ToolCall{Name: "read_file", Arguments: string(args)}
	restored, _, _, err := kit.RestoreRemoteNativeRead(call)
	require.NoError(t, err)
	actor, path := kit.ExecutionActor()
	p, err := kit.PrepareSessionNativeRead(context.Background(), actor, path, kit.LookupTool("read_file"), call, restored)
	require.NoError(t, err)
	batch, err := tools.PrepareSessionReadBatch(context.Background(), []tools.PreparedSessionRead{p})
	require.NoError(t, err)
	ctx := context.Background()
	ledger, err := toolledger.New(testSessionDocuments(t, dir), "actor")
	require.NoError(t, err)
	group, err := ledger.BeginBatch(ctx, "turn", 0)
	require.NoError(t, err)
	inv, err := ledger.Prepare(ctx, group, providers.ToolCall{ID: "call", Name: "run_code", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, group))
	require.NoError(t, ledger.Start(ctx, inv.ID))
	id := batch.Identity
	require.NoError(t, ledger.StageObservation(ctx, inv.ID, toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity}, toolresult.FromText("secret result")))
	if state == "committed" {
		require.NoError(t, batch.Commit(ctx))
		_, exists := kit.ActiveFilesContextBlock()
		require.True(t, exists)
	}
}
