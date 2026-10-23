package agentcontrol

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestWorkerExecutionLeaseProtectsLiveRunAcrossAgentControls(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	artifactDir := filepath.Join(dir, ".wuu-state", "sessions", "shared-worker")
	historyDir := filepath.Join(artifactDir, "workers")
	threadDir := filepath.Join(artifactDir, "threads")
	harnessDir := filepath.Join(artifactDir, "harness")

	ownerClient := newBlockingClient()
	owner := newSharedAgentControl(t, dir, historyDir, threadDir, harnessDir, ownerClient)
	t.Cleanup(func() {
		owner.StopAll()
		owner.Close()
	})
	spawned, err := owner.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "single_owner",
		Prompt:   "remain live",
	})
	if err != nil {
		t.Fatalf("spawn owner worker: %v", err)
	}
	ownerClient.waitStarted(t)

	contenderClient := &recordingClient{resp: providers.ChatResponse{Content: "resumed once"}}
	contender := newSharedAgentControl(t, dir, historyDir, threadDir, harnessDir, contenderClient)
	t.Cleanup(func() {
		contender.StopAll()
		contender.Close()
	})

	task := harnessTaskByID(t, contender.HarnessStore(), spawned.AgentID)
	if task.Status != harness.TaskStatusRunning {
		t.Fatalf("contender reconciled the live owner's task to %q", task.Status)
	}
	if contender.Manager().Get(spawned.AgentID) != nil {
		t.Fatal("contender created a duplicate live executor")
	}
	if err := contender.SendMessage(context.Background(), spawned.AgentID, "must not resume concurrently"); !errors.Is(err, errWorkerExecutionBusy) {
		t.Fatalf("cross-process follow-up error = %v, want worker ownership busy", err)
	}
	if request := contenderClient.LastRequest(); len(request.Messages) > 0 {
		t.Fatal("contender invoked the provider while the owner was live")
	}

	if !owner.Stop(spawned.AgentID) {
		t.Fatal("owner did not stop its live worker")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ownerTerminal, err := owner.Manager().Wait(waitCtx, spawned.AgentID)
	if err != nil {
		t.Fatalf("wait for owner terminal state: %v", err)
	}
	if ownerTerminal.Status != subagent.StatusCancelled {
		t.Fatalf("owner terminal status = %q, want cancelled", ownerTerminal.Status)
	}

	resumed, err := contender.FollowupTask(context.Background(), spawned.AgentID, "resume after ownership release")
	if err != nil {
		t.Fatalf("resume after owner release: %v", err)
	}
	if resumed.Status != subagent.StatusRunning {
		t.Fatalf("resumed status = %q, want running", resumed.Status)
	}
	resumedTerminal, err := contender.Manager().Wait(waitCtx, spawned.AgentID)
	if err != nil {
		t.Fatalf("wait for resumed worker: %v", err)
	}
	if resumedTerminal.Status != subagent.StatusCompleted {
		t.Fatalf("resumed terminal status = %q, want completed", resumedTerminal.Status)
	}
	request := contenderClient.LastRequest()
	if len(request.Messages) == 0 {
		t.Fatal("resumed worker never invoked the provider")
	}
	var sawFollowup bool
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "resume after ownership release") {
			sawFollowup = true
			break
		}
	}
	if !sawFollowup {
		t.Fatalf("resumed request omitted the follow-up: %+v", request.Messages)
	}
}

func TestQueuedSpawnRetriesAfterForeignWorkerLeaseReleases(t *testing.T) {
	dir := t.TempDir()
	owner := pinAgentControl(t, dir, &pinRecordingClient{id: "owner"})
	contenderClient := &pinRecordingClient{id: "contender"}
	contender := pinAgentControl(t, dir, contenderClient)
	const workerID = "worker_queued_foreign_lease"

	acquired, err := owner.acquireWorkerExecution(workerID)
	if err != nil {
		t.Fatalf("acquire owner execution lease: %v", err)
	}
	if !acquired {
		t.Fatal("owner did not acquire the worker execution lease")
	}
	t.Cleanup(func() { owner.releaseWorkerExecution(workerID) })

	writeQueuedSpawnPayload(t, contender.HarnessStore().Dir(), workerID, "", "")
	if err := contender.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restore queued spawn: %v", err)
	}
	contender.StartQueuedWork()
	contender.maybeStartQueued(context.Background())
	if _, ok := contenderClient.LastRequest(); ok {
		t.Fatal("contender launched while the foreign worker lease was owned")
	}
	contender.queueMu.Lock()
	queued := len(contender.queued)
	contender.queueMu.Unlock()
	if queued != 1 {
		t.Fatalf("local queued spawn count = %d, want retry retained", queued)
	}
	items, err := contender.HarnessStore().ListQueueItems()
	if err != nil {
		t.Fatalf("list durable queue: %v", err)
	}
	if len(items) != 1 || items[0].TaskID != workerID {
		t.Fatalf("durable queue was lost while lease was busy: %+v", items)
	}

	owner.releaseWorkerExecution(workerID)
	if _, ok := contenderClient.WaitRequest(2 * time.Second); !ok {
		t.Fatal("queued spawn did not retry after the foreign lease released")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		items, err = contender.HarnessStore().ListQueueItems()
		if err != nil {
			t.Fatalf("list durable queue after retry: %v", err)
		}
		if len(items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claimed queued spawn remained durable: %+v", items)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newSharedAgentControl(t *testing.T, parentRepo, historyDir, threadDir, harnessDir string, client providers.StreamClient) *AgentControl {
	t.Helper()
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    parentRepo,
		WorktreeRoot:  filepath.Join(parentRepo, ".wuu", "worktrees"),
		SessionID:     "shared-worker",
		HistoryDir:    historyDir,
		ThreadDir:     threadDir,
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatalf("create agent control: %v", err)
	}
	control.StartQueuedWork()
	return control
}

func harnessTaskByID(t *testing.T, store *harness.Store, id string) harness.Task {
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
