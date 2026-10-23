package agentcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestQueuedSpawnAckRetriesBeforeReleasingExecutionLease(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_ack_retry"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())

	client := &queuedSpawnCountingClient{}
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		harnessDir,
		1,
		client,
		nil,
	)
	firstAckEntered := make(chan struct{})
	releaseFirstAck := make(chan struct{})
	terminalRelease := make(chan struct{}, 1)
	var releaseAckOnce sync.Once
	var ackAttempts atomic.Int32
	t.Cleanup(func() { releaseAckOnce.Do(func() { close(releaseFirstAck) }) })
	control.queuedSpawnAckHookMu.Lock()
	control.queuedSpawnAckForTest = func(id string) error {
		if id != workerID {
			return nil
		}
		attempt := ackAttempts.Add(1)
		if attempt == 1 {
			close(firstAckEntered)
			<-releaseFirstAck
		}
		if attempt <= 2 {
			return errors.New("injected queue acknowledgement failure")
		}
		return nil
	}
	control.queuedSpawnAckHookMu.Unlock()
	control.SetWorkerExecutionReleaseHookForTest(func(id string) {
		if id == workerID {
			terminalRelease <- struct{}{}
		}
	})
	control.StartQueuedWork()
	waitForChannelForConcurrencyTest(t, firstAckEntered, "first queued acknowledgement attempt")

	if worker := control.Manager().Get(workerID); worker == nil {
		t.Fatal("Manager.Spawn was not observable before queue acknowledgement")
	}
	waitForChannelForConcurrencyTest(t, terminalRelease, "terminal path to attempt physical lease release")
	if exists, err := control.HarnessStore().QueueItemExists(workerID); err != nil || !exists {
		t.Fatalf("durable payload during failed acknowledgement = %v, %v; want true, nil", exists, err)
	}
	if !control.workerExecutionOwned(workerID) {
		t.Fatal("worker execution lease was released before queue acknowledgement")
	}
	if !control.HasOwnedWorkerExecutions() {
		t.Fatal("runtime reported no owned executions while physical lease release was blocked")
	}

	releaseAckOnce.Do(func() { close(releaseFirstAck) })
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		exists, existsErr := control.HarnessStore().QueueItemExists(workerID)
		return existsErr == nil && !exists && ackAttempts.Load() >= 3 && !control.workerExecutionOwned(workerID)
	}, "queued acknowledgement to retry until durable")
	if got := client.calls.Load(); got != 1 {
		t.Fatalf("provider executions = %d, want exactly 1", got)
	}
}

func TestShutdownYieldsLaunchedQueueAckOnlyAfterTerminalIntentIsDurable(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_shutdown_ack_yield"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())

	client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		harnessDir,
		1,
		client,
		nil,
	)
	firstAck := make(chan struct{})
	var firstAckOnce sync.Once
	var ackAttempts atomic.Int32
	control.queuedSpawnAckHookMu.Lock()
	control.queuedSpawnAckForTest = func(id string) error {
		if id != workerID {
			return nil
		}
		ackAttempts.Add(1)
		firstAckOnce.Do(func() { close(firstAck) })
		return errors.New("injected persistent queue acknowledgement failure")
	}
	control.queuedSpawnAckHookMu.Unlock()
	control.StartQueuedWork()
	waitForChannelForConcurrencyTest(t, client.started, "queued worker provider request")
	waitForChannelForConcurrencyTest(t, firstAck, "queued worker acknowledgement retry")
	if pending, err := control.workerTerminalFinalizationPending(workerID); err != nil || pending {
		t.Fatalf("terminal intent before shutdown = %t, %v; want false, nil", pending, err)
	}

	control.BeginShutdown()
	stopDone := make(chan struct{})
	go func() {
		control.StopAll()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll remained blocked behind a launched queue acknowledgement")
	}
	control.YieldWorkerTerminalFinalizations()
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return !control.HasOwnedWorkerExecutions()
	}, "shutdown-yielded worker execution lease release")
	if pending, err := control.workerTerminalFinalizationPending(workerID); err != nil || !pending {
		t.Fatalf("durable terminal recovery authority = %t, %v; want true, nil", pending, err)
	}
	if exists, err := control.HarnessStore().QueueItemExists(workerID); err != nil || !exists {
		t.Fatalf("durable queue recovery authority = %t, %v; want true, nil", exists, err)
	}
	if got := ackAttempts.Load(); got < 2 {
		t.Fatalf("queue acknowledgement attempts = %d, want retry before durable yield", got)
	}
}

func TestShutdownYieldsDurableQueuedCancellationCompensation(t *testing.T) {
	tests := []struct {
		name             string
		rollbackFailure  bool
		acknowledgeError bool
	}{
		{name: "rollback outage", rollbackFailure: true},
		{name: "acknowledgement outage", acknowledgeError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootDir := t.TempDir()
			client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
			control := newQueuedSpawnConcurrencyControl(
				t,
				rootDir,
				filepath.Join(rootDir, "control"),
				filepath.Join(rootDir, "harness"),
				1,
				client,
				nil,
			)
			if _, err := control.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "occupier", Prompt: "hold capacity"}); err != nil {
				t.Fatalf("spawn occupier: %v", err)
			}
			waitForChannelForConcurrencyTest(t, client.started, "occupier provider request")
			var rollbackAttempts atomic.Int32
			queued, err := control.Spawn(context.Background(), SpawnRequest{
				Type:     DefaultSubagentType,
				TaskName: "cancel_during_shutdown",
				Prompt:   "remain queued",
				AdmissionPrepare: func(string) (SpawnAdmissionRollback, error) {
					return func() error {
						rollbackAttempts.Add(1)
						if test.rollbackFailure {
							return errors.New("persistent rollback outage")
						}
						return nil
					}, nil
				},
			})
			if err != nil {
				t.Fatalf("queue worker: %v", err)
			}
			if test.acknowledgeError {
				control.queuedSpawnAckHookMu.Lock()
				control.queuedSpawnAckForTest = func(id string) error {
					if id == queued.AgentID {
						return errors.New("persistent acknowledgement outage")
					}
					return nil
				}
				control.queuedSpawnAckHookMu.Unlock()
			}

			control.BeginShutdown()
			stopDone := make(chan struct{})
			go func() {
				control.StopAll()
				close(stopDone)
			}()
			waitForChannelForConcurrencyTest(t, stopDone, "shutdown cancellation to yield to durable recovery")
			item, found, err := control.HarnessStore().GetQueueItem(queued.AgentID)
			if err != nil || !found || item.State != harness.QueueItemStateCancelling {
				t.Fatalf("durable cancellation recovery item = %+v, %t, %v", item, found, err)
			}
			if rollbackAttempts.Load() == 0 {
				t.Fatal("queued admission rollback was not attempted")
			}
		})
	}
}

