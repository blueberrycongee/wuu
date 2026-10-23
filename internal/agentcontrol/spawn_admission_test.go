package agentcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestSpawnAdmissionPrepareFailureHasNoSpawnSideEffects(t *testing.T) {
	dir := t.TempDir()
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:        &fakeClient{resp: providers.ChatResponse{Content: "unused"}},
		DefaultModel:  "spawn-admission-test",
		ParentRepo:    dir,
		SessionID:     "spawn-admission-prepare-failure",
		HistoryDir:    filepath.Join(dir, "workers"),
		ThreadDir:     filepath.Join(dir, "threads"),
		HarnessDir:    filepath.Join(dir, "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	t.Cleanup(control.Close)

	workerID := ""
	result, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "must_not_exist",
		Prompt:   "do not start",
		AdmissionPrepare: func(id string) (SpawnAdmissionRollback, error) {
			workerID = id
			return nil, errors.New("prepare failed")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "prepare failed") {
		t.Fatalf("Spawn error = %v, want prepare failure", err)
	}
	if result != nil {
		t.Fatalf("Spawn result = %+v, want nil", result)
	}
	if workerID == "" {
		t.Fatal("prepare hook did not receive the final worker ID")
	}
	if control.Manager().Get(workerID) != nil {
		t.Fatal("prepare failure created a manager worker")
	}
	if _, ok := control.Threads().Resolve(workerID); ok {
		t.Fatal("prepare failure created a child thread")
	}
	if tasks, listErr := control.HarnessStore().ListTasks(); listErr != nil || len(tasks) != 0 {
		t.Fatalf("prepare failure harness tasks = %+v, err=%v", tasks, listErr)
	}
	if queue, listErr := control.HarnessStore().ListQueueItems(); listErr != nil || len(queue) != 0 {
		t.Fatalf("prepare failure queue = %+v, err=%v", queue, listErr)
	}
}

func TestSpawnAdmissionRollsBackWhenDirectLaunchFails(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "prepared")
	var rollbackAttempts atomic.Int32
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       &fakeClient{resp: providers.ChatResponse{Content: "unused"}},
		DefaultModel: "spawn-admission-test",
		ParentRepo:   dir,
		SessionID:    "spawn-admission-launch-failure",
		HistoryDir:   filepath.Join(dir, "workers"),
		ThreadDir:    filepath.Join(dir, "threads"),
		HarnessDir:   filepath.Join(dir, "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return nil, errors.New("toolkit failed")
		},
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	t.Cleanup(control.Close)

	_, err = control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "launch_failure",
		Prompt:   "fail before manager spawn",
		AdmissionPrepare: func(string) (SpawnAdmissionRollback, error) {
			if writeErr := os.WriteFile(marker, []byte("prepared"), 0o600); writeErr != nil {
				return nil, writeErr
			}
			return func() error {
				if rollbackAttempts.Add(1) <= 2 {
					return errors.New("injected transient rollback failure")
				}
				return os.Remove(marker)
			}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "toolkit failed") {
		t.Fatalf("Spawn error = %v, want toolkit failure", err)
	}
	if got := rollbackAttempts.Load(); got != 3 {
		t.Fatalf("rollback attempts = %d, want 3", got)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("prepared marker survived failed launch: %v", statErr)
	}
}

