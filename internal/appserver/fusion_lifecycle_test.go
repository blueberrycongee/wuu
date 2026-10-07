package appserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/sidethread"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// Lifecycle failure cases: archiving splits the pair, restoring replaces Sidekick,
// deleting leaks its history/resources, a rejected delete removes half the pair,
// and another host starts Sidekick between an idle probe and the durable mutation.
func TestFusionArchiveRestoreAndDelete(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			srv, client, calls := newFusionFixture(t)
			var created ThreadStartResult
			client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
			id := created.Thread.ID
			client.rpc(t, MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "Investigate"}, nil)
			calls.next(t).response <- fusionTool("task", `{"message":"Investigate and report"}`)
			calls.next(t).response <- fusionReply("Persistent Sidekick evidence")
			calls.next(t).response <- fusionReply("Review later")
			fusionAwait(t, srv, id, func(th Thread) bool { return th.Status == ThreadStatusIdle })
			sides, err := srv.fusionSides(id)
			if err != nil || len(sides) != 1 {
				t.Fatalf("Sidekick: %+v %v", sides, err)
			}
			sideID := sides[0].ID
			if disabled {
				client.rpc(t, MethodConfigModelUpdate, ConfigModelUpdateParams{ThreadID: id, Fusion: new(bool)}, nil)
			}
			before, err := session.LoadHistoryRecords(srv.rt.SessionDir, sideID, true)
			if err != nil || len(before) == 0 {
				t.Fatalf("history: %+v %v", before, err)
			}
			for _, target := range []string{id, sideID} {
				if _, err := session.UpdatePinned(srv.rt.SessionDir, target, true); err != nil {
					t.Fatal(err)
				}
			}
			var archivedPair ThreadArchiveResult
			client.rpc(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true}, &archivedPair)
			if len(archivedPair.Threads) != 2 || archivedPair.Threads[1].FusionLeadID != id {
				t.Fatalf("archived Sidekick lost its persistent owner: %+v", archivedPair.Threads)
			}
			for _, target := range []string{id, sideID} {
				metadata, found, err := session.Find(srv.rt.SessionDir, target)
				if err != nil || !found || metadata.ArchivedAt == nil || metadata.PinnedAt != nil {
					t.Fatalf("archive split pair: %+v %v", metadata, err)
				}
				th := srv.thread(target)
				th.mu.Lock()
				archived := th.ArchivedAt != nil
				th.mu.Unlock()
				if !archived {
					t.Fatalf("cached thread %s not archived", target)
				}
			}
			var archived ThreadListResult
			client.rpc(t, MethodThreadListArchived, ThreadListParams{}, &archived)
			if len(archived.Threads) != 1 || archived.Threads[0].ID != id {
				t.Fatalf("archive should show one pair: %+v", archived.Threads)
			}
			client.rpc(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: false}, nil)
			lead, _, err := session.Find(srv.rt.SessionDir, id)
			if err != nil {
				t.Fatal(err)
			}
			side, found, err := session.Find(srv.rt.SessionDir, sideID)
			if err != nil || !found || side.ArchivedAt != nil {
				t.Fatalf("restore split pair: %+v %v", side, err)
			}
			if !disabled {
				reused, _, err := srv.ensureFusionSide(context.Background(), lead, false)
				if err != nil || reused.ID != sideID {
					t.Fatalf("restoration replaced Sidekick: %+v %v", reused, err)
				}
			}
			after, err := session.LoadHistoryRecords(srv.rt.SessionDir, sideID, true)
			if err != nil || len(after) != len(before) {
				t.Fatalf("restore changed history: %d -> %d, %v", len(before), len(after), err)
			}
			stateDir, err := srv.workspaceStateDir()
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{id, sideID} {
				if err := os.MkdirAll(statepath.SessionArtifactDir(stateDir, target), 0700); err != nil {
					t.Fatal(err)
				}
				if _, err := srv.sideThreadStore.BeginTurn(target, "retained side chat", "user", "answer"); err != nil {
					t.Fatal(err)
				}
				if _, _, err := srv.sideThreadStore.FinishTurn(target, "answer", "evidence", sidethread.StatusCompleted, ""); err != nil {
					t.Fatal(err)
				}
			}
			client.rpc(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true}, nil)
			client.rpc(t, MethodThreadDelete, ThreadDeleteParams{ThreadID: id, OnlyIfArchived: true}, nil)
			for _, target := range []string{id, sideID} {
				if _, found, err := session.Find(srv.rt.SessionDir, target); err != nil || found {
					t.Fatalf("session survived: %s %t %v", target, found, err)
				}
				if history, err := session.LoadHistoryRecords(srv.rt.SessionDir, target, true); (err != nil && !errors.Is(err, session.ErrSessionNotFound)) || len(history) != 0 {
					t.Fatalf("history survived: %s %v", target, err)
				}
				if srv.thread(target) != nil {
					t.Fatalf("cached runtime survived: %s", target)
				}
				if _, err := os.Stat(statepath.SessionArtifactDir(stateDir, target)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("artifacts survived: %s %v", target, err)
				}
				if exists, err := srv.sideThreadStore.Exists(target); err != nil || exists {
					t.Fatalf("side chat survived: %s %v", target, err)
				}
				if _, found, err := session.ReadControl(srv.rt.SessionDir, target); err != nil || found {
					t.Fatalf("control survived: %s %v", target, err)
				}
			}
			if tasks, err := session.ListFusionTasks(srv.rt.SessionDir, id); err != nil || len(tasks) != 0 {
				t.Fatalf("tasks survived: %+v %v", tasks, err)
			}
			if receipts, err := session.ListFusionDispatches(srv.rt.SessionDir, id); err != nil || len(receipts) != 0 {
				t.Fatalf("receipts survived: %+v %v", receipts, err)
			}
		})
	}
}