func TestShutdownYieldsRestoredQueuedTerminalTombstoneAcknowledgement(t *testing.T) {
	tests := []struct {
		name  string
		state harness.QueueItemState
	}{
		{name: "cancelling", state: harness.QueueItemStateCancelling},
		{name: "failing", state: harness.QueueItemStateFailing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootDir := t.TempDir()
			harnessDir := filepath.Join(rootDir, "harness")
			workerID := "restored_" + test.name
			persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())
			store := harness.NewStore(harnessDir)
			var marked bool
			var err error
			if test.state == harness.QueueItemStateCancelling {
				marked, err = store.MarkQueueItemCancelling(workerID)
			} else {
				marked, err = store.MarkQueueItemFailing(workerID, "persisted launch failure")
			}
			if err != nil || !marked {
				t.Fatalf("mark restored queue state: %t, %v", marked, err)
			}
			control := newDormantQueuedSpawnConcurrencyControl(
				t,
				rootDir,
				filepath.Join(rootDir, "control"),
				harnessDir,
				1,
				&queuedSpawnCountingClient{},
				nil,
			)
			ackEntered := make(chan struct{})
			var ackOnce sync.Once
			control.queuedSpawnAckHookMu.Lock()
			control.queuedSpawnAckForTest = func(id string) error {
				if id == workerID {
					ackOnce.Do(func() { close(ackEntered) })
					return errors.New("persistent acknowledgement outage")
				}
				return nil
			}
			control.queuedSpawnAckHookMu.Unlock()
			control.StartQueuedWork()
			waitForChannelForConcurrencyTest(t, ackEntered, "restored terminal tombstone acknowledgement")
			control.BeginShutdown()
			stopDone := make(chan struct{})
			go func() {
				control.StopAll()
				close(stopDone)
			}()
			waitForChannelForConcurrencyTest(t, stopDone, "restored terminal tombstone shutdown yield")
			item, found, err := store.GetQueueItem(workerID)
			if err != nil || !found || item.State != test.state {
				t.Fatalf("restored recovery item = %+v, %t, %v; want %s", item, found, err, test.state)
			}
			if control.workerExecutionOwned(workerID) {
				t.Fatal("restored terminal tombstone retained execution lease after shutdown yield")
			}
		})
	}
}

func TestShutdownYieldsGenericQueuedFailureBeforeTerminalTombstone(t *testing.T) {
	rootDir := t.TempDir()
	client := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	control := newQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		filepath.Join(rootDir, "harness"),
		1,
		client,
		func(_ string, _ WorkerType, meta agentthread.Metadata) (agent.ToolExecutor, error) {
			if meta.TaskName == "fails_before_manager_spawn" {
				return nil, errors.New("injected pre-spawn failure")
			}
			return fakeToolkit{}, nil
		},
	)
	occupier, err := control.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "occupier", Prompt: "hold capacity"})
	if err != nil {
		t.Fatalf("spawn occupier: %v", err)
	}
	waitForChannelForConcurrencyTest(t, client.started, "occupier provider request")
	queued, err := control.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "fails_before_manager_spawn", Prompt: "fail during queued preparation"})
	if err != nil {
		t.Fatalf("queue failing worker: %v", err)
	}
	markEntered := make(chan struct{})
	releaseMark := make(chan struct{})
	var markOnce sync.Once
	control.queuedSpawnAckHookMu.Lock()
	control.queuedSpawnMarkFailureForTest = func(id string) error {
		if id == queued.AgentID {
			markOnce.Do(func() { close(markEntered) })
			<-releaseMark
			return errors.New("persistent terminal tombstone outage")
		}
		return nil
	}
	control.queuedSpawnAckHookMu.Unlock()
	if !control.Stop(occupier.AgentID) {
		t.Fatal("stop occupier")
	}
	waitForChannelForConcurrencyTest(t, markEntered, "generic queued failure tombstone attempt")
	control.BeginShutdown()
	close(releaseMark)
	drainDone := make(chan struct{})
	go func() {
		control.queueDrainMu.Lock()
		control.queueDrainMu.Unlock()
		close(drainDone)
	}()
	waitForChannelForConcurrencyTest(t, drainDone, "generic queued failure to yield during shutdown")
	item, found, err := control.HarnessStore().GetQueueItem(queued.AgentID)
	if err != nil || !found || (item.State != "" && item.State != harness.QueueItemStateQueued) {
		t.Fatalf("retryable launch intent after shutdown yield = %+v, %t, %v", item, found, err)
	}
	if control.workerExecutionOwned(queued.AgentID) {
		t.Fatal("generic queued failure retained its execution lease after shutdown yield")
	}
	if got := queuedSpawnCountForTest(control); got != 1 {
		t.Fatalf("in-memory retry queue count = %d, want 1", got)
	}
}