func TestQueuedSpawnAdmissionRollsBackWhenDeferredLaunchFails(t *testing.T) {
	dir := t.TempDir()
	client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	marker := filepath.Join(dir, "queued-prepared")
	var rollbackAttempts atomic.Int32
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       client,
		DefaultModel: "spawn-admission-test",
		ParentRepo:   dir,
		SessionID:    "spawn-admission-queued-failure",
		HistoryDir:   filepath.Join(dir, "workers"),
		ThreadDir:    filepath.Join(dir, "threads"),
		HarnessDir:   filepath.Join(dir, "harness"),
		MaxParallel:  1,
		WorkerFactory: func(_ string, _ WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
			if meta.TaskName == "queued_failure" {
				return nil, errors.New("queued toolkit failed")
			}
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	control.StartQueuedWork()
	t.Cleanup(func() {
		control.StopAll()
		control.Close()
	})

	occupier, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "occupier",
		Prompt:   "hold the slot",
	})
	if err != nil {
		t.Fatalf("spawn occupier: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("occupier did not start")
	}

	queued, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "queued_failure",
		Prompt:   "fail when dequeued",
		AdmissionPrepare: func(string) (SpawnAdmissionRollback, error) {
			if writeErr := os.WriteFile(marker, []byte("prepared"), 0o600); writeErr != nil {
				return nil, writeErr
			}
			return func() error {
				if rollbackAttempts.Add(1) <= 2 {
					return errors.New("injected transient rollback failure")
				}
				return os.Remove(marker)
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("queue spawn: %v", err)
	}
	if queued.Status != "queued" {
		t.Fatalf("queued status = %q, want queued", queued.Status)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("queued admission was not prepared before persistence: %v", statErr)
	}
	items, err := control.HarnessStore().ListQueueItems()
	if err != nil || len(items) != 1 || items[0].ID != queued.AgentID {
		t.Fatalf("persisted queue = %+v, err=%v", items, err)
	}

	close(client.release)
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := control.Manager().Wait(waitCtx, occupier.AgentID); err != nil {
		t.Fatalf("wait occupier: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(marker); errors.Is(statErr, os.ErrNotExist) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := rollbackAttempts.Load(); got != 3 {
		t.Fatalf("queued rollback attempts = %d, want 3", got)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("queued prepared marker survived failed launch: %v", statErr)
	}
	if control.Manager().Get(queued.AgentID) != nil {
		t.Fatal("failed queued launch created a manager worker")
	}
}

func TestStopAllCancelsRunningWorkersBeforeQueuedAdmissionCompensation(t *testing.T) {
	dir := t.TempDir()
	client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       client,
		DefaultModel: "stop-all-ordering-test",
		ParentRepo:   dir,
		SessionID:    "stop-all-ordering",
		HistoryDir:   filepath.Join(dir, "workers"),
		ThreadDir:    filepath.Join(dir, "threads"),
		HarnessDir:   filepath.Join(dir, "harness"),
		MaxParallel:  1,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	control.StartQueuedWork()
	var releaseRollbackOnce sync.Once
	releaseRollback := make(chan struct{})
	t.Cleanup(func() {
		releaseRollbackOnce.Do(func() { close(releaseRollback) })
		close(client.release)
		stopAndCloseAgentControlForTest(t, control)
	})

	if _, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "running_before_shutdown",
		Prompt:   "hold the only slot",
	}); err != nil {
		t.Fatalf("spawn running worker: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("running worker did not enter its provider request")
	}

	rollbackEntered := make(chan struct{})
	if queued, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "queued_during_shutdown",
		Prompt:   "wait behind the running worker",
		AdmissionPrepare: func(string) (SpawnAdmissionRollback, error) {
			return func() error {
				close(rollbackEntered)
				<-releaseRollback
				return nil
			}, nil
		},
	}); err != nil {
		t.Fatalf("queue worker: %v", err)
	} else if queued.Status != string(subagent.StatusQueued) {
		t.Fatalf("queued worker status = %q, want queued", queued.Status)
	}

	control.BeginShutdown()
	stopDone := make(chan struct{})
	go func() {
		control.StopAll()
		close(stopDone)
	}()
	select {
	case <-rollbackEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll did not enter queued admission compensation")
	}
	deadline := time.Now().Add(2 * time.Second)
	for control.Manager().CountRunning() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := control.Manager().CountRunning(); got != 0 {
		t.Fatalf("running workers during blocked queue compensation = %d, want 0", got)
	}
	select {
	case <-stopDone:
		t.Fatal("StopAll returned before queued admission compensation completed")
	default:
	}
	releaseRollbackOnce.Do(func() { close(releaseRollback) })
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("StopAll did not finish after queued admission compensation")
	}
}

