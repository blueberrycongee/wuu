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
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestServerThreadDeleteRemovesSessionAndArtifacts(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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

	if _, ok, err := rt.Sessions.Find(threadID); err != nil {
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
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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
	if _, ok, err := rt.Sessions.Find(threadID); err != nil || !ok {
		t.Fatalf("running thread must survive a rejected delete: ok=%v err=%v", ok, err)
	}
}

func TestServerThreadDeleteRejectsActiveBackgroundAgent(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	srv := New(rt, out)

	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID

	workerClient := newBlockingStreamClient("done")
	control, err := agentcontrol.New(agentcontrol.Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

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
	if _, ok, err := rt.Sessions.Find(threadID); err != nil || !ok {
		t.Fatalf("thread with an active agent must survive a rejected delete: ok=%v err=%v", ok, err)
	}
}

func TestServerThreadDeleteRejectsDurableNestedAgentFromAnotherServer(t *testing.T) {
	ownerRuntime := newTestRuntime(t, &fakeClient{})
	ownerRuntime.StateDir = filepath.Join(ownerRuntime.RootDir, ".wuu-state")
	rootID := "cross-server-child-delete"
	if _, err := ownerRuntime.Sessions.CreateWithMetadata(rootID, ownerRuntime.RootDir); err != nil {
		t.Fatalf("create root session: %v", err)
	}

	bindThreadDeleteArtifacts(t, ownerRuntime, &threadDeleteArtifactProvider{})
	owner := New(ownerRuntime, &lockedBuffer{})
	t.Cleanup(owner.Close)
	rootThread, err := owner.ensureThreadLoaded(testRuntimeState(t, owner.rt), rootID)
	if err != nil {
		t.Fatalf("load owner thread: %v", err)
	}
	artifactDir := statepath.SessionArtifactDir(ownerRuntime.StateDir, rootID)
	workerClient := newBlockingStreamClient("nested done")
	control, err := agentcontrol.New(agentcontrol.Config{
		Documents:         ownerRuntime.Documents,
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

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
	contenderRuntime.Documents, contenderRuntime.Sessions = ownerRuntime.Documents, ownerRuntime.Sessions
	contenderRuntime.StateDir = ownerRuntime.StateDir
	contenderOut := &lockedBuffer{}
	bindThreadDeleteArtifacts(t, contenderRuntime, &threadDeleteArtifactProvider{})
	contender := New(contenderRuntime, contenderOut)
	t.Cleanup(contender.Close)

	dispatchPayload(t, contender, "delete-active", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, contenderOut.String()), "delete-active"); resp["error"] == nil || !strings.Contains(fmt.Sprint(resp["error"]), "active agents") {
		t.Fatalf("cross-server delete must reject the durable active nested child: %+v", resp)
	}
	if _, ok, err := ownerRuntime.Sessions.Find(rootID); err != nil || !ok {
		t.Fatalf("rejected delete removed the root session: ok=%t err=%v", ok, err)
	}

	close(workerClient.release)
	waitForNoDurableActiveAgents(t, contender, rootID)
	dispatchPayload(t, contender, "delete-terminal", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, contenderOut.String()), "delete-terminal"); resp["error"] != nil {
		t.Fatalf("terminal durable child state should allow delete: %+v", resp["error"])
	}
	if _, ok, err := ownerRuntime.Sessions.Find(rootID); err != nil {
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
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatalf("create root session: %v", err)
	}
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	control, err := agentcontrol.New(agentcontrol.Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(agent.DefaultDriver{}),

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
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "delete-observer-busy", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, out.String()), "delete-observer-busy"); resp["error"] == nil || !strings.Contains(fmt.Sprint(resp["error"]), "active agents") {
		t.Fatalf("delete must reject the terminal worker lease observer: %+v", resp)
	}
	if _, ok, err := rt.Sessions.Find(rootID); err != nil || !ok {
		t.Fatalf("rejected delete removed the root session: ok=%t err=%v", ok, err)
	}

	releaseWorker()
	waitForOwnedWorkerExecutions(t, control, 0)
	dispatchPayload(t, srv, "delete-observer-released", MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})
	if resp := responseByID(t, parseOutput(t, out.String()), "delete-observer-released"); resp["error"] != nil {
		t.Fatalf("delete after worker lease release failed: %+v", resp["error"])
	}
	if _, ok, err := rt.Sessions.Find(rootID); err != nil {
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
			bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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
		bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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
		bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
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
	if _, ok, err := rt.Sessions.Find(forkResult.Thread.ID); err != nil {
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
			bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			const threadID = "guarded-delete"
			if _, err := rt.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
				t.Fatal(err)
			}
			if err := rt.Sessions.AppendHistoryRecord(threadID, session.HistoryRecord{Role: "user", Content: "keep my history"}); err != nil {
				t.Fatal(err)
			}
			if test.archived {
				if _, err := rt.Sessions.UpdateArchived(threadID, true); err != nil {
					t.Fatal(err)
				}
			}
			if test.restored {
				if _, err := rt.Sessions.UpdateArchived(threadID, false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rt.Sessions.CreateInitialized(session.Session{ID: "attached", ParentID: threadID, Placement: session.PlacementSide, CWD: rt.RootDir},
				[]session.HistoryRecord{{Role: "user", Content: "side question"}, {Role: "assistant", Content: "keep my reply"}}); err != nil {
				t.Fatal(err)
			}
			dispatchPayload(t, srv, "delete", MethodThreadDelete, map[string]any{
				"thread_id": threadID, "only_if_archived": test.onlyIfArchived,
			})
			response := responseByID(t, parseOutput(t, out.String()), "delete")
			if gotError := response["error"] != nil; gotError != test.wantError {
				t.Fatalf("delete error = %t, want %t: %+v", gotError, test.wantError, response)
			}
			if _, found, err := rt.Sessions.Find(threadID); err != nil || found != test.wantError {
				t.Fatalf("session retained = %t, want %t: %v", found, test.wantError, err)
			}
			if _, exists, err := rt.Sessions.Find("attached"); err != nil || exists != test.wantError {
				t.Fatalf("side thread retained = %t, want %t: %v", exists, test.wantError, err)
			}
			if !test.wantError {
				return
			}
			if !strings.Contains(fmt.Sprint(response["error"]), "not archived") {
				t.Fatalf("guard rejection did not explain archive eligibility: %+v", response)
			}
			history, err := rt.Sessions.LoadHistoryRecords(threadID, true)
			if err != nil || len(history) != 1 || history[0].Content != "keep my history" {
				t.Fatalf("rejected deletion changed history: %+v, %v", history, err)
			}
			side, err := rt.Sessions.LoadHistoryRecords("attached", true)
			if err != nil || len(side) != 2 || side[1].Content != "keep my reply" {
				t.Fatalf("rejected deletion changed side history: %+v, %v", side, err)
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
			bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			const threadID = "foreign-archive-delete"
			foreignStateDir, err := statepath.WorkspaceDirByID(rt.WuuHome, "foreign-workspace")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.Sessions.CreateWithMetadata(threadID, filepath.Join(t.TempDir(), "foreign-project")); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.Sessions.UpdateArchived(threadID, true); err != nil {
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
			otherStoreStore := newAppserverTestStore(t, otherStore)
			if _, err := otherStoreStore.CreateWithMetadata(threadID, rt.RootDir); err != nil {
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
			if _, found, err := rt.Sessions.Find(threadID); err != nil || found != queued {
				t.Fatalf("foreign archived session retained = %t, want %t: %v", found, queued, err)
			}
			_, artifactErr := os.Stat(artifactDir)
			if queued && artifactErr != nil {
				t.Fatalf("rejected deletion removed foreign artifacts: %v", artifactErr)
			}
			if !queued && !errors.Is(artifactErr, os.ErrNotExist) {
				t.Fatalf("foreign artifacts survived deletion: %v", artifactErr)
			}
			if _, found, err := otherStoreStore.Find(threadID); err != nil || !found {
				t.Fatalf("deletion escaped the configured session store: found=%t err=%v", found, err)
			}
		})
	}
}

func TestServerThreadDeleteKeepsUnrelatedGlobalArtifacts(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.StateDir = t.TempDir()
	out := &lockedBuffer{}
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	const threadID = "same-id-different-store"
	if _, err := rt.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
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
				bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
				srv := New(rt, out)
				t.Cleanup(srv.Close)
				const threadID = "foreign-symlink"
				if _, err := rt.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
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
				if _, found, err := rt.Sessions.Find(threadID); err != nil || !found {
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
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	const threadID = "foreign-workspace-symlink"
	if _, err := rt.Sessions.CreateWithMetadata(threadID, rt.RootDir); err != nil {
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
	if _, found, err := rt.Sessions.Find(threadID); err != nil || !found {
		t.Fatalf("unsafe workspace path did not preserve session: found=%t err=%v", found, err)
	}
	if queue, err := store.ListQueueItems(); err != nil || len(queue) != 1 || queue[0].ID != "queued-worker" {
		t.Fatalf("unsafe workspace path changed queued work: %+v, %v", queue, err)
	}
}

func TestServerThreadDeleteRetriesSelectedArtifactsAfterDocumentCommit(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	provider := &threadDeleteArtifactProvider{err: errors.New("selected cleanup response lost")}
	bindThreadDeleteArtifacts(t, rt, provider)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	const id = "partial-artifact-delete"
	if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	stateDir, err := srv.workspaceStateDir()
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(statepath.SessionArtifactDir(stateDir, id), "artifacts", "foreign-provider-copy")
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("must remain with its owner"), 0600); err != nil {
		t.Fatal(err)
	}
	request := func(requestID string) map[string]any {
		t.Helper()
		data, err := json.Marshal(map[string]any{"id": requestID, "method": MethodThreadDelete, "params": ThreadDeleteParams{ThreadID: id}})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.handleLine(context.Background(), data); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), requestID)
	}
	failed := request("first")
	if failed["error"] == nil || !strings.Contains(fmt.Sprint(failed["error"]), "session deleted") || provider.calls != 1 {
		t.Fatalf("partial deletion was not reported: %+v, calls=%d", failed, provider.calls)
	}
	if _, found, err := rt.Sessions.Find(id); err != nil || found {
		t.Fatalf("committed document deletion rolled back: found=%v err=%v", found, err)
	}
	provider.err = nil
	if response := request("retry"); response["error"] != nil || response["result"] == nil || provider.calls != 2 {
		t.Fatalf("missing document prevented selected cleanup retry: %+v calls=%d", response, provider.calls)
	}
	if data, err := os.ReadFile(local); err != nil || string(data) != "must remain with its owner" {
		t.Fatalf("selected cleanup deleted unselected native data: %q %v", data, err)
	}
}

type threadDeleteArtifactProvider struct {
	calls int
	err   error
}

func (*threadDeleteArtifactProvider) ID() string { return "test.delete-artifacts" }
func (p *threadDeleteArtifactProvider) Status() pluginhost.Status {
	return pluginhost.Status{ID: p.ID(), State: pluginhost.StateActive}
}
func (*threadDeleteArtifactProvider) Close(context.Context) error                       { return nil }
func (*threadDeleteArtifactProvider) RequiredServices() []pluginhost.ServiceRequirement { return nil }
func (*threadDeleteArtifactProvider) ProvidedServices() []pluginhost.ServiceDescriptor {
	return []pluginhost.ServiceDescriptor{{Name: runtime.ArtifactServiceName, Version: "1.1.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "delete-thread", InputSchema: "state.artifacts.delete-thread.input.v1", OutputSchema: "state.artifacts.delete-thread.output.v1"}}}}
}
func (p *threadDeleteArtifactProvider) InvokeService(_ context.Context, input pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if input.Method != "delete-thread" {
		return nil, errors.New("unexpected artifact method")
	}
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	var request struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal(input.Params, &request); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"thread_id": request.ThreadID, "deleted": true})
}

type threadDeleteServiceConsumer struct{}

func (*threadDeleteServiceConsumer) ID() string { return runtime.StateServicesConsumer }
func (c *threadDeleteServiceConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*threadDeleteServiceConsumer) Close(context.Context) error                      { return nil }
func (*threadDeleteServiceConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*threadDeleteServiceConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: runtime.ArtifactServiceName, MajorVersion: 1, Required: true}, {Name: config.ServiceName, MajorVersion: 1, Required: true}}
}
func bindThreadDeleteArtifacts(t *testing.T, rt *runtime.Session, artifacts *threadDeleteArtifactProvider) {
	t.Helper()
	cfg := config.Default()
	cfg.DefaultProvider = rt.ProviderName
	cfg.Providers = map[string]config.ProviderConfig{rt.ProviderName: {Type: "openai-compatible", Model: rt.Model, BaseURL: "https://example.test/v1"}}
	source := config.NewMemoryService("test.delete-config", cfg)
	consumer := &threadDeleteServiceConsumer{}
	attachTestModelGeneration(t, rt, source, artifacts, consumer)
}

func TestDurableChildMigrationIncludesEveryArtifactRoot(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	rootID := "multi-root-child-migration"
	empty, populated := t.TempDir(), t.TempDir()
	harnessDir := filepath.Join(statepath.SessionArtifactDir(populated, rootID), "harness")
	if err := harness.NewStore(harnessDir).UpsertQueueItem(harness.QueueItem{ID: "queued", Kind: "agent_spawn"}); err != nil {
		t.Fatal(err)
	}
	active, err := srv.threadHasDurableActiveAgents(rootID, empty, populated)
	if err != nil || !active {
		t.Fatalf("later legacy root omitted: active=%v err=%v", active, err)
	}
}
