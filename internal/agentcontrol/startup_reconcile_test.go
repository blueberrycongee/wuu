package agentcontrol

import (
	"context"
	"os"
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

// seedOrphanHarnessTask persists a harness task + run in a non-terminal
// status with no live executor, standing in for a process that died mid-run.
func seedOrphanHarnessTask(t *testing.T, harnessDir, id string, status harness.TaskStatus, startedAt time.Time) {
	t.Helper()
	store := harness.NewStore(harnessDir)
	if err := store.UpsertTask(harness.Task{
		ID:        id,
		SessionID: "sess-reconcile",
		Path:      agentthread.RootPath + "/" + id,
		Name:      id,
		Role:      DefaultSubagentType,
		Status:    status,
		LastRunID: harnessRunID(id),
		CreatedAt: startedAt,
		UpdatedAt: startedAt,
		StartedAt: startedAt,
	}); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}
	if err := store.UpsertRun(harness.AgentRun{
		ID:        harnessRunID(id),
		TaskID:    id,
		AgentID:   id,
		Role:      DefaultSubagentType,
		Status:    status,
		StartedAt: startedAt,
	}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}
}

func reconcileTestControl(t *testing.T, dir string, client providers.StreamClient) *AgentControl {
	t.Helper()
	c, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-reconcile",
		HistoryDir:    filepath.Join(dir, "workers"),
		ThreadDir:     filepath.Join(dir, "threads"),
		HarnessDir:    filepath.Join(dir, "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatalf("agentcontrol.New: %v", err)
	}
	c.StartQueuedWork()
	t.Cleanup(c.Close)
	return c
}

func loadHarnessTask(t *testing.T, harnessDir, id string) harness.Task {
	t.Helper()
	tasks, err := harness.NewStore(harnessDir).ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %q not found", id)
	return harness.Task{}
}

// TestNew_MarksOrphanedRunningTaskInterrupted is the crash story for repair
// item #5: a worker goroutine died with the process, its harness task froze
// at running, and no startup pass ever settled it. New must sweep the task
// to interrupted while preserving the original timestamps, settle the worker
// snapshot the same way, and leave the run resumable through a follow-up.
func TestNew_MarksOrphanedRunningTaskInterrupted(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	historyDir := filepath.Join(dir, "workers")
	threadDir := filepath.Join(dir, "threads")
	startedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)

	seedOrphanHarnessTask(t, harnessDir, "wk-orphan", harness.TaskStatusRunning, startedAt)
	writeResumeSnapshot(t, historyDir, "wk-orphan", subagent.ResumeSnapshotVersion, dir, "running")
	if err := agentthread.NewStore(threadDir).UpsertThread(agentthread.Metadata{
		ID:        "wk-orphan",
		ParentID:  "sess-reconcile",
		Path:      agentthread.RootPath + "/wk-orphan",
		TaskName:  "wk-orphan",
		Role:      DefaultSubagentType,
		Status:    agentthread.StatusRunning,
		CreatedAt: startedAt,
		UpdatedAt: startedAt,
		Source: agentthread.Source{
			Kind:       agentthread.SourceThreadSpawn,
			ParentPath: agentthread.RootPath,
		},
	}); err != nil {
		t.Fatalf("seed child thread: %v", err)
	}

	client := &recordingClient{resp: providers.ChatResponse{Content: "resumed after crash"}}
	c := reconcileTestControl(t, dir, client)

	task := loadHarnessTask(t, harnessDir, "wk-orphan")
	if task.Status != harness.TaskStatusInterrupted {
		t.Fatalf("expected interrupted task, got %s", task.Status)
	}
	if !task.StartedAt.Equal(startedAt) {
		t.Fatalf("reconciliation must preserve StartedAt: want %v got %v", startedAt, task.StartedAt)
	}
	if !strings.Contains(task.Error, "interrupted") {
		t.Fatalf("task error should carry the reconciliation reason, got %q", task.Error)
	}
	if task.CompletedAt.IsZero() {
		t.Fatal("interrupted task should record when it was settled")
	}
	runs, err := harness.NewStore(harnessDir).ListRuns()
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %v (%d runs)", err, len(runs))
	}
	if runs[0].Status != harness.TaskStatusInterrupted {
		t.Fatalf("expected interrupted run record, got %s", runs[0].Status)
	}

	// The worker snapshot must be settled too, so the normal rehydrate
	// path can resume it instead of rejecting a non-terminal status.
	run, err := subagent.LoadPersistedRun(filepath.Join(historyDir, "wk-orphan.json"))
	if err != nil {
		t.Fatalf("LoadPersistedRun: %v", err)
	}
	if run.Status != subagent.StatusInterrupted {
		t.Fatalf("expected interrupted snapshot, got %s", run.Status)
	}
	if !strings.Contains(run.Error, "interrupted") {
		t.Fatalf("snapshot error should carry the reason, got %q", run.Error)
	}
	threads, err := agentthread.NewStore(threadDir).ListThreads()
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	var persistedChild *agentthread.Metadata
	for index := range threads {
		if threads[index].ID == "wk-orphan" {
			persistedChild = &threads[index]
			break
		}
	}
	if persistedChild == nil || persistedChild.Status != agentthread.StatusFailed {
		t.Fatalf("persisted child thread must be terminal after reconciliation, got %+v", threads)
	}

	// CountRunning must not see the settled orphan.
	if got := c.Manager().CountRunning(); got != 0 {
		t.Fatalf("orphan reconciliation must not pollute CountRunning, got %d", got)
	}

	// An interrupted run is resumable exactly like other terminal states.
	snap, err := c.FollowupTask(context.Background(), "wk-orphan", "pick up where you left off")
	if err != nil {
		t.Fatalf("FollowupTask on interrupted run: %v", err)
	}
	if snap.Status != subagent.StatusRunning {
		t.Fatalf("expected resumed run running, got %s", snap.Status)
	}
	final, err := c.Manager().Wait(context.Background(), "wk-orphan")
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.Status != subagent.StatusCompleted || final.Result != "resumed after crash" {
		t.Fatalf("expected resumed completion, got %s result=%q", final.Status, final.Result)
	}
}

