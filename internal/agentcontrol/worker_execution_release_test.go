package agentcontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Exercise the exact interval between releasing the OS lock and removing the
// owner's map entry without relying on goroutine scheduling.
func TestWaitOwnedWorkerExecutionsWaitsForOwnerRemoval(t *testing.T) {
	control := &AgentControl{harnessDir: t.TempDir()}
	acquired, err := control.acquireWorkerExecution("worker")
	if err != nil || !acquired {
		t.Fatalf("acquire worker: acquired=%v err=%v", acquired, err)
	}
	control.workerLeaseMu.Lock()
	lease := control.workerLeases["worker"]
	control.workerLeaseMu.Unlock()
	defer control.releaseWorkerExecution("worker")
	if err := lease.release(); err != nil {
		t.Fatal(err)
	}
	probe, ok, err := tryAcquireWorkerExecutionLease(control.harnessDir, "worker")
	if err != nil || !ok {
		t.Fatalf("physical lock not released: acquired=%v err=%v", ok, err)
	}
	if err := probe.release(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := control.WaitOwnedWorkerExecutions(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait returned before owner map removal: %v", err)
	}
	control.releaseWorkerExecution("worker")
	if err := control.WaitOwnedWorkerExecutions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if control.HasOwnedWorkerExecutions() {
		t.Fatal("released owner remains visible")
	}
	acquired, err = control.acquireWorkerExecution("worker")
	if err != nil || !acquired {
		t.Fatalf("reacquire worker: acquired=%v err=%v", acquired, err)
	}
	if !control.HasOwnedWorkerExecutions() {
		t.Fatal("a new owner was hidden by the previous release")
	}
}

func TestWaitOwnedWorkerExecutionsCancelsWhileReleaseIsBlocked(t *testing.T) {
	control := &AgentControl{harnessDir: t.TempDir()}
	acquired, err := control.acquireWorkerExecution("worker")
	if err != nil || !acquired {
		t.Fatalf("acquire worker: acquired=%v err=%v", acquired, err)
	}
	unblock := control.lockWorkerExecutionRelease("worker")
	done := make(chan struct{})
	go func() { control.releaseWorkerExecution("worker"); close(done) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := control.WaitOwnedWorkerExecutions(ctx); !errors.Is(err, context.Canceled) {
		unblock()
		t.Fatalf("waiting for a blocked release ignored cancellation: %v", err)
	}
	unblock()
	<-done
	if err := control.WaitOwnedWorkerExecutions(context.Background()); err != nil {
		t.Fatal(err)
	}
}