func TestShutdownRacingSpawnPreparationCannotPublishQueuedWorker(t *testing.T) {
	dir := t.TempDir()
	client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       client,
		DefaultModel: "spawn-admission-test",
		ParentRepo:   dir,
		SessionID:    "spawn-admission-shutdown-race",
		HistoryDir:   filepath.Join(dir, "workers"),
		ThreadDir:    filepath.Join(dir, "threads"),
		HarnessDir:   filepath.Join(dir, "harness"),
		MaxParallel:  1,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	control.StartQueuedWork()
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, control) })

	if _, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "occupier",
		Prompt:   "hold the only slot",
	}); err != nil {
		t.Fatalf("spawn occupier: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("occupier did not start")
	}

	prepareEntered := make(chan struct{})
	allowPrepare := make(chan struct{})
	var rollbackCalls atomic.Int32
	spawnDone := make(chan error, 1)
	go func() {
		_, spawnErr := control.Spawn(context.Background(), SpawnRequest{
			Type:     DefaultSubagentType,
			TaskName: "late_queue",
			Prompt:   "must not survive shutdown",
			AdmissionPrepare: func(string) (SpawnAdmissionRollback, error) {
				close(prepareEntered)
				<-allowPrepare
				return func() error {
					rollbackCalls.Add(1)
					return nil
				}, nil
			},
		})
		spawnDone <- spawnErr
	}()
	select {
	case <-prepareEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("queued spawn did not reach admission preparation")
	}

	control.BeginShutdown()
	control.StopAll()
	close(allowPrepare)
	select {
	case spawnErr := <-spawnDone:
		if !errors.Is(spawnErr, errAgentControlStopping) {
			t.Fatalf("late queued spawn error = %v, want shutdown rejection", spawnErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late queued spawn did not finish after preparation released")
	}
	if got := rollbackCalls.Load(); got != 1 {
		t.Fatalf("late admission rollback calls = %d, want 1", got)
	}
	if items, err := control.HarnessStore().ListQueueItems(); err != nil || len(items) != 0 {
		t.Fatalf("late queued spawn survived shutdown: items=%+v err=%v", items, err)
	}
	if tasks, err := control.HarnessStore().ListTasks(); err != nil || len(tasks) != 1 {
		// Only the already-running occupier may have produced a harness task.
		t.Fatalf("unexpected harness tasks after shutdown race: tasks=%+v err=%v", tasks, err)
	}
}

func TestShutdownRacingDirectSpawnPreparationHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "direct-spawn-shutdown", "harness")
	worktreeRoot := filepath.Join(dir, ".wuu-state", "worktrees")
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       &fakeClient{resp: providers.ChatResponse{Content: "unused"}},
		DefaultModel: "spawn-admission-test",
		ParentRepo:   dir,
		WorktreeRoot: worktreeRoot,
		SessionID:    "direct-spawn-shutdown",
		HistoryDir:   filepath.Join(dir, ".wuu-state", "sessions", "direct-spawn-shutdown", "workers"),
		ThreadDir:    filepath.Join(dir, ".wuu-state", "sessions", "direct-spawn-shutdown", "threads"),
		HarnessDir:   harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	t.Cleanup(control.Close)

	prepareEntered := make(chan struct{})
	allowPrepare := make(chan struct{})
	var rollbackCalls atomic.Int32
	workerID := ""
	spawnDone := make(chan error, 1)
	go func() {
		_, spawnErr := control.Spawn(context.Background(), SpawnRequest{
			Type:      DefaultSubagentType,
			TaskName:  "late_direct_spawn",
			Prompt:    "must not create lifecycle state after shutdown",
			Isolation: string(IsolationWorktree),
			AdmissionPrepare: func(id string) (SpawnAdmissionRollback, error) {
				workerID = id
				close(prepareEntered)
				<-allowPrepare
				return func() error {
					rollbackCalls.Add(1)
					return nil
				}, nil
			},
		})
		spawnDone <- spawnErr
	}()
	select {
	case <-prepareEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("direct spawn did not reach admission preparation")
	}

	control.BeginShutdown()
	control.StopAll()
	close(allowPrepare)
	select {
	case spawnErr := <-spawnDone:
		if !errors.Is(spawnErr, errAgentControlStopping) {
			t.Fatalf("late direct spawn error = %v, want shutdown rejection", spawnErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late direct spawn did not finish after preparation released")
	}
	if workerID == "" {
		t.Fatal("spawn preparation did not receive a worker ID")
	}
	if got := rollbackCalls.Load(); got != 1 {
		t.Fatalf("late direct admission rollback calls = %d, want 1", got)
	}
	if control.Manager().Get(workerID) != nil {
		t.Fatal("shutdown-rejected direct spawn reached the manager")
	}
	if _, ok := control.Threads().Resolve(workerID); ok {
		t.Fatal("shutdown-rejected direct spawn registered a child thread")
	}
	if tasks, listErr := control.HarnessStore().ListTasks(); listErr != nil || len(tasks) != 0 {
		t.Fatalf("shutdown-rejected direct spawn harness tasks = %+v, err=%v", tasks, listErr)
	}
	if control.OwnedWorkerExecutionCount() != 0 {
		t.Fatalf("shutdown-rejected direct spawn owns %d worker leases, want 0", control.OwnedWorkerExecutionCount())
	}
	leaseDir := filepath.Join(harnessDir, workerExecutionLeaseDirName)
	if entries, readErr := os.ReadDir(leaseDir); readErr == nil && len(entries) != 0 {
		t.Fatalf("shutdown-rejected direct spawn created worker lease files: %+v", entries)
	} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("inspect worker lease directory: %v", readErr)
	}
	worktrees, listErr := control.worktrees.List(control.sessionID)
	if listErr != nil || len(worktrees) != 0 {
		t.Fatalf("shutdown-rejected direct spawn worktrees = %+v, err=%v", worktrees, listErr)
	}
}

func TestDirectForkAdmissionCoversPreparationThroughManagerCommit(t *testing.T) {
	dir := t.TempDir()
	client := &blockingStreamClient{release: make(chan struct{})}
	factoryEntered := make(chan struct{})
	allowFactory := make(chan struct{})
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       client,
		DefaultModel: "spawn-admission-test",
		ParentRepo:   dir,
		SessionID:    "direct-fork-shutdown",
		HistoryDir:   filepath.Join(dir, "workers"),
		ThreadDir:    filepath.Join(dir, "threads"),
		HarnessDir:   filepath.Join(dir, "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			close(factoryEntered)
			<-allowFactory
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, control) })

	type forkOutcome struct {
		result *SpawnResult
		err    error
	}
	forkDone := make(chan forkOutcome, 1)
	go func() {
		result, forkErr := control.Fork(context.Background(), ForkRequest{
			TaskName:  "admitted_fork",
			Prompt:    "finish after shutdown begins",
			Isolation: string(IsolationInplace),
		}, []providers.ChatMessage{{Role: "user", Content: "parent context"}})
		forkDone <- forkOutcome{result: result, err: forkErr}
	}()
	select {
	case <-factoryEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("direct fork did not reach worker preparation")
	}

	shutdownDone := make(chan struct{})
	go func() {
		control.BeginShutdown()
		control.StopAll()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown passed an admitted direct fork before Manager.Spawn committed")
	case <-time.After(100 * time.Millisecond):
	}
	close(allowFactory)

	var outcome forkOutcome
	select {
	case outcome = <-forkDone:
	case <-time.After(5 * time.Second):
		t.Fatal("direct fork did not finish after preparation released")
	}
	if outcome.err != nil {
		t.Fatalf("admitted direct fork failed during shutdown: %v", outcome.err)
	}
	if outcome.result == nil {
		t.Fatal("admitted direct fork returned no result")
	}
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after direct fork committed")
	}
	if control.Manager().Get(outcome.result.AgentID) == nil {
		t.Fatal("admitted direct fork was not visible to StopAll")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, waitErr := control.Manager().Wait(waitCtx, outcome.result.AgentID); waitErr != nil {
		t.Fatalf("wait for shutdown-cancelled fork: %v", waitErr)
	}
}