// TestNew_ReconcileSkipsRestorableQueuedTask asserts the sweep does not eat
// queued tasks whose spawn payloads restoreQueuedSpawns still owns: those are
// legitimately waiting for capacity, not crash orphans.
func TestNew_ReconcileSkipsRestorableQueuedTask(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	startedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)

	seedOrphanHarnessTask(t, harnessDir, "wk_queued", harness.TaskStatusQueued, startedAt)
	writeQueuedSpawnPayload(t, harnessDir, "wk_queued", "", "")

	c := reconcileTestControl(t, dir, &fakeClient{resp: providers.ChatResponse{Content: "queued done"}})

	task := loadHarnessTask(t, harnessDir, "wk_queued")
	if task.Status == harness.TaskStatusInterrupted {
		t.Fatalf("queued task with a restorable payload must not be marked interrupted")
	}

	// Let the background queued spawn settle before the temp dir is torn
	// down; the assertion above already ran against the post-sweep state.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sa := c.Manager().Get("wk_queued"); sa != nil {
			waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = c.Manager().Wait(waitCtx, "wk_queued")
			cancel()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.Close()
}

func TestNew_RestoreQueueFailureStopsBeforeOrphanReconcile(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	startedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	seedOrphanHarnessTask(t, harnessDir, "wk_queued", harness.TaskStatusQueued, startedAt)
	if err := os.WriteFile(filepath.Join(harnessDir, "queue.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write corrupt queue: %v", err)
	}

	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:        &fakeClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-reconcile",
		HistoryDir:    filepath.Join(dir, "workers"),
		ThreadDir:     filepath.Join(dir, "threads"),
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if control != nil {
		control.Close()
		t.Fatal("New returned a control after queued-spawn restore failed")
	}
	if err == nil || !strings.Contains(err.Error(), "restore queued spawns") || !strings.Contains(err.Error(), "queue.json") {
		t.Fatalf("New error = %v, want queued-spawn restore error", err)
	}

	task := loadHarnessTask(t, harnessDir, "wk_queued")
	if task.Status != harness.TaskStatusQueued {
		t.Fatalf("restore failure must not reconcile queued task as orphaned; status=%s", task.Status)
	}
}

// TestNew_ReconcileMarksOrphanedQueuedTaskWithoutPayload asserts a queued
// task whose spawn payload is gone (nothing will ever start it) is settled
// as interrupted instead of advertising a forever-pending spawn.
func TestNew_ReconcileMarksOrphanedQueuedTaskWithoutPayload(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	startedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)

	seedOrphanHarnessTask(t, harnessDir, "wk-lost-queue", harness.TaskStatusQueued, startedAt)

	reconcileTestControl(t, dir, &fakeClient{})

	task := loadHarnessTask(t, harnessDir, "wk-lost-queue")
	if task.Status != harness.TaskStatusInterrupted {
		t.Fatalf("expected interrupted, got %s", task.Status)
	}
}

// TestFollowupInterruptedRunWithoutSnapshotExplains locks the readable error:
// a run that crashed before any snapshot was persisted cannot resume, and the
// caller should hear the interruption story, not a bare not-found.
func TestFollowupInterruptedRunWithoutSnapshotExplains(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	startedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)

	seedOrphanHarnessTask(t, harnessDir, "wk-no-snap", harness.TaskStatusRunning, startedAt)

	c := reconcileTestControl(t, dir, &fakeClient{})

	if task := loadHarnessTask(t, harnessDir, "wk-no-snap"); task.Status != harness.TaskStatusInterrupted {
		t.Fatalf("expected interrupted, got %s", task.Status)
	}
	_, err := c.FollowupTask(context.Background(), "wk-no-snap", "resume please")
	if err == nil {
		t.Fatal("expected follow-up to fail without a snapshot")
	}
	if !strings.Contains(err.Error(), "interrupted") || !strings.Contains(err.Error(), "re-spawn") {
		t.Fatalf("error should explain the interruption and suggest re-spawning, got %v", err)
	}
}

// TestNew_ReconcileLeavesLiveAndTerminalTasksAlone asserts the sweep only
// touches orphans: terminal records stay untouched.
func TestNew_ReconcileLeavesTerminalTasksAlone(t *testing.T) {
	dir := t.TempDir()
	harnessDir := filepath.Join(dir, "harness")
	startedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)

	store := harness.NewStore(harnessDir)
	if err := store.UpsertTask(harness.Task{
		ID:        "wk-done",
		Status:    harness.TaskStatusCompleted,
		StartedAt: startedAt,
		CreatedAt: startedAt,
		UpdatedAt: startedAt,
	}); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}

	reconcileTestControl(t, dir, &fakeClient{})

	if task := loadHarnessTask(t, harnessDir, "wk-done"); task.Status != harness.TaskStatusCompleted {
		t.Fatalf("terminal task must stay untouched, got %s", task.Status)
	}
}
