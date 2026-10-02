package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestServerThreadDeleteRemovesSessionAndArtifacts(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	stateDir, err := srv.workspaceStateDir()
	if err != nil {
		t.Fatalf("workspaceStateDir: %v", err)
	}
	artifactDir := statepath.SessionArtifactDir(stateDir, threadID)
	if err := os.MkdirAll(filepath.Join(artifactDir, "workers"), 0o755); err != nil {
		t.Fatalf("seed artifact dir: %v", err)
	}

	deletePayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadDelete,
		"params": ThreadDeleteParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), deletePayload); err != nil {
		t.Fatalf("thread/delete: %v", err)
	}
	deleteResult := remarshal[ThreadDeleteResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if deleteResult.ThreadID != threadID {
		t.Fatalf("unexpected delete result: %+v", deleteResult)
	}

	if _, ok, err := session.Find(rt.SessionDir, threadID); err != nil {
		t.Fatalf("session.Find after delete: %v", err)
	} else if ok {
		t.Fatalf("session %q should be gone after thread/delete", threadID)
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("artifact dir should be removed, stat err = %v", err)
	}
	if srv.thread(threadID) != nil {
		t.Fatalf("thread %q should be dropped from the in-memory registry", threadID)
	}

	if err := srv.handleLine(context.Background(), []byte(`{"id":"3","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}
	listResult := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"])
	if len(listResult.Threads) != 0 {
		t.Fatalf("deleted thread should not be listed, got %+v", listResult.Threads)
	}
}

func TestServerThreadDeleteRejectsRunningThread(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	th := srv.thread(threadID)
	if th == nil {
		t.Fatalf("thread %q not registered", threadID)
	}
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()

	deletePayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadDelete,
		"params": ThreadDeleteParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), deletePayload); err != nil {
		t.Fatalf("thread/delete: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "2")
	if resp["error"] == nil {
		t.Fatalf("deleting a running thread must fail, got %+v", resp)
	}
	if _, ok, err := session.Find(rt.SessionDir, threadID); err != nil || !ok {
		t.Fatalf("running thread must survive a rejected delete: ok=%v err=%v", ok, err)
	}
}

func TestServerThreadDeleteRejectsRunningSideThreadAndDoesNotRecreateIt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	blocking := newBlockingStreamClient("side reply")
	rt.StreamRunner.Client = blocking
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"side-delete-start","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "side-delete-start")["result"]).Thread.ID
	if _, err := srv.sendSideThreadMessage(threadID, "status?"); err != nil {
		t.Fatalf("side send: %v", err)
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("side provider request did not start")
	}

	dispatchPayload(t, srv, "side-delete-busy", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
	busy := responseByID(t, parseOutput(t, out.String()), "side-delete-busy")
	if busy["error"] == nil || !strings.Contains(fmt.Sprint(busy["error"]), "side thread is running") {
		t.Fatalf("delete did not report the running side thread: %+v", busy)
	}
	if _, ok, err := session.Find(rt.SessionDir, threadID); err != nil || !ok {
		t.Fatalf("rejected delete removed main thread: ok=%t err=%v", ok, err)
	}

	close(blocking.release)
	waitForSideThreadStatus(t, srv.sideThreadStore, threadID, sidethread.StatusCompleted)
	dispatchPayload(t, srv, "side-delete-idle", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
	idle := responseByID(t, parseOutput(t, out.String()), "side-delete-idle")
	if idle["error"] != nil {
		t.Fatalf("delete after side completion failed: %+v", idle["error"])
	}
	if exists, err := srv.sideThreadStore.Exists(threadID); err != nil || exists {
		t.Fatalf("deleted side thread was recreated: exists=%t err=%v", exists, err)
	}
}

func TestServerThreadDeletePreservesMainThreadWhenSideCleanupFails(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"side-cleanup-start","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "side-cleanup-start")["result"]).Thread.ID

	invalidStoreDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(invalidStoreDir, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("seed invalid side store: %v", err)
	}
	srv.sideThreadStore = sidethread.NewStore(invalidStoreDir)
	dispatchPayload(t, srv, "side-cleanup-delete", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
	resp := responseByID(t, parseOutput(t, out.String()), "side-cleanup-delete")
	if resp["error"] == nil || !strings.Contains(fmt.Sprint(resp["error"]), "delete side thread") {
		t.Fatalf("side cleanup failure was not returned: %+v", resp)
	}
	if _, ok, err := session.Find(rt.SessionDir, threadID); err != nil || !ok {
		t.Fatalf("failed side cleanup removed the main thread: ok=%t err=%v", ok, err)
	}
}

func TestServerThreadDeleteRestoresSideThreadWhenMainDeleteFails(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"main-delete-failure-start","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "main-delete-failure-start")["result"]).Thread.ID
	if _, err := srv.sideThreadStore.BeginTurn(threadID, "keep me", "side-user", "side-assistant"); err != nil {
		t.Fatalf("seed side thread: %v", err)
	}
	if _, _, err := srv.sideThreadStore.FinishTurn(threadID, "side-assistant", "preserved reply", sidethread.StatusCompleted, ""); err != nil {
		t.Fatalf("finish side thread: %v", err)
	}
	srv.deleteSessionForTest = func(string) (session.Session, error) {
		return session.Session{}, errors.New("injected main delete failure")
	}

	dispatchPayload(t, srv, "main-delete-failure", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
	resp := responseByID(t, parseOutput(t, out.String()), "main-delete-failure")
	if resp["error"] == nil || !strings.Contains(fmt.Sprint(resp["error"]), "injected main delete failure") {
		t.Fatalf("main delete failure was not returned: %+v", resp)
	}
	if _, ok, err := session.Find(rt.SessionDir, threadID); err != nil || !ok {
		t.Fatalf("failed main delete removed thread: ok=%t err=%v", ok, err)
	}
	side, err := srv.sideThreadStore.Load(threadID)
	if err != nil || len(side.Messages) != 2 || side.Messages[1].Text != "preserved reply" {
		t.Fatalf("side thread was not restored after main delete failure: side=%+v err=%v", side, err)
	}
	if _, err := os.Stat(filepath.Join(srv.sideThreadStore.Dir(), ".deleting", threadID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("side delete stage survived rollback: %v", err)
	}
}

func TestServerThreadDeleteRejectsActiveBackgroundAgent(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	workerClient := newBlockingStreamClient("done")
	control, err := agentcontrol.New(agentcontrol.Config{
		Client:       workerClient,
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    threadID,
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("agentcontrol.New: %v", err)
	}
	t.Cleanup(func() {
		control.StopAll()
		control.Close()
	})

	th := srv.thread(threadID)
	if th == nil {
		t.Fatalf("thread %q not registered", threadID)
	}
	th.mu.Lock()
	th.execRuntime = &runtime.ThreadRuntime{AgentControl: control}
	th.mu.Unlock()
	if _, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "background_delete_guard",
		Description: "keep running while delete is attempted",
		Prompt:      "wait",
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(time.Second):
		t.Fatal("background agent did not start")
	}

	deletePayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadDelete,
		"params": ThreadDeleteParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), deletePayload); err != nil {
		t.Fatalf("thread/delete: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "2")
	if resp["error"] == nil {
		t.Fatalf("deleting a thread with an active background agent must fail, got %+v", resp)
	}
	if _, ok, err := session.Find(rt.SessionDir, threadID); err != nil || !ok {
		t.Fatalf("thread with an active agent must survive a rejected delete: ok=%v err=%v", ok, err)
	}
}

func TestServerThreadDeleteRejectsDurableNestedAgentFromAnotherServer(t *testing.T) {
	ownerRuntime := newTestRuntime(t, &fakeClient{})
	ownerRuntime.StateDir = filepath.Join(ownerRuntime.RootDir, ".wuu-state")
	rootID := "cross-server-child-delete"
	if _, err := session.CreateWithMetadata(ownerRuntime.SessionDir, rootID, ownerRuntime.RootDir); err != nil {
		t.Fatalf("create root session: %v", err)
	}

	owner := New(ownerRuntime, &lockedBuffer{})
	t.Cleanup(owner.Close)
	rootThread, err := owner.ensureThreadLoaded(rootID)
	if err != nil {
		t.Fatalf("load owner thread: %v", err)
	}
	artifactDir := statepath.SessionArtifactDir(ownerRuntime.StateDir, rootID)
	workerClient := newBlockingStreamClient("nested done")
	control, err := agentcontrol.New(agentcontrol.Config{
		Client:       workerClient,
		DefaultModel: "fake-model",
		ParentRepo:   ownerRuntime.RootDir,
		WorktreeRoot: filepath.Join(ownerRuntime.RootDir, ".wuu", "worktrees"),
		SessionID:    rootID,
		HistoryDir:   filepath.Join(artifactDir, "workers"),
		ThreadDir:    filepath.Join(artifactDir, "threads"),
		HarnessDir:   filepath.Join(artifactDir, "harness"),
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create agent control: %v", err)
	}
	rootThread.mu.Lock()
	rootThread.execRuntime = &runtime.ThreadRuntime{AgentControl: control}
	rootThread.mu.Unlock()

	parent, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:           agentcontrol.DefaultSubagentType,
		TaskName:       "completed_parent",
		Description:    "finish before the nested child starts",
		Prompt:         "finish",
		ClientOverride: providers.AdaptStreamClient(&fakeClient{response: providers.ChatResponse{Content: "parent done"}}),
	})
	if err != nil {
		t.Fatalf("spawn completed parent: %v", err)
	}
	waitForNoDurableActiveAgents(t, owner, rootID)

	nested, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "blocking_nested_child",
		Description: "remain active while another app-server tries to delete the root",
		Prompt:      "wait",
		ParentID:    parent.AgentID,
		ParentPath:  parent.AgentPath,
	})
	if err != nil {
		t.Fatalf("spawn nested child: %v", err)
	}
	select {
	case <-workerClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("nested child did not start")
	}
	if nested.AgentPath != parent.AgentPath+"/blocking_nested_child" {
		t.Fatalf("nested path = %q, want child of %q", nested.AgentPath, parent.AgentPath)
	}

	contenderRuntime := newTestRuntime(t, &fakeClient{})
	contenderRuntime.RootDir = ownerRuntime.RootDir
	contenderRuntime.SessionDir = ownerRuntime.SessionDir
	contenderRuntime.StateDir = ownerRuntime.StateDir
	contenderOut := &lockedBuffer{}
	contender := New(contenderRuntime, contenderOut)
	t.Cleanup(contender.Close)

	dispatchPayload(t, contender, "delete-active", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, contenderOut.String()), "delete-active"); resp["error"] == nil || !strings.Contains(fmt.Sprint(resp["error"]), "active agents") {
		t.Fatalf("cross-server delete must reject the durable active nested child: %+v", resp)
	}
	if _, ok, err := session.Find(ownerRuntime.SessionDir, rootID); err != nil || !ok {
		t.Fatalf("rejected delete removed the root session: ok=%t err=%v", ok, err)
	}

	close(workerClient.release)
	waitForNoDurableActiveAgents(t, contender, rootID)
	dispatchPayload(t, contender, "delete-terminal", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, contenderOut.String()), "delete-terminal"); resp["error"] != nil {
		t.Fatalf("terminal durable child state should allow delete: %+v", resp["error"])
	}
	if _, ok, err := session.Find(ownerRuntime.SessionDir, rootID); err != nil {
		t.Fatalf("find deleted root session: %v", err)
	} else if ok {
		t.Fatal("root session survived delete after all children became terminal")
	}
	if _, err := os.Stat(artifactDir); !os.IsNotExist(err) {
		t.Fatalf("root artifacts survived delete: %v", err)
	}
}

func TestServerThreadDeleteRejectsTerminalWorkerUntilExecutionLeaseReleases(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
	const rootID = "terminal-worker-delete-observer"
	if _, err := session.CreateWithMetadata(rt.SessionDir, rootID, rt.RootDir); err != nil {
		t.Fatalf("create root session: %v", err)
	}
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	control, err := agentcontrol.New(agentcontrol.Config{
		Client:       providers.AdaptStreamClient(&fakeClient{response: providers.ChatResponse{Content: "done"}}),
		DefaultModel: "fake-model",
		ParentRepo:   rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"),
		SessionID:    rootID,
		HistoryDir:   filepath.Join(artifactDir, "workers"),
		ThreadDir:    filepath.Join(artifactDir, "threads"),
		HarnessDir:   filepath.Join(artifactDir, "harness"),
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create agent control: %v", err)
	}
	var enteredOnce sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseWorker()
		control.StopAll()
		control.Close()
	})
	control.SetWorkerExecutionReleaseHookForTest(func(string) {
		enteredOnce.Do(func() { close(entered) })
		<-release
	})

	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type: agentcontrol.DefaultSubagentType, TaskName: "terminal_delete_observer", Prompt: "finish",
	})
	if err != nil {
		t.Fatalf("spawn worker: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach the terminal lease observer")
	}
	if got := control.Manager().CountRunning(); got != 0 {
		t.Fatalf("manager running count = %d, want terminal observer window", got)
	}
	meta, ok := control.Threads().Resolve(spawned.AgentID)
	if !ok || meta.Status != agentthread.StatusCompleted {
		t.Fatalf("durable thread status = %+v, found=%t; want completed", meta, ok)
	}
	task := harnessTaskByIDForDelete(t, control.HarnessStore(), spawned.AgentID)
	if task.Status != harness.TaskStatusCompleted {
		t.Fatalf("durable harness status = %q, want completed", task.Status)
	}
	if !control.HasOwnedWorkerExecutions() {
		t.Fatal("terminal observer no longer owns the worker execution lease")
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "delete-observer-busy", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, out.String()), "delete-observer-busy"); resp["error"] == nil || !strings.Contains(fmt.Sprint(resp["error"]), "active agents") {
		t.Fatalf("delete must reject the terminal worker lease observer: %+v", resp)
	}
	if _, ok, err := session.Find(rt.SessionDir, rootID); err != nil || !ok {
		t.Fatalf("rejected delete removed the root session: ok=%t err=%v", ok, err)
	}

	releaseWorker()
	waitForOwnedWorkerExecutions(t, control, 0)
	dispatchPayload(t, srv, "delete-observer-released", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, out.String()), "delete-observer-released"); resp["error"] != nil {
		t.Fatalf("delete after worker lease release failed: %+v", resp["error"])
	}
	if _, ok, err := session.Find(rt.SessionDir, rootID); err != nil {
		t.Fatalf("find deleted root session: %v", err)
	} else if ok {
		t.Fatal("root session survived delete after worker lease release")
	}
}

func harnessTaskByIDForDelete(t *testing.T, store *harness.Store, id string) harness.Task {
	t.Helper()
	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("list harness tasks: %v", err)
	}
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("harness task %q not found: %+v", id, tasks)
	return harness.Task{}
}

func TestThreadHasDurableActiveAgentsIgnoresCrashStaleProjectedStates(t *testing.T) {
	tests := []struct {
		name   string
		status harness.TaskStatus
	}{
		{name: "pending", status: harness.TaskStatusPending},
		{name: "queued", status: harness.TaskStatusQueued},
		{name: "running", status: harness.TaskStatusRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
			rootID := "durable-active-" + tt.name
			artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
			if err := harness.NewStore(filepath.Join(artifactDir, "harness")).UpsertTask(harness.Task{
				ID:        "nested-" + tt.name,
				SessionID: rootID,
				ParentID:  "direct-child",
				Path:      "/root/direct/nested",
				Status:    tt.status,
			}); err != nil {
				t.Fatalf("seed harness task: %v", err)
			}
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			active, err := srv.threadHasDurableActiveAgents(rootID)
			if err != nil {
				t.Fatalf("inspect durable agents: %v", err)
			}
			if active {
				t.Fatalf("crash-stale %s task blocked deletion without queue, terminal intent, or execution lease", tt.status)
			}
		})
	}

	t.Run("thread index", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
		rootID := "durable-active-thread-index"
		artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
		if err := agentthread.NewStore(filepath.Join(artifactDir, "threads")).UpsertThread(agentthread.Metadata{
			ID:        "nested-thread-index",
			SessionID: rootID,
			ParentID:  "direct-child",
			Path:      "/root/direct/nested",
			Status:    agentthread.StatusPending,
			Source: agentthread.Source{
				Kind:       agentthread.SourceThreadSpawn,
				ParentPath: "/root/direct",
			},
		}); err != nil {
			t.Fatalf("seed agent thread: %v", err)
		}
		srv := New(rt, &lockedBuffer{})
		t.Cleanup(srv.Close)
		active, err := srv.threadHasDurableActiveAgents(rootID)
		if err != nil {
			t.Fatalf("inspect durable agents: %v", err)
		}
		if active {
			t.Fatal("crash-stale pending thread metadata blocked deletion without execution ownership")
		}
	})

	t.Run("spawn queue", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		rt.StateDir = filepath.Join(rt.RootDir, ".wuu-state")
		rootID := "durable-active-spawn-queue"
		artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
		if err := harness.NewStore(filepath.Join(artifactDir, "harness")).UpsertQueueItem(harness.QueueItem{
			ID:      "queued-child",
			TaskID:  "queued-child",
			Kind:    "agent_spawn",
			Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("seed spawn queue: %v", err)
		}
		srv := New(rt, &lockedBuffer{})
		t.Cleanup(srv.Close)
		active, err := srv.threadHasDurableActiveAgents(rootID)
		if err != nil {
			t.Fatalf("inspect durable agents: %v", err)
		}
		if !active {
			t.Fatal("durable spawn queue was not recognized as active")
		}
	})
}

func waitForNoDurableActiveAgents(t *testing.T, srv *Server, threadID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		active, err := srv.threadHasDurableActiveAgents(threadID)
		if err != nil {
			t.Fatalf("inspect durable agents: %v", err)
		}
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable agents for %q did not become terminal", threadID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerThreadDeleteStopsRuntimeSubscription(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	th := srv.thread(threadID)
	if th == nil {
		t.Fatalf("thread %q not registered", threadID)
	}
	sub := &threadRuntimeSubscription{done: make(chan struct{})}
	th.mu.Lock()
	th.execRuntime = &runtime.ThreadRuntime{}
	th.runtimeSubscription = sub
	th.mu.Unlock()

	deletePayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadDelete,
		"params": ThreadDeleteParams{ThreadID: threadID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), deletePayload); err != nil {
		t.Fatalf("thread/delete: %v", err)
	}
	if resp := responseByID(t, parseOutput(t, out.String()), "2"); resp["error"] != nil {
		t.Fatalf("thread/delete failed: %+v", resp["error"])
	}
	select {
	case <-sub.done:
	default:
		t.Fatal("thread/delete left its runtime subscription running")
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.execRuntime != nil || th.runtimeSubscription != nil {
		t.Fatalf("thread/delete retained runtime ownership: runtime=%p subscription=%p", th.execRuntime, th.runtimeSubscription)
	}
}

func TestServerThreadDeleteCleansForkWorktree(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	initAppserverGitRepo(t, rt.RootDir)
	out := &lockedBuffer{}
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	sourceID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	forkPayload, err := json.Marshal(map[string]any{
		"id":     "2",
		"method": MethodThreadFork,
		"params": ThreadForkParams{ThreadID: sourceID, Mode: "worktree"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), forkPayload); err != nil {
		t.Fatalf("thread/fork: %v", err)
	}
	forkResult := remarshal[ThreadForkResult](t, responseByID(t, parseOutput(t, out.String()), "2")["result"])
	if forkResult.Worktree == nil || forkResult.Worktree.Path == "" {
		t.Fatalf("fork should bind a worktree, got %+v", forkResult)
	}
	worktreePath := forkResult.Worktree.Path
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("fork worktree should exist on disk: %v", err)
	}

	deletePayload, err := json.Marshal(map[string]any{
		"id":     "3",
		"method": MethodThreadDelete,
		"params": ThreadDeleteParams{ThreadID: forkResult.Thread.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), deletePayload); err != nil {
		t.Fatalf("thread/delete: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "3")
	if resp["error"] != nil {
		t.Fatalf("thread/delete failed: %+v", resp["error"])
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("fork worktree should be removed with the thread, stat err = %v", err)
	}
	if _, ok, err := session.Find(rt.SessionDir, forkResult.Thread.ID); err != nil {
		t.Fatalf("session.Find after delete: %v", err)
	} else if ok {
		t.Fatalf("fork session should be gone after thread/delete")
	}
}

func TestServerThreadDeleteOnlyIfArchivedPreservesRestoredData(t *testing.T) {
	for _, test := range []struct {
		name           string
		archived       bool
		restored       bool
		onlyIfArchived bool
		wantError      bool
	}{
		{name: "archived", archived: true, onlyIfArchived: true},
		{name: "active", onlyIfArchived: true, wantError: true},
		{name: "restored", archived: true, restored: true, onlyIfArchived: true, wantError: true},
		{name: "ordinary active delete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			const threadID = "guarded-delete"
			if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
				t.Fatal(err)
			}
			if err := session.AppendHistoryRecord(rt.SessionDir, threadID, session.HistoryRecord{Role: "user", Content: "keep my history"}); err != nil {
				t.Fatal(err)
			}
			if test.archived {
				if _, err := session.UpdateArchived(rt.SessionDir, threadID, true); err != nil {
					t.Fatal(err)
				}
			}
			if test.restored {
				if _, err := session.UpdateArchived(rt.SessionDir, threadID, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := srv.sideThreadStore.BeginTurn(threadID, "side question", "side-user", "side-assistant"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := srv.sideThreadStore.FinishTurn(threadID, "side-assistant", "keep my reply", sidethread.StatusCompleted, ""); err != nil {
				t.Fatal(err)
			}
			dispatchPayload(t, srv, "delete", MethodThreadDelete, map[string]any{
				"thread_id": threadID, "only_if_archived": test.onlyIfArchived,
			})
			response := responseByID(t, parseOutput(t, out.String()), "delete")
			if gotError := response["error"] != nil; gotError != test.wantError {
				t.Fatalf("delete error = %t, want %t: %+v", gotError, test.wantError, response)
			}
			if _, found, err := session.Find(rt.SessionDir, threadID); err != nil || found != test.wantError {
				t.Fatalf("session retained = %t, want %t: %v", found, test.wantError, err)
			}
			if exists, err := srv.sideThreadStore.Exists(threadID); err != nil || exists != test.wantError {
				t.Fatalf("side thread retained = %t, want %t: %v", exists, test.wantError, err)
			}
			if !test.wantError {
				return
			}
			if !strings.Contains(fmt.Sprint(response["error"]), "not archived") {
				t.Fatalf("guard rejection did not explain archive eligibility: %+v", response)
			}
			history, err := session.LoadHistoryRecords(rt.SessionDir, threadID, true)
			if err != nil || len(history) != 1 || history[0].Content != "keep my history" {
				t.Fatalf("rejected deletion changed history: %+v, %v", history, err)
			}
			side, err := srv.sideThreadStore.Load(threadID)
			if err != nil || len(side.Messages) != 2 || side.Messages[1].Text != "keep my reply" {
				t.Fatalf("rejected deletion changed side history: %+v, %v", side, err)
			}
			if _, err := os.Stat(filepath.Join(srv.sideThreadStore.Dir(), ".deleting", threadID+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected deletion did not roll back side staging: %v", err)
			}
		})
	}
}

func TestServerThreadDeleteArchivedUsesGlobalStoreAndForeignArtifacts(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprintf("queued_%t", queued), func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.WuuHome = t.TempDir()
			rt.StateDir, _ = statepath.WorkspaceDirByID(rt.WuuHome, "current-workspace")
			rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			const threadID = "foreign-archive-delete"
			foreignStateDir, err := statepath.WorkspaceDirByID(rt.WuuHome, "foreign-workspace")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, filepath.Join(t.TempDir(), "foreign-project")); err != nil {
				t.Fatal(err)
			}
			if _, err := session.UpdateArchived(rt.SessionDir, threadID, true); err != nil {
				t.Fatal(err)
			}
			artifactDir := statepath.SessionArtifactDir(foreignStateDir, threadID)
			if err := os.MkdirAll(filepath.Join(artifactDir, "workers"), 0o755); err != nil {
				t.Fatal(err)
			}
			if queued {
				store := harness.NewStore(filepath.Join(artifactDir, "harness"))
				if err := store.UpsertQueueItem(harness.QueueItem{ID: "queued-worker", TaskID: "worker", Kind: "agent_spawn", Payload: json.RawMessage(`{}`)}); err != nil {
					t.Fatal(err)
				}
			}
			otherStore := t.TempDir()
			if _, err := session.CreateWithMetadata(otherStore, threadID, rt.RootDir); err != nil {
				t.Fatal(err)
			}
			dispatchPayload(t, srv, "list", MethodThreadListArchived, ThreadListParams{CWD: rt.RootDir})
			listed := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "list")["result"])
			if len(listed.Threads) != 1 || listed.Threads[0].ID != threadID {
				t.Fatalf("foreign archived session missing from global list: %+v", listed)
			}
			dispatchPayload(t, srv, "delete", MethodThreadDelete, map[string]any{"thread_id": threadID, "only_if_archived": true})
			response := responseByID(t, parseOutput(t, out.String()), "delete")
			if gotError := response["error"] != nil; gotError != queued {
				t.Fatalf("delete error = %t, want %t: %+v", gotError, queued, response)
			}
			if queued && !strings.Contains(fmt.Sprint(response["error"]), "active agents") {
				t.Fatalf("foreign queued worker rejection lost its reason: %+v", response)
			}
			if _, found, err := session.Find(rt.SessionDir, threadID); err != nil || found != queued {
				t.Fatalf("foreign archived session retained = %t, want %t: %v", found, queued, err)
			}
			_, artifactErr := os.Stat(artifactDir)
			if queued && artifactErr != nil {
				t.Fatalf("rejected deletion removed foreign artifacts: %v", artifactErr)
			}
			if !queued && !errors.Is(artifactErr, os.ErrNotExist) {
				t.Fatalf("foreign artifacts survived deletion: %v", artifactErr)
			}
			if _, found, err := session.Find(otherStore, threadID); err != nil || !found {
				t.Fatalf("deletion escaped the configured session store: found=%t err=%v", found, err)
			}
		})
	}
}

func TestServerThreadDeleteArchivedRechecksRestoreAfterSideStaging(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	const threadID = "restore-after-side-stage"
	if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendHistoryRecord(rt.SessionDir, threadID, session.HistoryRecord{Role: "user", Content: "survive restoration"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateArchived(rt.SessionDir, threadID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.sideThreadStore.BeginTurn(threadID, "side question", "side-user", "side-assistant"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.sideThreadStore.FinishTurn(threadID, "side-assistant", "side reply", sidethread.StatusCompleted, ""); err != nil {
		t.Fatal(err)
	}
	srv.deleteSessionForTest = func(id string) (session.Session, error) {
		if _, err := os.Stat(filepath.Join(srv.sideThreadStore.Dir(), ".deleting", id+".json")); err != nil {
			t.Fatalf("side deletion was not staged before restore: %v", err)
		}
		// Another archive caller may restore after any handler-level precheck.
		if _, err := session.UpdateArchived(rt.SessionDir, id, false); err != nil {
			t.Fatal(err)
		}
		srv.deleteSessionForTest = nil
		return srv.deleteThreadSession(id, true)
	}
	dispatchPayload(t, srv, "delete", MethodThreadDelete, map[string]any{"thread_id": threadID, "only_if_archived": true})
	response := responseByID(t, parseOutput(t, out.String()), "delete")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "not archived") {
		t.Fatalf("restore before the delete transaction must reject deletion: %+v", response)
	}
	metadata, found, err := session.Find(rt.SessionDir, threadID)
	if err != nil || !found || metadata.ArchivedAt != nil {
		t.Fatalf("restored session was changed: found=%t metadata=%+v err=%v", found, metadata, err)
	}
	history, err := session.LoadHistoryRecords(rt.SessionDir, threadID, true)
	if err != nil || len(history) != 1 || history[0].Content != "survive restoration" {
		t.Fatalf("restored history changed: %+v, %v", history, err)
	}
	side, err := srv.sideThreadStore.Load(threadID)
	if err != nil || len(side.Messages) != 2 || side.Messages[1].Text != "side reply" {
		t.Fatalf("side staging was not rolled back: %+v, %v", side, err)
	}
}

func TestServerThreadDeleteKeepsUnrelatedGlobalArtifacts(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.StateDir = t.TempDir()
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	const threadID = "same-id-different-store"
	if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	foreignStateDir, err := statepath.WorkspaceDirByID(rt.WuuHome, "unrelated-global-workspace")
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := statepath.SessionArtifactDir(foreignStateDir, threadID)
	store := harness.NewStore(filepath.Join(artifactDir, "harness"))
	if err := store.UpsertQueueItem(harness.QueueItem{ID: "unrelated-worker", TaskID: "worker", Kind: "agent_spawn", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	dispatchPayload(t, srv, "delete", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
	response := responseByID(t, parseOutput(t, out.String()), "delete")
	if response["error"] != nil {
		t.Fatalf("unrelated global artifact blocked custom-store deletion: %+v", response)
	}
	if _, err := os.Stat(artifactDir); err != nil {
		t.Fatalf("custom-store deletion removed unrelated global artifact: %v", err)
	}
}

func TestServerThreadDeleteRejectsSymlinkedForeignArtifactPaths(t *testing.T) {
	for _, root := range []string{"sessions", "worktrees"} {
		for _, linkParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_parent_%t", root, linkParent), func(t *testing.T) {
				rt := newTestRuntime(t, &fakeClient{})
				rt.WuuHome = t.TempDir()
				rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
				rt.StateDir, _ = statepath.WorkspaceDirByID(rt.WuuHome, "current")
				out := &lockedBuffer{}
				srv := New(rt, out)
				t.Cleanup(srv.Close)
				const threadID = "foreign-symlink"
				if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
					t.Fatal(err)
				}
				foreignStateDir, err := statepath.WorkspaceDirByID(rt.WuuHome, "foreign")
				if err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				marker := filepath.Join(outside, "keep.txt")
				if err := os.WriteFile(marker, []byte("unrelated user data"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(foreignStateDir, root, threadID)
				if linkParent {
					link = filepath.Dir(link)
				}
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				dispatchPayload(t, srv, "delete", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
				response := responseByID(t, parseOutput(t, out.String()), "delete")
				if response["error"] == nil {
					t.Fatalf("delete traversed unsafe foreign artifact path: %+v", response)
				}
				if _, found, err := session.Find(rt.SessionDir, threadID); err != nil || !found {
					t.Fatalf("unsafe artifact path did not preserve session: found=%t err=%v", found, err)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("unsafe artifact path changed external data: %v", err)
				}
			})
		}
	}
}

func TestServerThreadDeleteRejectsSymlinkedForeignWorkspace(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
	rt.StateDir, _ = statepath.WorkspaceDirByID(rt.WuuHome, "current")
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	const threadID = "foreign-workspace-symlink"
	if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	artifactDir := statepath.SessionArtifactDir(outside, threadID)
	store := harness.NewStore(filepath.Join(artifactDir, "harness"))
	if err := store.UpsertQueueItem(harness.QueueItem{ID: "queued-worker", TaskID: "worker", Kind: "agent_spawn", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rt.WuuHome, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rt.WuuHome, "workspaces", "linked-workspace")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dispatchPayload(t, srv, "delete", MethodThreadDelete, ThreadDeleteParams{ThreadID: threadID})
	response := responseByID(t, parseOutput(t, out.String()), "delete")
	if response["error"] == nil {
		t.Fatalf("unsafe workspace path bypassed active worker protection: %+v", response)
	}
	if _, found, err := session.Find(rt.SessionDir, threadID); err != nil || !found {
		t.Fatalf("unsafe workspace path did not preserve session: found=%t err=%v", found, err)
	}
	if queue, err := store.ListQueueItems(); err != nil || len(queue) != 1 || queue[0].ID != "queued-worker" {
		t.Fatalf("unsafe workspace path changed queued work: %+v, %v", queue, err)
	}
}
