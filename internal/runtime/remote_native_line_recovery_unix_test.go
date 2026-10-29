//go:build !windows

package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func TestRemoteNativeLineRecoversSameWorkerAfterReconnect(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("x\n"), 0600))
	socketID := "observation-test-" + rand.Text()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	worker := exec.Command(binary, "execution-worker", "--socket", socketID, "--idle-seconds", "60")
	worker.Env = os.Environ()
	worker.ExtraFiles = []*os.File{write}
	require.NoError(t, worker.Start())
	_ = write.Close()
	defer func() {
		_ = worker.Process.Kill()
		_ = worker.Wait()
		sum := sha256.Sum256([]byte(socketID))
		_ = os.Remove(filepath.Join("/tmp", fmt.Sprintf("wuu-execution-%d", os.Getuid()), fmt.Sprintf("worker-%x.sock", sum[:12])))
	}()
	require.NoError(t, read.SetReadDeadline(time.Now().Add(10*time.Second)))
	var ready [1]byte
	_, err = io.ReadFull(read, ready[:])
	require.NoError(t, err)
	_ = read.Close()
	profile := executionenv.Profile{Backend: "command", Command: []string{binary, "execution-connect", "--socket", socketID}, Workspace: root}
	state := t.TempDir()
	first := executionenv.NewEnvironment(profile, "same-env", "thread", state)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	digest := sha256.Sum256([]byte("x\n"))
	req := executionenv.ToolRequest{Actor: "actor", PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: fmt.Sprintf(`{"path":"a","offset":1,"limit":1,"expected_sha256":%q}`, fmt.Sprintf("%x", digest))}}
	_, err = first.Execute(ctx, req)
	require.NoError(t, err)
	p, err := first.PrepareNativeRead(ctx, req)
	require.NoError(t, err)
	batch, err := executionenv.PrepareReadBatch(ctx, []executionenv.PreparedRead{p})
	require.NoError(t, err)
	identity := batch.Identity
	// A document provider may see the non-bearer identity, but a separate
	// connector without the host's transport credential cannot query it.
	attacker := executionenv.NewClient([]string{binary, "execution-connect", "--socket", socketID}, os.Environ())
	defer attacker.Close()
	_, attackErr := attacker.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: root, Session: "thread", Identity: "same-env"})
	require.Error(t, attackErr)
	encoded, err := json.Marshal(identity)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "receipt")
	stage := func(owner string, id executionenv.ReadBatchIdentity) *toolledger.Ledger {
		ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), owner)
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
	wrongIdentity := identity
	wrongIdentity.WorkerInstance = "different-worker"
	wrongLedger := stage("actor", wrongIdentity)
	require.NoError(t, wrongLedger.Reconcile(ctx))
	require.Error(t, executionenv.RecoverLedgerObservations(ctx, wrongLedger, first))
	require.Error(t, wrongLedger.ValidateContinuation(ctx))
	wrongPending, err := wrongLedger.PendingObservations(ctx)
	require.NoError(t, err)
	require.Len(t, wrongPending, 1)
	ledger := stage("actor", identity)
	require.NoError(t, first.Close())
	second := executionenv.NewEnvironment(profile, "same-env", "thread", state)
	require.NoError(t, ledger.Reconcile(ctx))
	require.Error(t, ledger.ValidateContinuation(ctx))
	// A prepared operation is fenced rather than committed by recovery.
	require.NoError(t, executionenv.RecoverLedgerObservations(ctx, ledger, second))
	require.NoError(t, ledger.ValidateContinuation(ctx))
	projected, err := ledger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, projected, 1)
	require.True(t, projected[0].Result.IsError)
	probe, err := second.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := probe.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "aborted native prepare retains the previous observation")
	// A commit whose response was lost is resolved using the original worker.
	p2, err := second.PrepareNativeRead(ctx, req)
	require.NoError(t, err)
	batch2, err := executionenv.PrepareReadBatch(ctx, []executionenv.PreparedRead{p2})
	require.NoError(t, err)
	ledger2 := stage("actor", batch2.Identity)
	require.NoError(t, batch2.Commit(ctx))
	require.NoError(t, second.Close())
	third := executionenv.NewEnvironment(profile, "same-env", "thread", state)
	defer third.Close()
	require.NoError(t, ledger2.Reconcile(ctx))
	require.NoError(t, executionenv.RecoverLedgerObservations(ctx, ledger2, third))
	require.NoError(t, ledger2.ValidateContinuation(ctx))
	projected, err = ledger2.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, projected, 1)
	require.Equal(t, "final result", projected[0].Result.TextProjection())

	probe, err = third.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err = probe.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "recovered committed native read publishes its observation")

	// A worker reboot can reuse the same socket, session and profile, but its
	// new instance must not settle the original instance's pending operation.
	p3, err := third.PrepareNativeRead(ctx, req)
	require.NoError(t, err)
	batch3, err := executionenv.PrepareReadBatch(ctx, []executionenv.PreparedRead{p3})
	require.NoError(t, err)
	ledger3 := stage("actor", batch3.Identity)
	require.NoError(t, third.Close())
	require.NoError(t, worker.Process.Kill())
	_ = worker.Wait()
	sum := sha256.Sum256([]byte(socketID))
	socketPath := filepath.Join("/tmp", fmt.Sprintf("wuu-execution-%d", os.Getuid()), fmt.Sprintf("worker-%x.sock", sum[:12]))
	require.NoError(t, os.Remove(socketPath))
	read2, write2, err := os.Pipe()
	require.NoError(t, err)
	replacement := exec.Command(binary, "execution-worker", "--socket", socketID, "--idle-seconds", "60")
	replacement.Env, replacement.ExtraFiles = os.Environ(), []*os.File{write2}
	require.NoError(t, replacement.Start())
	_ = write2.Close()
	defer func() { _ = replacement.Process.Kill(); _ = replacement.Wait() }()
	require.NoError(t, read2.SetReadDeadline(time.Now().Add(10*time.Second)))
	_, err = io.ReadFull(read2, ready[:])
	require.NoError(t, err)
	_ = read2.Close()
	fourth := executionenv.NewEnvironment(profile, "same-env", "thread", state)
	defer fourth.Close()
	require.NoError(t, ledger3.Reconcile(ctx))
	require.Error(t, executionenv.RecoverLedgerObservations(ctx, ledger3, fourth))
	require.Error(t, ledger3.ValidateContinuation(ctx))
	unresolved, err := ledger3.PendingObservations(ctx)
	require.NoError(t, err)
	require.Len(t, unresolved, 1)
}