func TestQueuedSpawnFailureAcknowledgesBeforeReleasingExecutionLease(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_failed_launch_handoff"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())

	ackEntered := make(chan struct{})
	releaseAck := make(chan struct{})
	rollbackEntered := make(chan struct{})
	releaseRollback := make(chan struct{})
	var releaseAckOnce sync.Once
	var releaseRollbackOnce sync.Once
	t.Cleanup(func() {
		releaseRollbackOnce.Do(func() { close(releaseRollback) })
		releaseAckOnce.Do(func() { close(releaseAck) })
	})
	failed := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "failed-control"),
		harnessDir,
		1,
		&queuedSpawnCountingClient{},
		func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return nil, errors.New("injected worker factory failure")
		},
	)
	var rollbackAttempts atomic.Int32
	failed.queueMu.Lock()
	if len(failed.queued) != 1 || failed.queued[0].WorkerID != workerID {
		failed.queueMu.Unlock()
		t.Fatalf("restored queued payloads = %+v, want %s", failed.queued, workerID)
	}
	failed.queued[0].AdmissionRollback = func() error {
		attempt := rollbackAttempts.Add(1)
		if attempt == 1 {
			close(rollbackEntered)
			<-releaseRollback
		}
		if attempt <= 2 {
			return errors.New("injected admission rollback failure")
		}
		return nil
	}
	failed.queueMu.Unlock()
	failed.queuedSpawnAckHookMu.Lock()
	failed.queuedSpawnAckForTest = func(id string) error {
		if id == workerID {
			close(ackEntered)
			<-releaseAck
		}
		return nil
	}
	failed.queuedSpawnAckHookMu.Unlock()
	failed.StartQueuedWork()
	waitForChannelForConcurrencyTest(t, rollbackEntered, "failed launch admission rollback")

	if item, exists, err := failed.HarnessStore().GetQueueItem(workerID); err != nil || !exists || item.State != harness.QueueItemStateFailing {
		t.Fatalf("failed launch payload during rollback = %+v, %v, %v; want failing, true, nil", item, exists, err)
	}
	if !failed.workerExecutionOwned(workerID) {
		t.Fatal("failed launcher released execution lease before admission rollback")
	}

	contenderClient := &queuedSpawnCountingClient{}
	contender := newQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "contender-control"),
		harnessDir,
		1,
		contenderClient,
		nil,
	)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return queuedSpawnRetryPendingForTest(contender)
	}, "contender to wait on failed launch acknowledgement")
	if got := contenderClient.calls.Load(); got != 0 {
		t.Fatalf("contender provider executions during failure rollback = %d, want 0", got)
	}

	releaseRollbackOnce.Do(func() { close(releaseRollback) })
	waitForChannelForConcurrencyTest(t, ackEntered, "failed launch acknowledgement after rollback")
	if got := rollbackAttempts.Load(); got != 3 {
		t.Fatalf("admission rollback attempts = %d, want 3", got)
	}
	if exists, err := failed.HarnessStore().QueueItemExists(workerID); err != nil || !exists {
		t.Fatalf("failed launch payload before acknowledgement = %v, %v; want true, nil", exists, err)
	}
	if !failed.workerExecutionOwned(workerID) {
		t.Fatal("failed launcher released execution lease before queue acknowledgement")
	}
	releaseAckOnce.Do(func() { close(releaseAck) })
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		exists, existsErr := failed.HarnessStore().QueueItemExists(workerID)
		return existsErr == nil && !exists &&
			queuedSpawnCountForTest(contender) == 0 &&
			!queuedSpawnRetryPendingForTest(contender)
	}, "failed queue item to settle before contender retries")
	if got := contenderClient.calls.Load(); got != 0 {
		t.Fatalf("contender provider executions after failed queue settlement = %d, want 0", got)
	}
	if contender.Manager().Get(workerID) != nil {
		t.Fatal("contender registered a worker for an already-settled failed launch")
	}
}
func TestQueuedSpawnDefersToDurableTerminalEvidence(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_with_terminal_and_queue_intent"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())

	client := &queuedSpawnCountingClient{}
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "terminal-evidence-control"),
		harnessDir,
		1,
		client,
		nil,
	)
	notification := subagent.Notification{
		AgentID: workerID,
		Status:  subagent.StatusCompleted,
		Snapshot: subagent.SubAgentSnapshot{
			ID:          workerID,
			Status:      subagent.StatusCompleted,
			StartedAt:   time.Now().Add(-time.Second).UTC(),
			CompletedAt: time.Now().UTC(),
			Result:      "already completed before restart",
		},
	}
	if err := control.persistWorkerTerminalFinalization(notification); err != nil {
		t.Fatalf("persist terminal evidence: %v", err)
	}
	var finalizerCalls atomic.Int32
	control.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error {
		if n.AgentID != workerID || n.Status != subagent.StatusCompleted {
			return errors.New("unexpected terminal generation")
		}
		finalizerCalls.Add(1)
		return nil
	})
	control.StartWorkerTerminalRecovery()
	control.StartQueuedWork()

	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		exists, existsErr := control.HarnessStore().QueueItemExists(workerID)
		pending, pendingErr := control.workerTerminalFinalizationPending(workerID)
		return existsErr == nil && !exists && pendingErr == nil && !pending && finalizerCalls.Load() == 1
	}, "terminal recovery to consume stale queue intent")
	if got := client.calls.Load(); got != 0 {
		t.Fatalf("provider executions with durable terminal evidence = %d, want 0", got)
	}
	if control.Manager().Get(workerID) != nil {
		t.Fatal("stale queue intent launched despite durable terminal evidence")
	}
}