func TestFusionLifecycleRejectsIndependentSideAndBusyPair(t *testing.T) {
	srv, client, _ := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	lead, _, err := session.Find(srv.rt.SessionDir, created.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	side, _, err := srv.ensureFusionSide(context.Background(), lead, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, archived := range []bool{true, false} {
		if failure := client.call(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: side.ID, Archived: archived}, nil); failure == nil {
			t.Fatal("independent Sidekick archive/restore succeeded")
		}
	}
	if failure := client.call(t, MethodThreadDelete, ThreadDeleteParams{ThreadID: side.ID}, nil); failure == nil {
		t.Fatal("independent Sidekick deletion succeeded")
	}
	lease, acquired, err := session.TryAcquireThreadExecutionLease(srv.rt.SessionDir, side.ID)
	if err != nil || !acquired {
		t.Fatalf("execution lease: %t %v", acquired, err)
	}
	defer lease.Release()
	if failure := client.call(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: lead.ID, Archived: true}, nil); failure == nil {
		t.Fatal("archived pair while Sidekick was owned by another host")
	}
	if failure := client.call(t, MethodThreadDelete, ThreadDeleteParams{ThreadID: lead.ID}, nil); failure == nil {
		t.Fatal("deleted pair while Sidekick was owned by another host")
	}
	for _, id := range []string{lead.ID, side.ID} {
		metadata, found, err := session.Find(srv.rt.SessionDir, id)
		if err != nil || !found || metadata.ArchivedAt != nil {
			t.Fatalf("rejected operation changed pair: %+v %v", metadata, err)
		}
	}
}

func TestFusionDeleteFailurePreservesPairResources(t *testing.T) {
	srv, client, _ := newFusionFixture(t)
	var created ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{Fusion: true}, &created)
	lead, _, err := session.Find(srv.rt.SessionDir, created.Thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	side, _, err := srv.ensureFusionSide(context.Background(), lead, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{lead.ID, side.ID} {
		if _, err := srv.sideThreadStore.BeginTurn(id, "preserve me", "user", "answer"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := srv.sideThreadStore.FinishTurn(id, "answer", "preserved", sidethread.StatusCompleted, ""); err != nil {
			t.Fatal(err)
		}
	}
	srv.deleteSessionForTest = func(string) (session.Session, error) { return session.Session{}, errors.New("injected delete failure") }
	if failure := client.call(t, MethodThreadDelete, ThreadDeleteParams{ThreadID: lead.ID}, nil); failure == nil {
		t.Fatal("expected deletion failure")
	}
	for _, id := range []string{lead.ID, side.ID} {
		if _, found, err := session.Find(srv.rt.SessionDir, id); err != nil || !found {
			t.Fatalf("pair partially deleted: %s %v", id, err)
		}
		chat, err := srv.sideThreadStore.Load(id)
		if err != nil || len(chat.Messages) != 2 {
			t.Fatalf("side chat lost: %s %+v %v", id, chat, err)
		}
		if _, err := os.Stat(filepath.Join(srv.sideThreadStore.Dir(), ".deleting", id+".json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("staged delete survived failure: %s %v", id, err)
		}
	}
}
