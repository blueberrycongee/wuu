package subagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestTerminalPrepareRetriesRetainGeneration(t *testing.T) {
	mgr := NewManager(&fakeClient{response: providers.ChatResponse{Content: "done"}}, "model", agent.DefaultDriver{})
	var generations []string
	mgr.SetTerminalPrepareObserver(func(n Notification) error {
		generations = append(generations, n.TerminalGeneration)
		if len(generations) < 3 {
			return errors.New("retry")
		}
		return nil
	})
	terminal := make(chan Notification, 1)
	mgr.SetTerminalObserver(func(n Notification) error { terminal <- n; return nil })
	worker, err := mgr.Spawn(context.Background(), SpawnOptions{Type: "worker", Prompt: "finish", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Wait(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	n := <-terminal
	if len(generations) != 3 || generations[0] == "" || generations[0] != generations[1] || generations[0] != generations[2] || n.TerminalGeneration != generations[0] || !n.TerminalPrepared {
		t.Fatalf("prepare=%v terminal=%+v", generations, n)
	}
}

func TestTerminalSnapshotBroadcastDoesNotInvokeTerminalObserver(t *testing.T) {
	mgr := NewManager(&fakeClient{}, "model", agent.DefaultDriver{})
	var called atomic.Int32
	mgr.SetTerminalObserver(func(Notification) error { called.Add(1); return nil })
	ch := make(chan Notification, 1)
	mgr.Subscribe(ch)
	worker := &SubAgent{ID: "late_usage", Status: StatusCompleted}
	mgr.BroadcastSnapshot(worker)
	if called.Load() != 0 {
		t.Fatal("late usage snapshot invoked terminal observer")
	}
	select {
	case n := <-ch:
		if n.Status != StatusCompleted || !n.SnapshotOnly {
			t.Fatalf("terminal snapshot = %+v", n)
		}
	default:
		t.Fatal("ordinary listener did not receive terminal snapshot")
	}
}

func TestTerminalNotificationUsesPreparedSnapshotDespiteLateUsage(t *testing.T) {
	mgr := NewManager(&fakeClient{response: providers.ChatResponse{Content: "done"}}, "model", agent.DefaultDriver{})
	entered := make(chan Notification, 1)
	release := make(chan struct{})
	terminal := make(chan Notification, 1)
	mgr.SetTerminalPrepareObserver(func(n Notification) error { entered <- n; <-release; return nil })
	mgr.SetTerminalObserver(func(n Notification) error { terminal <- n; return nil })
	worker, err := mgr.Spawn(context.Background(), SpawnOptions{Type: "worker", Prompt: "finish", Toolkit: fakeToolkit{}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := <-entered
	worker.mu.Lock()
	worker.InputTokens = prepared.Snapshot.InputTokens + 99
	worker.mu.Unlock()
	close(release)
	if _, err := mgr.Wait(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	notified := <-terminal
	if notified.TerminalGeneration != prepared.TerminalGeneration || notified.Snapshot.InputTokens != prepared.Snapshot.InputTokens {
		t.Fatalf("prepared=%+v notified=%+v", prepared, notified)
	}
}
