//go:build !windows

package agentcontrol

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

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func TestWorkerFollowupRecoversRemoteObservationUnderExecutionLease(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	initRepo(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("contents\n"), 0600))
	socketID := "worker-observation-" + rand.Text()
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	docs, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "documents"), "sqlite")
	require.NoError(t, err)
	defer docs.Close()
	artifact := filepath.Join(root, ".wuu-state", "sessions", "parent")
	factory := func(id string) (*toolledger.Ledger, error) { return toolledger.New(docs, id) }
	config := Config{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), Client: &fakeClient{resp: providers.ChatResponse{Content: "first"}}, DefaultModel: "fake", ParentRepo: root, WorktreeRoot: filepath.Join(root, "worktrees"), SessionID: "parent", HistoryDir: filepath.Join(artifact, "workers"), ThreadDir: filepath.Join(artifact, "threads"), HarnessDir: filepath.Join(artifact, "harness"), WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }, ToolLedgerFactory: factory}
	first, err := New(config)
	require.NoError(t, err)
	spawn, err := first.Spawn(ctx, SpawnRequest{Type: DefaultSubagentType, TaskName: "recovery", Prompt: "finish", Synchronous: true})
	require.NoError(t, err)
	require.Equal(t, "completed", spawn.Status)
	first.Close()

	profile := executionenv.Profile{Backend: "command", Command: []string{binary, "execution-connect", "--socket", socketID}, Workspace: root}
	state := t.TempDir()
	environment := executionenv.NewEnvironment(profile, "same-env", "parent", state)
	request := executionenv.ToolRequest{Actor: spawn.AgentID, PermissionMode: "read_only", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"a"}`}}
	_, err = environment.Execute(ctx, request)
	require.NoError(t, err)
	prepared, err := environment.PrepareUnobservedRead(ctx, request)
	require.NoError(t, err)
	remoteBatch, err := executionenv.PrepareReadBatch(ctx, []executionenv.PreparedRead{prepared})
	require.NoError(t, err)
	ledger, err := factory(spawn.AgentID)
	require.NoError(t, err)
	batch, err := ledger.BeginBatch(ctx, "turn", 0)
	require.NoError(t, err)
	call, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "read_file", Arguments: `{}`}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, batch))
	require.NoError(t, ledger.Start(ctx, call.ID))
	intent := toolledger.ObservationIntent{OperationID: remoteBatch.Identity.OperationID, WorkerInstance: remoteBatch.Identity.WorkerInstance, Actor: spawn.AgentID, EnvironmentIdentity: remoteBatch.Identity.EnvironmentIdentity}
	require.NoError(t, ledger.StageObservation(ctx, call.ID, intent, toolresult.FromText("worker result")))
	require.NoError(t, remoteBatch.Commit(ctx))
	require.NoError(t, environment.Close())

	current := executionenv.NewEnvironment(profile, "different-env", "parent", state)
	defer func() { _ = current.Close() }()
	config.Client = &recordingClient{resp: providers.ChatResponse{Content: "resumed"}}
	config.RecoverToolObservations = func(ctx context.Context, ledger *toolledger.Ledger, _ agent.ToolExecutor) error {
		return executionenv.RecoverLedgerObservations(ctx, ledger, current)
	}
	second, err := New(config)
	require.NoError(t, err)
	defer second.Close()
	_, err = second.FollowupTask(ctx, spawn.AgentID, "resume")
	require.Error(t, err)
	require.Error(t, ledger.ValidateContinuation(ctx))
	require.NoError(t, current.Close())
	current = executionenv.NewEnvironment(profile, "same-env", "parent", state)
	_, err = second.FollowupTask(ctx, spawn.AgentID, "resume")
	require.NoError(t, err)
	_, err = second.Manager().Wait(ctx, spawn.AgentID)
	require.NoError(t, err)
	require.NoError(t, ledger.ValidateContinuation(ctx))
}
