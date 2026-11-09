package subagent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// wakeProbeToolkit is a fakeToolkit with pointer identity so tests can assert
// the wake-authority hook received the exact executor the worker runs with.
type wakeProbeToolkit struct{ fakeToolkit }

// TestFollowup_WakeRefreshesAuthority locks the wake-admission chokepoint:
// waking a dormant worker is a new execution admission, so the wake-authority
// hook must run on the worker's executor before the resumed turn starts —
// and must NOT run for a message queued into a still-running turn or for the
// immediate continuation that drains it, both of which keep the admitted
// snapshot of an execution that never settled.
func TestFollowup_WakeRefreshesAuthority(t *testing.T) {
	client := &terminalBoundaryClient{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
		secondSeen:   make(chan providers.ChatRequest, 1),
	}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})
	kit := &wakeProbeToolkit{}
	var refreshed atomic.Int32
	var refreshedWith atomic.Value
	mgr.SetWakeAuthority(func(executor agent.ToolExecutor) {
		refreshed.Add(1)
		refreshedWith.Store(executor)
	})

	sa, err := mgr.Spawn(context.Background(), SpawnOptions{
		Type:    "worker",
		Prompt:  "initial task",
		Toolkit: kit,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-client.firstStarted
	if refreshed.Load() != 0 {
		t.Fatal("spawn must not trigger the wake-authority hook: spawn admission builds a fresh executor")
	}

	// Queue into the RUNNING worker: no wake, no refresh.
	if _, err := mgr.Followup(context.Background(), sa.ID, "queued while running"); err != nil {
		t.Fatalf("Followup (running): %v", err)
	}
	if refreshed.Load() != 0 {
		t.Fatal("queueing into a running worker must not refresh its authority")
	}

	// The queued message drains into an immediate continuation turn of the
	// same, never-settled execution — still no refresh.
	close(client.releaseFirst)
	<-client.secondSeen
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if refreshed.Load() != 0 {
		t.Fatal("a continuation of an unsettled run must keep its admitted authority")
	}

	// Waking the completed worker IS an execution admission. The hook runs
	// synchronously before the resumed turn is started.
	if _, err := mgr.Followup(context.Background(), sa.ID, "wake up"); err != nil {
		t.Fatalf("Followup (wake): %v", err)
	}
	if got := refreshed.Load(); got != 1 {
		t.Fatalf("wake-authority refreshes after wake = %d, want 1", got)
	}
	if got, _ := refreshedWith.Load().(*wakeProbeToolkit); got != kit {
		t.Fatalf("wake-authority hook received %p, want the worker's executor %p", got, kit)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait (wake): %v", err)
	}
}

// TestRestoreThenFollowup_WakeRefreshesAuthority covers the restart shape of
// the same invariant: a worker rebuilt from its persisted snapshot and then
// woken by a follow-up gets its authority refreshed at wake time. Restore
// itself is registration, not admission — only the wake refreshes.
func TestRestoreThenFollowup_WakeRefreshesAuthority(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "resumed"}}
	mgr := NewManager(client, "fake-model", agent.DefaultDriver{})
	kit := &wakeProbeToolkit{}
	var refreshed atomic.Int32
	var refreshedWith atomic.Value
	mgr.SetWakeAuthority(func(executor agent.ToolExecutor) {
		refreshed.Add(1)
		refreshedWith.Store(executor)
	})

	sa, err := mgr.Restore(RestoreOptions{
		Run: PersistedRun{
			Version: ResumeSnapshotVersion,
			ID:      "wk-wake-restored",
			Type:    "general-purpose",
			Status:  StatusCompleted,
			Model:   "fake-model",
			Messages: []providers.ChatMessage{
				{Role: "user", Content: "original task"},
				{Role: "assistant", Content: "done"},
			},
		},
		Toolkit: kit,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if refreshed.Load() != 0 {
		t.Fatal("restore alone must not refresh authority: it registers a dormant worker")
	}

	if _, err := mgr.Followup(context.Background(), sa.ID, "wake after restart"); err != nil {
		t.Fatalf("Followup: %v", err)
	}
	if got := refreshed.Load(); got != 1 {
		t.Fatalf("wake-authority refreshes after restore+wake = %d, want 1", got)
	}
	if got, _ := refreshedWith.Load().(*wakeProbeToolkit); got != kit {
		t.Fatalf("wake-authority hook received %p, want the restored executor %p", got, kit)
	}
	if _, err := mgr.Wait(context.Background(), sa.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}
