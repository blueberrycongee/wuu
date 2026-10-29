//go:build !windows

package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func initSessionWorkerRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}, {"commit", "--allow-empty", "-qm", "init"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		output, err := c.CombinedOutput()
		require.NoError(t, err, string(output))
	}
}

func TestWorkerFollowupUsesOriginalSessionObservationStore(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%v", committed), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t.Setenv("WUU_HOME", t.TempDir())
			root := t.TempDir()
			initSessionWorkerRepo(t, root)
			sessionRoot := filepath.Join(root, ".wuu-state", "sessions", "parent")
			docs, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "documents"), "sqlite")
			require.NoError(t, err)
			defer docs.Close()
			ledgers := map[string]*toolledger.Ledger{}
			factory := func(id string) (*toolledger.Ledger, error) {
				if l := ledgers[id]; l != nil {
					return l, nil
				}
				l, e := toolledger.New(docs, id)
				ledgers[id] = l
				return l, e
			}
			var workerKit *tools.Toolkit
			var recovered bool
			config := agentcontrol.Config{AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}), Client: &sessionWorkerClient{}, DefaultModel: "fake", ParentRepo: root, WorktreeRoot: filepath.Join(root, "worktrees"), SessionID: "parent", HistoryDir: filepath.Join(sessionRoot, "workers"), ThreadDir: filepath.Join(sessionRoot, "threads"), HarnessDir: filepath.Join(sessionRoot, "harness"), ToolLedgerFactory: factory}
			config.WorkerFactory = func(_ string, _ agentcontrol.WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
				k, e := tools.New(root)
				if e != nil {
					return nil, e
				}
				k.SetSessionID("parent")
				k.SetAgentIdentity(meta.ID, meta.Path)
				if e = k.SetSessionDir(sessionRoot); e != nil {
					return nil, e
				}
				workerKit = k
				return k, nil
			}
			config.RecoverToolObservations = func(ctx context.Context, ledger *toolledger.Ledger, executor agent.ToolExecutor) error {
				require.Same(t, workerKit, executor)
				recovered = true
				// Production uses the same selected executor under a hook wrapper.
				selected := newNativeToolScopeExecutor(workerKit, nil, nil, "parent", root, false)
				wrapped := hooks.NewHookedExecutor(selected, nil, "parent", root)
				return executionenv.RecoverLedgerObservations(ctx, ledger, workerObservationRecoverer(wrapped))
			}
			control, err := agentcontrol.New(config)
			require.NoError(t, err)
			defer control.Close()
			spawn, err := control.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "session_recovery", Prompt: "finish", Synchronous: true})
			require.NoError(t, err)
			content := []byte("first\nsecond\n")
			require.NoError(t, os.WriteFile(filepath.Join(sessionRoot, "owned.txt"), content, 0600))
			digest := sha256.Sum256(content)
			raw, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/owned.txt", "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", digest)})
			require.NoError(t, err)
			args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
			require.NoError(t, err)
			call := providers.ToolCall{Name: "read_file", Arguments: string(args)}
			restored, _, _, err := workerKit.RestoreRemoteNativeRead(call)
			require.NoError(t, err)
			actor, actorPath := workerKit.ExecutionActor()
			prepared, err := workerKit.PrepareSessionNativeRead(ctx, actor, actorPath, workerKit.LookupTool("read_file"), call, restored)
			require.NoError(t, err)
			receipt, err := tools.PrepareSessionReadBatch(ctx, []tools.PreparedSessionRead{prepared})
			require.NoError(t, err)
			ledger, err := factory(spawn.AgentID)
			require.NoError(t, err)
			batch, err := ledger.BeginBatch(ctx, "synthetic", 0)
			require.NoError(t, err)
			invocation, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "receipt", Name: "read_file", Arguments: string(args)}, toolledger.ReplayAtMostOnce)
			require.NoError(t, err)
			require.NoError(t, ledger.FinalizeBatch(ctx, batch))
			require.NoError(t, ledger.Start(ctx, invocation.ID))
			id := receipt.Identity
			intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity, OwnerKind: id.OwnerKind}
			require.NoError(t, ledger.StageObservation(ctx, invocation.ID, intent, prepared.Result))
			if committed {
				require.NoError(t, receipt.Commit(ctx))
				require.NoError(t, toolctx.RegisterObservationAuthorization(toolctx.SettlementIdentity{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity, OwnerKind: id.OwnerKind}, ledger, prepared.Authorize))
			}
			_, err = control.FollowupTask(ctx, spawn.AgentID, "continue")
			require.NoError(t, err)
			_, err = control.Manager().Wait(ctx, spawn.AgentID)
			require.NoError(t, err)
			require.True(t, recovered)
			require.NoError(t, ledger.ValidateContinuation(ctx))
		})
	}
}

type sessionWorkerClient struct{}

func (*sessionWorkerClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "done"}, nil
}
func (*sessionWorkerClient) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c := make(chan providers.StreamEvent, 2)
	c <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
	c <- providers.StreamEvent{Type: providers.EventDone}
	close(c)
	return c, nil
}

