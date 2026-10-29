//go:build !windows

package appserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

// The admitted thread must recover the exact worker's terminal receipt, not a
// replacement worker, before a model or any tool is allowed to run.
func TestThreadAdmissionRecoversRemoteObservationFromOriginalWorker(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("contents\n"), 0600))
	socketID := "admission-observation-" + rand.Text()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	worker := exec.Command(binary, "execution-worker", "--socket", socketID, "--idle-seconds", "60")
	worker.Env, worker.ExtraFiles = os.Environ(), []*os.File{write}
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

	rt := newTestRuntime(t, &fakeClient{})
	// The shared appserver test runtime deliberately starts without tools.
	// Install its native toolkit so thread construction follows the same
	// native backend path that owns an execution environment in production.
	kit, err := tools.New(rt.RootDir)
	require.NoError(t, err)
	rt.Toolkit, rt.StreamRunner.Tools = kit, kit
	sess, err := rt.Sessions.CreateWithMetadata("observation-process", rt.RootDir)
	require.NoError(t, err)
	ledger, err := toolledger.New(rt.Documents, sess.ID)
	require.NoError(t, err)
	profile := executionenv.Profile{Backend: "command", Command: []string{binary, "execution-connect", "--socket", socketID}, Workspace: root}
	state := t.TempDir()
	first := executionenv.NewEnvironment(profile, "same-env", sess.ID, state)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := executionenv.ToolRequest{Actor: sess.ID, PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}}
	_, err = first.Execute(ctx, request)
	require.NoError(t, err)
	prepared, err := first.PrepareUnobservedRead(ctx, request)
	require.NoError(t, err)
	remoteBatch, err := executionenv.PrepareReadBatch(ctx, []executionenv.PreparedRead{prepared})
	require.NoError(t, err)
	intent := toolledger.ObservationIntent{OperationID: remoteBatch.Identity.OperationID, WorkerInstance: remoteBatch.Identity.WorkerInstance, Actor: sess.ID, EnvironmentIdentity: remoteBatch.Identity.EnvironmentIdentity}
	batch, err := ledger.BeginBatch(ctx, "turn", 0)
	require.NoError(t, err)
	call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, batch))
	require.NoError(t, ledger.Start(ctx, call.ID))
	require.NoError(t, ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("committed result")))
	require.NoError(t, remoteBatch.Commit(ctx))
	require.NoError(t, first.Close())

	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), sess.ID)
	require.NoError(t, err)
	thread, err := srv.ensureThreadRuntime(th)
	require.NoError(t, err)
	require.NotNil(t, thread.Toolkit)
	wrong := executionenv.NewEnvironment(profile, "different-env", sess.ID, state)
	defer wrong.Close()
	thread.Toolkit.SetExecutionEnvironment(wrong)
	th.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
	th.mu.Unlock()
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { th.mu.Lock(); th.releaseThreadExecutionLeaseLocked(); th.mu.Unlock() }()
	_, err = srv.ensureThreadRuntimeAfterAdmission(ctx, th)
	require.Error(t, err)
	require.Error(t, ledger.ValidateContinuation(ctx))
	assertToolInvocationState(t, rt.Documents, call.ID, toolledger.InvocationInterruptedUnknown)
	second := executionenv.NewEnvironment(profile, "same-env", sess.ID, state)
	defer second.Close()
	thread.Toolkit.SetExecutionEnvironment(second)
	_, err = srv.ensureThreadRuntimeAfterAdmission(ctx, th)
	require.NoError(t, err)
	projection, err := ledger.PendingProjection(ctx)
	require.ErrorContains(t, err, "original observation authorization is unavailable")
	require.Empty(t, projection)
	require.NoError(t, ledger.ValidateContinuation(ctx))
}