func TestDirectSpawnAndForkReserveCapacityBeforeManagerRegistration(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	providerRelease := make(chan struct{})
	client := &queuedSpawnLimitClient{
		release: providerRelease,
		started: make(chan struct{}, 8),
	}
	firstFactoryEntered := make(chan struct{})
	firstFactoryRelease := make(chan struct{})
	var factoryCalls atomic.Int32
	workerFactory := func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
		if factoryCalls.Add(1) == 1 {
			close(firstFactoryEntered)
			<-firstFactoryRelease
		}
		return fakeToolkit{}, nil
	}
	control := newQueuedSpawnConcurrencyControl(t, rootDir, filepath.Join(rootDir, "control"), harnessDir, 1, client, workerFactory)
	var releaseFactoryOnce sync.Once
	var releaseProviderOnce sync.Once
	t.Cleanup(func() {
		releaseFactoryOnce.Do(func() { close(firstFactoryRelease) })
		releaseProviderOnce.Do(func() { close(providerRelease) })
	})

	firstResult := make(chan *SpawnResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		result, err := control.Spawn(context.Background(), SpawnRequest{
			Type:     DefaultSubagentType,
			TaskName: "direct_reserved",
			Prompt:   "hold launch preparation",
		})
		firstResult <- result
		firstErr <- err
	}()
	waitForChannelForConcurrencyTest(t, firstFactoryEntered, "direct spawn preparation")

	queuedSpawn, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "queued_behind_direct",
		Prompt:   "wait behind direct spawn",
	})
	if err != nil {
		t.Fatalf("queue concurrent direct spawn: %v", err)
	}
	if queuedSpawn.Status != string(subagent.StatusQueued) {
		t.Fatalf("concurrent direct spawn status = %q, want queued", queuedSpawn.Status)
	}
	queuedFork, err := control.Fork(context.Background(), ForkRequest{
		TaskName: "fork_behind_direct",
		Prompt:   "wait behind direct spawn",
	}, []providers.ChatMessage{{Role: "user", Content: "parent context"}})
	if err != nil {
		t.Fatalf("queue concurrent fork: %v", err)
	}
	if queuedFork.Status != string(subagent.StatusQueued) {
		t.Fatalf("concurrent fork status = %q, want queued", queuedFork.Status)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("worker preparations before first Manager.Spawn = %d, want 1", got)
	}
	if got := queuedSpawnCountForTest(control); got != 2 {
		t.Fatalf("queued workers behind reserved slot = %d, want 2", got)
	}

	releaseFactoryOnce.Do(func() { close(firstFactoryRelease) })
	if err := <-firstErr; err != nil {
		t.Fatalf("launch first direct spawn: %v", err)
	}
	if result := <-firstResult; result == nil || result.Status != string(subagent.StatusRunning) {
		t.Fatalf("first direct result = %+v, want running", result)
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first direct worker did not start")
	}
	if got := control.Manager().CountRunning(); got != 1 {
		t.Fatalf("running workers while provider is blocked = %d, want 1", got)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("queued worker prepared while direct worker was running: calls=%d", got)
	}

	releaseProviderOnce.Do(func() { close(providerRelease) })
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return client.calls.Load() == 3 &&
			queuedSpawnCountForTest(control) == 0 &&
			control.Manager().CountRunning() == 0
	}, "direct spawn and queued spawn/fork to finish sequentially")
	if got := client.maxActive.Load(); got > 1 {
		t.Fatalf("maximum concurrent provider executions = %d, maxParallel is 1", got)
	}
}

func TestSynchronousSpawnReleasesPreparationReservationAfterManagerRegistration(t *testing.T) {
	rootDir := t.TempDir()
	providerRelease := make(chan struct{})
	client := &queuedSpawnLimitClient{
		release: providerRelease,
		started: make(chan struct{}, 4),
	}
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		filepath.Join(rootDir, "harness"),
		2,
		client,
		nil,
	)
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(providerRelease) }) })

	firstDone := make(chan error, 1)
	go func() {
		_, err := control.Spawn(context.Background(), SpawnRequest{
			Type:        DefaultSubagentType,
			TaskName:    "synchronous_waiter",
			Prompt:      "wait synchronously while running",
			Synchronous: true,
		})
		firstDone <- err
	}()
	waitForChannelForConcurrencyTest(t, client.started, "synchronous worker to start")

	second, err := control.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "remaining_capacity",
		Prompt:   "use the second running slot",
	})
	if err != nil {
		t.Fatalf("spawn second worker: %v", err)
	}
	if second.Status == string(subagent.StatusQueued) {
		t.Fatal("synchronous wait retained its preparation reservation after Manager.Spawn")
	}
	waitForChannelForConcurrencyTest(t, client.started, "second worker to use remaining capacity")
	if got := control.Manager().CountRunning(); got != 2 {
		t.Fatalf("running workers = %d, want 2", got)
	}

	releaseOnce.Do(func() { close(providerRelease) })
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("synchronous spawn completion: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("synchronous spawn did not finish")
	}
}

