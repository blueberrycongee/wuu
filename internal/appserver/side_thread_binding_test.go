package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
)

// A delayed execution must finish against its captured state even after the
// request-time state template changes. Channels hold the provider at the exact
// publication boundary; no timing assumption controls which store receives it.
func TestSideThreadTurnKeepsCapturedDocuments(t *testing.T) {
	for _, action := range []string{"finish", "interrupt", "reset", "close"} {
		t.Run(action, func(t *testing.T) {
			client := &sideThreadTestClient{response: "old authority result", started: make(chan struct{}), release: make(chan struct{})}
			s, rt, _ := newSideThreadServer(t, client)
			createSideThreadMain(t, s, "captured-main")
			captured := testRuntimeState(t, rt)
			oldStore := sidethread.NewStore(context.Background(), captured.Documents)
			if _, err := s.sendSideThreadMessage(captured, "captured-main", "old request"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider did not start")
			}
			nextDocs, err := persistence.Open(context.Background(), t.TempDir(), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = nextDocs.Close() })
			next := &runtime.State{Documents: nextDocs, Sessions: session.NewStore(context.Background(), nextDocs)}
			if _, err := next.Sessions.CreateWithMetadata("captured-main", rt.RootDir); err != nil {
				t.Fatal(err)
			}
			nextStore := sidethread.NewStore(context.Background(), next.Documents)
			if _, err := nextStore.BeginTurn("captured-main", "next authority request", "next-user", "next-assistant"); err != nil {
				t.Fatal(err)
			}
			rt.Documents, rt.Sessions = next.Documents, next.Sessions
			switch action {
			case "interrupt":
				if _, err := s.interruptSideThread(next, "captured-main"); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if _, err := s.resetSideThread(next, "captured-main"); err != nil {
					t.Fatal(err)
				}
			case "close":
				s.cancelSideThreads()
			}
			close(client.release)
			s.backgroundWG.Wait()
			current, err := nextStore.Load("captured-main")
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != sidethread.StatusRunning || current.Messages[1].Text != "" {
				t.Fatalf("old turn crossed into next state: %+v", current)
			}
			old, err := oldStore.Load("captured-main")
			if action == "reset" {
				if err != sidethread.ErrNotFound {
					t.Fatalf("old reset: %+v %v", old, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := sidethread.StatusCompleted
			if action != "finish" {
				expected = sidethread.StatusInterrupted
			}
			if old.Status != expected {
				t.Fatalf("old state status=%s want %s", old.Status, expected)
			}
			if action == "finish" && old.Messages[1].Text != "old authority result" {
				t.Fatalf("old reply missing: %+v", old)
			}
		})
	}
}
