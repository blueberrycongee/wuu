package agentcontrol

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestRecoveredQueuedRequiresReportClosingTurnRetainsExecutionLease(t *testing.T) {
	rootDir := t.TempDir()
	harnessDir := filepath.Join(rootDir, "harness")
	const workerID = "worker_recovered_requires_report_closing"
	persistQueuedSpawnOfTypeForConcurrencyTest(t, harnessDir, workerID, requiresReportWorkerType, time.Now().UTC())

	client := newClosingTurnBlockingClient()
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		harnessDir,
		1,
		client,
		nil,
	)
	var releaseClosingOnce sync.Once
	releaseClosing := func() { releaseClosingOnce.Do(func() { close(client.release) }) }
	t.Cleanup(releaseClosing)

	// The zero-latency first generation finishes while its durable queue
	// acknowledgement owns the release barrier. Recovery then consumes that
	// terminal record and starts the requires_report closing generation.
	control.StartWorkerTerminalRecovery()
	control.StartQueuedWork()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("recovered queued worker did not start its report-closing turn")
	}
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		control.workerTerminalRecoveryMu.Lock()
		_, recovering := control.workerTerminalRecovering[workerID]
		control.workerTerminalRecoveryMu.Unlock()
		return !recovering
	}, "terminal recovery to hand ownership to the closing generation")

	worker := control.Manager().Get(workerID)
	if worker == nil || worker.Snapshot().Status != subagent.StatusRunning {
		t.Fatalf("closing generation = %+v, want running", worker)
	}
	if !control.workerExecutionOwned(workerID) {
		t.Fatal("terminal recovery released the execution lease while the closing turn was running")
	}
	if pending, err := control.workerTerminalFinalizationPending(workerID); err != nil || !pending {
		t.Fatalf("closing generation terminal authority = %t, %v; want true, nil", pending, err)
	}

	releaseClosing()
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		worker := control.Manager().Get(workerID)
		if worker == nil || worker.Snapshot().Status != subagent.StatusCompleted || control.workerExecutionOwned(workerID) {
			return false
		}
		pending, err := control.workerTerminalFinalizationPending(workerID)
		return err == nil && !pending
	}, "closing generation to finalize and release execution ownership")
}

func TestYieldWorkerTerminalFinalizationsJoinsAdmittedRecoveryBeforeFinalizerRetires(t *testing.T) {
	rootDir := t.TempDir()
	control := newDormantQueuedSpawnConcurrencyControl(
		t,
		rootDir,
		filepath.Join(rootDir, "control"),
		filepath.Join(rootDir, "harness"),
		1,
		&queuedSpawnCountingClient{},
		nil,
	)
	const workerID = "worker_recovery_shutdown_join"
	notification := subagent.Notification{
		AgentID: workerID,
		Status:  subagent.StatusCompleted,
		Snapshot: subagent.SubAgentSnapshot{
			ID:          workerID,
			Status:      subagent.StatusCompleted,
			Result:      "done before shutdown",
			StartedAt:   time.Now().Add(-time.Second).UTC(),
			CompletedAt: time.Now().UTC(),
		},
	}
	if err := control.persistWorkerTerminalFinalization(notification); err != nil {
		t.Fatalf("persist terminal finalization: %v", err)
	}

	recoveryAcquired := make(chan struct{})
	releaseRecovery := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseRecovery) }) })
	control.workerReleaseHookMu.Lock()
	control.beforeWorkerTerminalRecoveryForTest = func(id string) {
		if id != workerID {
			return
		}
		close(recoveryAcquired)
		<-releaseRecovery
	}
	control.workerReleaseHookMu.Unlock()
	var finalizerCalls atomic.Int32
	unsubscribe := control.SubscribeWorkerTerminalFinalizer(func(subagent.Notification) error {
		finalizerCalls.Add(1)
		return errors.New("finalizer must not run after durable yield")
	})
	defer unsubscribe()

	control.startPendingWorkerTerminalRecovery(newWorkerTerminalFinalizationRecord(notification))
	select {
	case <-recoveryAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal recovery did not acquire execution ownership")
	}

	yielded := make(chan struct{})
	go func() {
		control.YieldWorkerTerminalFinalizations()
		close(yielded)
	}()
	select {
	case <-yielded:
		t.Fatal("durable yield returned before its admitted recovery goroutine exited")
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseRecovery) })
	select {
	case <-yielded:
	case <-time.After(2 * time.Second):
		t.Fatal("durable yield did not join terminal recovery")
	}

	if got := finalizerCalls.Load(); got != 0 {
		t.Fatalf("external finalizer calls after yield = %d, want 0", got)
	}
	if control.workerExecutionOwned(workerID) {
		t.Fatal("yielded recovery retained its physical execution lease")
	}
	if pending, err := control.workerTerminalFinalizationPending(workerID); err != nil || !pending {
		t.Fatalf("yielded durable terminal intent = %t, %v; want true, nil", pending, err)
	}
}