func TestQueuedSpawnPayloadSurvivesCrashBeforeManagerRegistration(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_crash_before_manager"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())
	gate := holdQueuedWorkerLeaseForConcurrencyTest(t, harnessDir, workerID)

	crashedControl := newQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "crashed-control"),
		harnessDir,
		1,
		&queuedSpawnCountingClient{},
		nil,
	)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return queuedSpawnRetryPendingForTest(crashedControl)
	}, "startup drain to wait on the foreign worker lease")

	hookEntered := make(chan struct{})
	crashNow := make(chan struct{})
	crashedControl.SetQueuedManagerSpawnHookForTest(func(id string) {
		if id != workerID {
			return
		}
		close(hookEntered)
		<-crashNow
		runtime.Goexit()
	})
	if err := gate.release(); err != nil {
		t.Fatalf("release queue launch gate: %v", err)
	}
	waitForChannelForConcurrencyTest(t, hookEntered, "queued launch pre-Manager.Spawn hook")

	exists, err := crashedControl.HarnessStore().QueueItemExists(workerID)
	if err != nil {
		t.Fatalf("inspect durable queue during preparation: %v", err)
	}
	if !exists {
		t.Fatal("durable queued payload was deleted before Manager.Spawn became observable")
	}
	if worker := crashedControl.Manager().Get(workerID); worker != nil {
		t.Fatalf("worker became observable before the crash hook released: %+v", worker.Snapshot())
	}

	close(crashNow)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		active, probeErr := WorkerExecutionActive(harnessDir, workerID)
		return probeErr == nil && !active
	}, "simulated crashed launcher to release its execution lease")
	exists, err = crashedControl.HarnessStore().QueueItemExists(workerID)
	if err != nil || !exists {
		t.Fatalf("durable queue after simulated crash = %v, %v; want true, nil", exists, err)
	}

	recoveryClient := &queuedSpawnCountingClient{}
	recovered := newQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "recovered-control"),
		harnessDir,
		1,
		recoveryClient,
		nil,
	)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		exists, existsErr := recovered.HarnessStore().QueueItemExists(workerID)
		return existsErr == nil && !exists && recoveryClient.calls.Load() == 1
	}, "a new AgentControl to recover and acknowledge the queued launch")
	if worker := recovered.Manager().Get(workerID); worker == nil {
		t.Fatal("recovered queued worker was never registered with Manager")
	}
}

func TestQueuedWorktreeSurvivesCrashBeforeManagerRegistration(t *testing.T) {
	rootDir := t.TempDir()
	initRepo(t, rootDir)
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_worktree_crash_before_manager"
	baseRevision := gitOutputForQueuedSpawnTest(t, rootDir, "rev-parse", "HEAD")
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())
	store := harness.NewStore(harnessDir)
	item, exists, err := store.GetQueueItem(workerID)
	if err != nil || !exists {
		t.Fatalf("load queued worktree payload: exists=%v err=%v", exists, err)
	}
	var payload queuedSpawnPayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatalf("decode queued worktree payload: %v", err)
	}
	payload.Isolation = string(IsolationWorktree)
	payload.BaseRepo = rootDir
	payload.BaseRevision = baseRevision
	item.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode queued worktree payload: %v", err)
	}
	if err := store.UpsertQueueItem(item); err != nil {
		t.Fatalf("persist queued worktree payload: %v", err)
	}

	gate := holdQueuedWorkerLeaseForConcurrencyTest(t, harnessDir, workerID)
	crashedControl := newQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "crashed-control"),
		harnessDir,
		1,
		&queuedSpawnCountingClient{},
		nil,
	)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return queuedSpawnRetryPendingForTest(crashedControl)
	}, "startup worktree drain to wait on the foreign worker lease")

	hookEntered := make(chan struct{})
	crashNow := make(chan struct{})
	crashedControl.SetQueuedManagerSpawnHookForTest(func(id string) {
		if id != workerID {
			return
		}
		close(hookEntered)
		<-crashNow
		runtime.Goexit()
	})
	if err := gate.release(); err != nil {
		t.Fatalf("release queue launch gate: %v", err)
	}
	waitForChannelForConcurrencyTest(t, hookEntered, "queued worktree pre-Manager.Spawn hook")

	worktreePath := filepath.Join(rootDir, ".wuu-test-worktrees", "queued-spawn-concurrency", workerID)
	if got := gitOutputForQueuedSpawnTest(t, worktreePath, "rev-parse", "HEAD"); got != baseRevision {
		t.Fatalf("prelaunch worktree HEAD = %s, want frozen %s", got, baseRevision)
	}
	close(crashNow)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		active, probeErr := WorkerExecutionActive(harnessDir, workerID)
		return probeErr == nil && !active
	}, "simulated crashed worktree launcher to release its execution lease")

	if err := os.WriteFile(filepath.Join(rootDir, "advanced.txt"), []byte("new head\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutputForQueuedSpawnTest(t, rootDir, "add", "advanced.txt")
	gitOutputForQueuedSpawnTest(t, rootDir, "commit", "-q", "-m", "advance parent")
	if advanced := gitOutputForQueuedSpawnTest(t, rootDir, "rev-parse", "HEAD"); advanced == baseRevision {
		t.Fatal("parent HEAD did not advance before queued recovery")
	}

	recoveryClient := &queuedSpawnCountingClient{}
	recovered := newQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "recovered-control"),
		harnessDir,
		1,
		recoveryClient,
		nil,
	)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		exists, existsErr := recovered.HarnessStore().QueueItemExists(workerID)
		return existsErr == nil && !exists && recoveryClient.calls.Load() == 1
	}, "a new AgentControl to reopen and acknowledge the frozen worktree launch")
	if got := recoveryClient.calls.Load(); got != 1 {
		t.Fatalf("recovered provider executions = %d, want exactly 1", got)
	}
	worker := recovered.Manager().Get(workerID)
	if worker == nil {
		t.Fatal("recovered queued worktree worker was never registered with Manager")
	}
	if got := worker.Snapshot().WorkerRoot; got != worktreePath {
		t.Fatalf("recovered worker root = %q, want %q", got, worktreePath)
	}
}