func TestQueuedPublicationFailureSettlesThreadLifecycle(t *testing.T) {
	for _, fork := range []bool{false, true} {
		name := "spawn"
		if fork {
			name = "fork"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			harnessDir := filepath.Join(dir, "harness")
			client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
			control, err := New(Config{
				AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

				Client:       client,
				DefaultModel: "spawn-admission-test",
				ParentRepo:   dir,
				SessionID:    "queued-publication-failure-" + name,
				HistoryDir:   filepath.Join(dir, "workers"),
				ThreadDir:    filepath.Join(dir, "threads"),
				HarnessDir:   harnessDir,
				MaxParallel:  1,
				WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
					return fakeToolkit{}, nil
				},
			})
			if err != nil {
				t.Fatalf("create AgentControl: %v", err)
			}
			queuePath := filepath.Join(harnessDir, "queue.json")
			t.Cleanup(func() {
				_ = os.Remove(queuePath)
				stopAndCloseAgentControlForTest(t, control)
			})

			if _, err := control.Spawn(context.Background(), SpawnRequest{
				Type:     DefaultSubagentType,
				TaskName: "occupier",
				Prompt:   "hold the only slot",
			}); err != nil {
				t.Fatalf("spawn occupier: %v", err)
			}
			select {
			case <-client.started:
			case <-time.After(5 * time.Second):
				t.Fatal("occupier did not start")
			}
			if err := os.Mkdir(queuePath, 0o755); err != nil {
				t.Fatalf("inject queue persistence failure: %v", err)
			}

			taskName := "queued_" + name + "_failure"
			var result *SpawnResult
			if fork {
				result, err = control.Fork(context.Background(), ForkRequest{
					TaskName: taskName,
					Prompt:   "must settle after queue write failure",
				}, []providers.ChatMessage{{Role: "user", Content: "parent context"}})
			} else {
				result, err = control.Spawn(context.Background(), SpawnRequest{
					Type:     DefaultSubagentType,
					TaskName: taskName,
					Prompt:   "must settle after queue write failure",
				})
			}
			if err == nil || !strings.Contains(err.Error(), "persist queued spawn") {
				t.Fatalf("queued %s error = %v, want persistence failure", name, err)
			}
			if result != nil {
				t.Fatalf("queued %s result = %+v, want nil", name, result)
			}
			if control.hasQueuedSpawns() {
				t.Fatalf("failed queued %s remained in the in-memory queue", name)
			}

			var failed agentthread.Metadata
			for _, meta := range control.Threads().List() {
				if meta.TaskName == taskName {
					failed = meta
					break
				}
			}
			if failed.ID == "" {
				t.Fatalf("failed queued %s thread was not registered", name)
			}
			if failed.Status != agentthread.StatusFailed || failed.Source.EdgeStatus != agentthread.EdgeClosed {
				t.Fatalf("failed queued %s metadata = %+v", name, failed)
			}
			persisted, listErr := control.threadStore.ListThreads()
			if listErr != nil {
				t.Fatalf("list persisted threads: %v", listErr)
			}
			persistedFailed := agentthread.Metadata{}
			for _, meta := range persisted {
				if meta.ID == failed.ID {
					persistedFailed = meta
					break
				}
			}
			if persistedFailed.Status != agentthread.StatusFailed || persistedFailed.Source.EdgeStatus != agentthread.EdgeClosed {
				t.Fatalf("persisted failed queued %s metadata = %+v", name, persistedFailed)
			}
			tasks, listErr := control.HarnessStore().ListTasks()
			if listErr != nil {
				t.Fatalf("list harness tasks: %v", listErr)
			}
			var failedTask harness.Task
			for _, task := range tasks {
				if task.ID == failed.ID {
					failedTask = task
					break
				}
			}
			if failedTask.Status != harness.TaskStatusFailed || !strings.Contains(failedTask.Error, "persist queued spawn") {
				t.Fatalf("failed queued %s harness task = %+v", name, failedTask)
			}
		})
	}
}

