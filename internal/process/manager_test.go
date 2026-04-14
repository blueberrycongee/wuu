package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartListAndPersist(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Start(context.Background(), StartOptions{Command: "echo hello; sleep 1", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerKind != OwnerMainAgent || p.Lifecycle != LifecycleManaged || p.Status != StatusRunning {
		t.Fatalf("unexpected process: %+v", p)
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 process, got %d", len(list))
	}
	if _, err := os.Stat(filepath.Join(root, ".wuu", "runtime", "processes", p.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestStartDetachesProcessLifecycleFromStartContext(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := m.Start(ctx, StartOptions{Command: "sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(200 * time.Millisecond)

	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	var status Status
	for _, proc := range list {
		if proc.ID == p.ID {
			status = proc.Status
			break
		}
	}
	if status != StatusRunning {
		t.Fatalf("expected process to keep running after start context cancel, got %s", status)
	}

	stopped, err := m.Stop(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped == nil || (stopped.Status != StatusStopped && stopped.Status != StatusFailed) {
		t.Fatalf("expected manager stop to end process, got %+v", stopped)
	}
}

func TestStopStopsProcessGroup(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root)
	p, err := m.Start(context.Background(), StartOptions{Command: "sleep 30 & wait", OwnerKind: OwnerSubagent, OwnerID: "worker-1", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Stop(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	proc, _ := os.FindProcess(p.PID)
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		// allow a brief settle, then fail if still alive
		time.Sleep(200 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			t.Fatal("process still alive after stop")
		}
	}
}

func TestCleanupSessionOnlyStopsSessionLifecycle(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root)
	sessionProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	managedProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupSession(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	list, _ := m.List()
	var gotSession, gotManaged Status
	for _, p := range list {
		if p.ID == sessionProc.ID {
			gotSession = p.Status
		}
		if p.ID == managedProc.ID {
			gotManaged = p.Status
		}
	}
	if gotSession != StatusStopped && gotSession != StatusFailed {
		t.Fatalf("session process not stopped: %s", gotSession)
	}
	if gotManaged != StatusRunning {
		t.Fatalf("managed process should keep running, got %s", gotManaged)
	}
	_, _ = m.Stop(managedProc.ID)
}

func TestReadOutput(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root)
	p, err := m.Start(context.Background(), StartOptions{Command: "echo ready; sleep 1", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	out, _, err := m.ReadOutput(p.ID, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ready") {
		t.Fatalf("unexpected output: %q", out)
	}
	_, _ = m.Stop(p.ID)
}

func TestManagerPublishesLifecycleEventsAndCleanupSkipsManaged(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	m.Subscribe(events)

	sessionProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 5", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	managedProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 5", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}

	if got := (<-events); got.Type != EventStarted || got.Process.ID != sessionProc.ID {
		t.Fatalf("unexpected first event: %+v", got)
	}
	if got := (<-events); got.Type != EventStarted || got.Process.ID != managedProc.ID {
		t.Fatalf("unexpected second event: %+v", got)
	}

	result, err := m.CleanupSessionWithResult()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cleaned) != 1 || result.Cleaned[0].ID != sessionProc.ID {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}

	gotCleanup := false
	gotStopped := false
	deadline := time.After(5 * time.Second)
	for !(gotCleanup && gotStopped) {
		select {
		case ev := <-events:
			switch {
			case ev.Type == EventCleanedUp && ev.Process.ID == sessionProc.ID:
				gotCleanup = true
			case ev.Type == EventStopped && ev.Process.ID == sessionProc.ID:
				gotStopped = true
			case ev.Process.ID == managedProc.ID && ev.Type == EventCleanedUp:
				t.Fatalf("managed process should not be cleaned up: %+v", ev)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for cleanup events; cleanup=%v stopped=%v", gotCleanup, gotStopped)
		}
	}

	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]Status{}
	for _, proc := range list {
		statuses[proc.ID] = proc.Status
	}
	if statuses[sessionProc.ID] != StatusStopped {
		t.Fatalf("expected session process stopped, got %s", statuses[sessionProc.ID])
	}
	if statuses[managedProc.ID] != StatusRunning {
		t.Fatalf("expected managed process still running, got %s", statuses[managedProc.ID])
	}
	_, _ = m.Stop(managedProc.ID)
}