func TestNativeByteProjectionRevalidatesAfterWorkerRestart(t *testing.T) {
	binary := os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if binary == "" {
		t.Skip("run remote observation acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	sessionDir := filepath.Join(t.TempDir(), "session")
	old := []byte("original byte payload\n")
	updated := []byte("new payload for test\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), old, 0600))
	socketID := "byte-restart-" + rand.Text()
	socketSum := sha256.Sum256([]byte(socketID))
	socket := filepath.Join("/tmp", fmt.Sprintf("wuu-execution-%d", os.Getuid()), fmt.Sprintf("worker-%x.sock", socketSum[:12]))
	start := func() *exec.Cmd {
		r, w, err := os.Pipe()
		require.NoError(t, err)
		cmd := exec.Command(binary, "execution-worker", "--socket", socketID, "--idle-seconds", "60")
		cmd.Env, cmd.ExtraFiles = os.Environ(), []*os.File{w}
		require.NoError(t, cmd.Start())
		_ = w.Close()
		require.NoError(t, r.SetReadDeadline(time.Now().Add(10*time.Second)))
		var ready [1]byte
		_, err = io.ReadFull(r, ready[:])
		require.NoError(t, err)
		_ = r.Close()
		return cmd
	}
	worker := start()
	defer func() { _ = worker.Process.Kill(); _ = worker.Wait(); _ = os.Remove(socket) }()
	env := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-connect", "--socket", socketID}, Workspace: root}, "same-env", "session", t.TempDir())
	defer env.Close()
	kit, err := tools.New(t.TempDir())
	require.NoError(t, err)
	kit.SetSessionID("session")
	kit.SetAgentIdentity("actor", "root/actor")
	kit.SetPermissionMode("standard")
	kit.SetExecutionEnvironment(env)
	require.NoError(t, kit.SetSessionDir(sessionDir))
	defer kit.CloseSessionArtifacts()
	makeCall := func(content []byte) providers.ToolCall {
		digest := sha256.Sum256(content)
		raw, err := json.Marshal(map[string]any{"path": "file", "byte_offset": 0, "byte_end_offset": len(content), "limit": len(content), "expected_sha256": fmt.Sprintf("%x", digest)})
		require.NoError(t, err)
		args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
		require.NoError(t, err)
		return providers.ToolCall{Name: "read_file", Arguments: string(args)}
	}
	call := makeCall(old)
	restored, managed, line, err := kit.RestoreRemoteNativeRead(call)
	require.NoError(t, err)
	require.False(t, managed)
	require.False(t, line)
	prepared, err := kit.PrepareNativeByteRead(ctx, env, "actor", "root/actor", kit.LookupTool("read_file"), call, restored, false)
	require.NoError(t, err)
	receipt, err := tools.PrepareSessionReadBatch(ctx, []tools.PreparedSessionRead{prepared})
	require.NoError(t, err)
	ledger, err := toolledger.New(testSessionDocuments(t, t.TempDir()), "actor")
	require.NoError(t, err)
	batch, err := ledger.BeginBatch(ctx, "turn", 0)
	require.NoError(t, err)
	inv, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "byte", Name: "read_file", Arguments: call.Arguments}, toolledger.ReplayAtMostOnce)
	require.NoError(t, err)
	require.NoError(t, ledger.FinalizeBatch(ctx, batch))
	require.NoError(t, ledger.Start(ctx, inv.ID))
	id := receipt.Identity
	intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity, OwnerKind: id.OwnerKind}
	require.NoError(t, ledger.StageObservation(ctx, inv.ID, intent, prepared.Result))
	require.NoError(t, receipt.Commit(ctx))
	require.NoError(t, ledger.ResolveObservation(ctx, inv.ID, intent, true))
	require.NoError(t, toolctx.RegisterObservationAuthorization(toolctx.SettlementIdentity{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: id.Actor, EnvironmentIdentity: id.EnvironmentIdentity, OwnerKind: id.OwnerKind}, ledger, prepared.Authorize))
	pending, err := ledger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.NoError(t, worker.Process.Kill())
	_ = worker.Wait()
	_ = os.Remove(socket)
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), updated, 0600))
	worker = start()
	var connectErr error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		attempt, stop := context.WithTimeout(ctx, 500*time.Millisecond)
		_, connectErr = env.Execute(attempt, executionenv.ToolRequest{Actor: "actor", PermissionMode: "standard", Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"file"}`}})
		stop()
		if connectErr == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.NoError(t, connectErr)
	pending, err = ledger.PendingProjection(ctx)
	require.Error(t, err)
	require.Empty(t, pending)
	newCall := makeCall(updated)
	newRestored, _, _, err := kit.RestoreRemoteNativeRead(newCall)
	require.NoError(t, err)
	fresh, err := kit.PrepareNativeByteRead(ctx, env, "actor", "root/actor", kit.LookupTool("read_file"), newCall, newRestored, false)
	require.NoError(t, err)
	require.Contains(t, fresh.Result.TextProjection(), "new payload")
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), old, 0600))
	pending, err = ledger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	policy := &projectionPolicy{}
	kit.SetAuthorizer(policy)
	policy.denied.Store(true)
	pending, err = ledger.PendingProjection(ctx)
	require.Error(t, err)
	require.Empty(t, pending)
	policy.denied.Store(false)
	kit.SetAgentIdentity("other-actor", "root/other-actor")
	pending, err = ledger.PendingProjection(ctx)
	require.Error(t, err)
	require.Empty(t, pending)
	kit.SetAgentIdentity("actor", "root/actor")
	other := executionenv.NewEnvironment(executionenv.Profile{Backend: "command", Command: []string{binary, "execution-connect", "--socket", socketID}, Workspace: root}, "different-env", "session", t.TempDir())
	defer other.Close()
	kit.SetExecutionEnvironment(other)
	pending, err = ledger.PendingProjection(ctx)
	require.Error(t, err)
	require.Empty(t, pending)
	kit.SetExecutionEnvironment(env)
	pending, err = ledger.PendingProjection(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
}