func TestQueuedWorktreeAdmissionPersistsFrozenBaseRevision(t *testing.T) {
	rootDir := t.TempDir()
	initRepo(t, rootDir)
	harnessDir := filepath.Join(rootDir, "harness")
	baseRevision := gitOutputForQueuedSpawnTest(t, rootDir, "rev-parse", "HEAD")
	releaseProvider := make(chan struct{})
	client := newQueuedSpawnLimitClient(releaseProvider)
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		harnessDir,
		1,
		client,
		nil,
	)
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseProvider) }) })

	first, err := control.Spawn(context.Background(), SpawnRequest{
		Type:      DefaultSubagentType,
		TaskName:  "occupy_capacity",
		Prompt:    "hold the only running slot",
		Isolation: string(IsolationInplace),
	})
	if err != nil {
		t.Fatalf("spawn capacity holder: %v", err)
	}
	if first.Status == string(subagent.StatusQueued) {
		t.Fatal("capacity holder unexpectedly queued")
	}
	waitForChannelForConcurrencyTest(t, client.started, "capacity holder to start")
	control.StartQueuedWork()
	canonicalRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		t.Fatalf("canonicalize repository root: %v", err)
	}

	queuedSpawn, err := control.Spawn(context.Background(), SpawnRequest{
		Type:      DefaultSubagentType,
		TaskName:  "frozen_spawn",
		Prompt:    "run from the admitted revision",
		Isolation: string(IsolationWorktree),
	})
	if err != nil {
		t.Fatalf("queue worktree spawn: %v", err)
	}
	queuedFork, err := control.Fork(context.Background(), ForkRequest{
		TaskName:  "frozen_fork",
		Prompt:    "fork from the admitted revision",
		Isolation: string(IsolationWorktree),
	}, []providers.ChatMessage{{Role: "user", Content: "parent history"}})
	if err != nil {
		t.Fatalf("queue worktree fork: %v", err)
	}
	for _, result := range []*SpawnResult{queuedSpawn, queuedFork} {
		if result.Status != string(subagent.StatusQueued) {
			t.Fatalf("worktree admission %s status = %q, want queued", result.AgentID, result.Status)
		}
		item, exists, err := control.HarnessStore().GetQueueItem(result.AgentID)
		if err != nil || !exists {
			t.Fatalf("load queued worktree %s: exists=%v err=%v", result.AgentID, exists, err)
		}
		var payload queuedSpawnPayload
		if err := json.Unmarshal(item.Payload, &payload); err != nil {
			t.Fatalf("decode queued worktree %s: %v", result.AgentID, err)
		}
		if payload.BaseRevision != baseRevision {
			t.Fatalf("queued worktree %s revision = %q, want %q", result.AgentID, payload.BaseRevision, baseRevision)
		}
		if payload.BaseRepo != canonicalRoot {
			t.Fatalf("queued worktree %s base repo = %q, want %q", result.AgentID, payload.BaseRepo, canonicalRoot)
		}
	}
}

func gitOutputForQueuedSpawnTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestBeginShutdownRacingQueuedStartCannotLaunchWorker(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_shutdown_queue_race"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())

	client := &queuedSpawnCountingClient{}
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		harnessDir,
		1,
		client,
		nil,
	)
	spawnCommitEntered := make(chan struct{})
	releaseSpawnCommit := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseSpawnCommit) }) })
	control.SetQueuedManagerSpawnHookForTest(func(id string) {
		if id != workerID {
			return
		}
		close(spawnCommitEntered)
		<-releaseSpawnCommit
	})

	control.StartQueuedWork()
	waitForChannelForConcurrencyTest(t, spawnCommitEntered, "queued launch commit point")

	shutdownDone := make(chan struct{})
	go func() {
		control.BeginShutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("BeginShutdown deadlocked with StartQueuedWork")
	}
	releaseOnce.Do(func() { close(releaseSpawnCommit) })
	drainDone := make(chan struct{})
	go func() {
		control.queueDrainMu.Lock()
		control.queueDrainMu.Unlock()
		close(drainDone)
	}()
	waitForChannelForConcurrencyTest(t, drainDone, "queued launch to observe shutdown")
	if worker := control.Manager().Get(workerID); worker != nil {
		t.Fatalf("worker launched after shutdown admission closed: %+v", worker.Snapshot())
	}
	if got := client.calls.Load(); got != 0 {
		t.Fatalf("provider calls after shutdown = %d, want 0", got)
	}
	if exists, err := control.HarnessStore().QueueItemExists(workerID); err != nil || !exists {
		t.Fatalf("durable queue after shutdown race = %v, %v; want true, nil", exists, err)
	}
	if got := queuedSpawnCountForTest(control); got != 1 {
		t.Fatalf("in-memory queue after shutdown race = %d, want 1", got)
	}
	control.StopAll()
	if exists, err := control.HarnessStore().QueueItemExists(workerID); err != nil || exists {
		t.Fatalf("durable queue after StopAll cancellation = %v, %v; want false, nil", exists, err)
	}
}