func TestQueuedSpawnCancellationRollsBackAdmission(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*AgentControl, string) bool
	}{
		{
			name: "targeted stop",
			cancel: func(control *AgentControl, workerID string) bool {
				return control.Stop(workerID)
			},
		},
		{
			name: "stop all",
			cancel: func(control *AgentControl, _ string) bool {
				control.StopAll()
				return true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
			marker := filepath.Join(dir, "queued-cancel-prepared")
			var rollbackAttempts atomic.Int32
			var tombstoneObserved atomic.Bool
			var queuedLaunches atomic.Int32
			control, err := New(Config{
				AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

				Client:       client,
				DefaultModel: "spawn-admission-test",
				ParentRepo:   dir,
				SessionID:    "spawn-admission-queued-cancel",
				HistoryDir:   filepath.Join(dir, "workers"),
				ThreadDir:    filepath.Join(dir, "threads"),
				HarnessDir:   filepath.Join(dir, "harness"),
				MaxParallel:  1,
				WorkerFactory: func(_ string, _ WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
					if meta.TaskName == "queued_cancel" {
						queuedLaunches.Add(1)
					}
					return fakeToolkit{}, nil
				},
			})
			if err != nil {
				t.Fatalf("create AgentControl: %v", err)
			}
			control.StartQueuedWork()
			t.Cleanup(func() {
				control.StopAll()
				control.Close()
			})

			occupier, err := control.Spawn(context.Background(), SpawnRequest{
				Type:     DefaultSubagentType,
				TaskName: "occupier",
				Prompt:   "hold the slot",
			})
			if err != nil {
				t.Fatalf("spawn occupier: %v", err)
			}
			select {
			case <-client.started:
			case <-time.After(5 * time.Second):
				t.Fatal("occupier did not start")
			}

			queued, err := control.Spawn(context.Background(), SpawnRequest{
				Type:     DefaultSubagentType,
				TaskName: "queued_cancel",
				Prompt:   "must never launch",
				AdmissionPrepare: func(workerID string) (SpawnAdmissionRollback, error) {
					if writeErr := os.WriteFile(marker, []byte("prepared"), 0o600); writeErr != nil {
						return nil, writeErr
					}
					return func() error {
						if rollbackAttempts.Load() == 0 {
							if item, found, itemErr := control.HarnessStore().GetQueueItem(workerID); itemErr == nil && found && item.State == harness.QueueItemStateCancelling {
								tombstoneObserved.Store(true)
							}
						}
						if rollbackAttempts.Add(1) <= 2 {
							return errors.New("injected transient rollback failure")
						}
						return os.Remove(marker)
					}, nil
				},
			})
			if err != nil {
				t.Fatalf("queue spawn: %v", err)
			}
			if queued.Status != "queued" {
				t.Fatalf("queued status = %q, want queued", queued.Status)
			}
			if !test.cancel(control, queued.AgentID) {
				t.Fatal("queued cancellation reported failure")
			}
			if got := rollbackAttempts.Load(); got != 3 {
				t.Fatalf("rollback attempts = %d, want 3", got)
			}
			if !tombstoneObserved.Load() {
				t.Fatal("queued cancellation rollback ran before its durable tombstone")
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("queued cancellation left prepared marker: %v", statErr)
			}
			if items, listErr := control.HarnessStore().ListQueueItems(); listErr != nil || len(items) != 0 {
				t.Fatalf("queued cancellation left durable payload: %+v, err=%v", items, listErr)
			}
			if meta, ok := control.Threads().Resolve(queued.AgentID); !ok || meta.Status != agentthread.StatusCancelled || meta.Source.EdgeStatus != agentthread.EdgeClosed {
				t.Fatalf("queued cancellation metadata = %+v, ok=%v", meta, ok)
			}

			close(client.release)
			waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := control.Manager().Wait(waitCtx, occupier.AgentID); err != nil {
				t.Fatalf("wait occupier: %v", err)
			}
			time.Sleep(50 * time.Millisecond)
			if got := queuedLaunches.Load(); got != 0 {
				t.Fatalf("cancelled queued worker launched %d times", got)
			}
			if control.Manager().Get(queued.AgentID) != nil {
				t.Fatal("cancelled queued worker reached manager")
			}
		})
	}
}