func TestQueuedSpawnClaimExecutesRecoveredPayloadOnceAcrossAgentControls(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_recovered_once"

	persistQueuedSpawnForConcurrencyTest(t, harnessDir, workerID, time.Now().UTC())
	gate := holdQueuedWorkerLeaseForConcurrencyTest(t, harnessDir, workerID)
	client := &queuedSpawnCountingClient{}
	controlA := newQueuedSpawnConcurrencyControl(t, rootDir, filepath.Join(rootDir, "control-a"), harnessDir, 1, client, nil)
	controlB := newQueuedSpawnConcurrencyControl(t, rootDir, filepath.Join(rootDir, "control-b"), harnessDir, 1, client, nil)

	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return queuedSpawnRetryPendingForTest(controlA) && queuedSpawnRetryPendingForTest(controlB)
	}, "both recovered queue copies to observe the foreign worker lease")
	if got := queuedSpawnCountForTest(controlA) + queuedSpawnCountForTest(controlB); got != 2 {
		t.Fatalf("recovered in-memory queue copies = %d, want 2", got)
	}
	if err := gate.release(); err != nil {
		t.Fatalf("release queue launch gate: %v", err)
	}

	start := make(chan struct{})
	var drains sync.WaitGroup
	for _, control := range []*AgentControl{controlA, controlB} {
		drains.Add(1)
		go func(control *AgentControl) {
			defer drains.Done()
			<-start
			control.maybeStartQueued(context.Background())
		}(control)
	}
	close(start)
	drains.Wait()

	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return client.calls.Load() == 1 &&
			queuedSpawnCountForTest(controlA) == 0 &&
			queuedSpawnCountForTest(controlB) == 0 &&
			controlA.Manager().CountRunning() == 0 &&
			controlB.Manager().CountRunning() == 0
	}, "one launch and both recovered queue copies to settle")
	// Let any lease-busy retry that was already scheduled run once more. It
	// must observe the missing durable item and drop its local copy.
	time.Sleep(2 * queuedSpawnLeaseRetryDelay)
	if got := client.calls.Load(); got != 1 {
		t.Fatalf("provider executions = %d, want exactly 1", got)
	}
	items, err := harness.NewStore(harnessDir).ListQueueItems()
	if err != nil {
		t.Fatalf("list durable queue: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("claimed durable queue item remained: %+v", items)
	}
}

func TestConcurrentQueuedDrainsRespectMaxParallel(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	now := time.Now().UTC()
	const (
		firstWorkerID  = "worker_parallel_first"
		secondWorkerID = "worker_parallel_second"
	)
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, firstWorkerID, now)
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, secondWorkerID, now.Add(time.Millisecond))

	providerRelease := make(chan struct{})
	client := newQueuedSpawnLimitClient(providerRelease)
	firstFactoryEntered := make(chan struct{})
	secondFactoryEntered := make(chan struct{}, 1)
	factoryRelease := make(chan struct{})
	var factoryCalls atomic.Int32
	workerFactory := func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
		switch factoryCalls.Add(1) {
		case 1:
			close(firstFactoryEntered)
			<-factoryRelease
		case 2:
			secondFactoryEntered <- struct{}{}
		}
		return fakeToolkit{}, nil
	}
	control := newQueuedSpawnConcurrencyControl(t, rootDir, filepath.Join(rootDir, "control"), harnessDir, 1, client, workerFactory)
	var releaseFactoryOnce sync.Once
	var releaseProviderOnce sync.Once
	t.Cleanup(func() {
		releaseFactoryOnce.Do(func() { close(factoryRelease) })
		releaseProviderOnce.Do(func() { close(providerRelease) })
	})

	select {
	case <-firstFactoryEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup queued drain did not reach worker preparation")
	}
	if got := queuedSpawnCountForTest(control); got != 1 {
		t.Fatalf("queued spawns while the first worker is preparing = %d, want 1", got)
	}
	secondDrainStarted := make(chan struct{})
	secondDrainDone := make(chan struct{})
	go func() {
		close(secondDrainStarted)
		defer close(secondDrainDone)
		control.maybeStartQueued(context.Background())
	}()
	<-secondDrainStarted

	releaseFactoryOnce.Do(func() { close(factoryRelease) })
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first queued worker did not start")
	}
	waitForChannelForConcurrencyTest(t, secondDrainDone, "second queued drain")
	select {
	case <-secondFactoryEntered:
		t.Fatal("a concurrent drain prepared a second worker before the first launch occupied capacity")
	default:
	}
	if got := control.Manager().CountRunning(); got != 1 {
		t.Fatalf("running workers = %d, want 1", got)
	}
	if got := queuedSpawnCountForTest(control); got != 1 {
		t.Fatalf("queued workers while capacity is full = %d, want 1", got)
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("worker factory calls while capacity is full = %d, want 1", got)
	}

	releaseProviderOnce.Do(func() { close(providerRelease) })
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		return client.calls.Load() == 2 &&
			queuedSpawnCountForTest(control) == 0 &&
			control.Manager().CountRunning() == 0 &&
			!queuedSpawnRetryPendingForTest(control)
	}, "both queued workers to finish sequentially")
	control.queueDrainMu.Lock()
	control.queueDrainMu.Unlock()
	if got := client.maxActive.Load(); got > 1 {
		t.Fatalf("maximum concurrent provider executions = %d, maxParallel is 1", got)
	}
}

type queuedSpawnCountingClient struct {
	calls atomic.Int32
}

func (c *queuedSpawnCountingClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	c.calls.Add(1)
	return providers.ChatResponse{Content: "done"}, nil
}

func (c *queuedSpawnCountingClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.calls.Add(1)
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
	events <- providers.StreamEvent{Type: providers.EventDone}
	close(events)
	return events, nil
}

type queuedSpawnLimitClient struct {
	release   <-chan struct{}
	started   chan struct{}
	calls     atomic.Int32
	active    atomic.Int32
	maxActive atomic.Int32
}

func newQueuedSpawnLimitClient(release <-chan struct{}) *queuedSpawnLimitClient {
	return &queuedSpawnLimitClient{release: release, started: make(chan struct{}, 2)}
}

func (c *queuedSpawnLimitClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	c.beginRequest()
	defer c.active.Add(-1)
	select {
	case <-c.release:
		return providers.ChatResponse{Content: "done"}, nil
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

func (c *queuedSpawnLimitClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.beginRequest()
	events := make(chan providers.StreamEvent, 2)
	go func() {
		defer close(events)
		select {
		case <-c.release:
			events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
			events <- providers.StreamEvent{Type: providers.EventDone}
		case <-ctx.Done():
			events <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}
		c.active.Add(-1)
	}()
	return events, nil
}

func (c *queuedSpawnLimitClient) beginRequest() {
	c.calls.Add(1)
	active := c.active.Add(1)
	for {
		observed := c.maxActive.Load()
		if active <= observed || c.maxActive.CompareAndSwap(observed, active) {
			break
		}
	}
	c.started <- struct{}{}
}

func newQueuedSpawnConcurrencyControl(
	t *testing.T,
	rootDir string,
	stateDir string,
	harnessDir string,
	maxParallel int,
	client providers.StreamClient,
	workerFactory WorkerToolkitFactory,
) *AgentControl {
	control := newDormantQueuedSpawnConcurrencyControl(t, rootDir, stateDir, harnessDir, maxParallel, client, workerFactory)
	control.StartQueuedWork()
	return control
}

func newDormantQueuedSpawnConcurrencyControl(
	t *testing.T,
	rootDir string,
	stateDir string,
	harnessDir string,
	maxParallel int,
	client providers.StreamClient,
	workerFactory WorkerToolkitFactory,
) *AgentControl {
	t.Helper()
	if workerFactory == nil {
		workerFactory = func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return fakeToolkit{}, nil
		}
	}
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:        client,
		DefaultModel:  "queued-spawn-test-model",
		ParentRepo:    rootDir,
		WorktreeRoot:  filepath.Join(rootDir, ".wuu-test-worktrees"),
		SessionID:     "queued-spawn-concurrency",
		HistoryDir:    filepath.Join(stateDir, "workers"),
		ThreadDir:     filepath.Join(stateDir, "threads"),
		HarnessDir:    harnessDir,
		MaxParallel:   maxParallel,
		WorkerFactory: workerFactory,
	})
	if err != nil {
		t.Fatalf("create AgentControl: %v", err)
	}
	t.Cleanup(func() {
		stopAndCloseAgentControlForTest(t, control)
	})
	return control
}

func persistQueuedSpawnForConcurrencyTest(t *testing.T, harnessDir, workerID string, createdAt time.Time) {
	persistQueuedSpawnOfTypeForConcurrencyTest(t, harnessDir, workerID, DefaultSubagentType, createdAt)
}

func persistQueuedSpawnOfTypeForConcurrencyTest(t *testing.T, harnessDir, workerID, workerTypeName string, createdAt time.Time) {
	t.Helper()
	workerType, err := LookupWorkerType(workerTypeName)
	if err != nil {
		t.Fatalf("lookup default worker type: %v", err)
	}
	meta := agentthread.Metadata{
		ID:        workerID,
		SessionID: "queued-spawn-concurrency",
		ParentID:  "queued-spawn-concurrency",
		Path:      agentthread.RootPath + "/" + workerID,
		TaskName:  workerID,
		Role:      workerType.Name,
		Status:    agentthread.StatusPending,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Source: agentthread.Source{
			Kind:           agentthread.SourceThreadSpawn,
			ParentThreadID: "queued-spawn-concurrency",
			ParentPath:     agentthread.RootPath,
			EdgeStatus:     agentthread.EdgeOpen,
		},
	}
	prepared := preparedSpawn{
		WorkerID:      workerID,
		ParticipantID: "participant-" + workerID,
		WorkerType:    workerType,
		ThreadMeta:    meta,
		Prompt:        "run " + workerID,
		Isolation:     IsolationInplace,
	}
	payload, err := json.Marshal(queuedSpawnPayloadFromPrepared(prepared))
	if err != nil {
		t.Fatalf("marshal queued spawn: %v", err)
	}
	if err := harness.NewStore(harnessDir).UpsertQueueItem(harness.QueueItem{
		ID:        workerID,
		TaskID:    workerID,
		Kind:      "agent_spawn",
		Payload:   payload,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("persist queued spawn: %v", err)
	}
}

func holdQueuedWorkerLeaseForConcurrencyTest(t *testing.T, harnessDir, workerID string) *workerExecutionLease {
	t.Helper()
	lease, acquired, err := tryAcquireWorkerExecutionLease(harnessDir, workerID)
	if err != nil {
		t.Fatalf("acquire queue launch gate: %v", err)
	}
	if !acquired {
		t.Fatal("queue launch gate was already owned")
	}
	t.Cleanup(func() { _ = lease.release() })
	return lease
}

func queuedSpawnCountForTest(control *AgentControl) int {
	control.queueMu.Lock()
	defer control.queueMu.Unlock()
	return len(control.queued)
}

func queuedSpawnRetryPendingForTest(control *AgentControl) bool {
	control.queueRetryMu.Lock()
	defer control.queueRetryMu.Unlock()
	return control.queueRetrying
}

func waitForQueuedSpawnConcurrencyTest(t *testing.T, condition func() bool, description string) {
	t.Helper()
	waitForQueuedSpawnConcurrencyTestUntil(t, 3*time.Second, condition, description)
}

const queuedSpawnRecoveryTimeoutForTest = time.Duration(queuedSpawnStoreRetryAttempts)*queuedSpawnAckRetryDelay + 2*time.Second

func waitForQueuedSpawnRecoveryTest(t *testing.T, condition func() bool, description string) {
	t.Helper()
	waitForQueuedSpawnConcurrencyTestUntil(t, queuedSpawnRecoveryTimeoutForTest, condition, description)
}

func waitForQueuedSpawnConcurrencyTestUntil(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func waitForChannelForConcurrencyTest(t *testing.T, done <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
