package agentthread

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func TestEnsureRootConcurrentAndWorkspaceChange(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), backend)
			if err != nil {
				t.Fatal(err)
			}
			defer docs.Close()
			store := NewDocumentStore(docs, "session")
			first := time.Unix(100, 0).UTC()
			second := first.Add(time.Hour)
			desired := func(at time.Time) Metadata {
				return Metadata{ID: "session", SessionID: "session", Path: RootPath, TaskName: "root", Role: "root", CWD: "/first", Status: StatusRunning, CreatedAt: at, UpdatedAt: at}
			}
			ready := make(chan struct{})
			results := make([]Metadata, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i, at := range []time.Time{first, second} {
				wg.Add(1)
				go func(i int, at time.Time) {
					defer wg.Done()
					<-ready
					results[i], errs[i] = store.EnsureRoot(desired(at))
				}(i, at)
			}
			close(ready)
			wg.Wait()
			if errs[0] != nil || errs[1] != nil {
				t.Fatalf("ensure root: %v %v", errs[0], errs[1])
			}
			if !results[0].CreatedAt.Equal(results[1].CreatedAt) || !results[0].UpdatedAt.Equal(results[1].UpdatedAt) {
				t.Fatalf("concurrent registrations differ: %+v", results)
			}
			events, err := store.ReadEvents()
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%+v err=%v", events, err)
			}
			changed := desired(second.Add(time.Hour))
			changed.CWD = "/second"
			updated, err := store.EnsureRoot(changed)
			if err != nil {
				t.Fatal(err)
			}
			if !updated.CreatedAt.Equal(results[0].CreatedAt) || updated.CWD != "/second" || !updated.UpdatedAt.Equal(changed.UpdatedAt) {
				t.Fatalf("workspace update lost root identity: %+v", updated)
			}
			events, err = store.ReadEvents()
			if err != nil || len(events) != 2 {
				t.Fatalf("events after workspace change=%+v err=%v", events, err)
			}
			foreign := desired(first)
			foreign.ID = "other"
			if _, err := store.EnsureRoot(foreign); err == nil {
				t.Fatal("another identity claimed the root path")
			}
		})
	}
}

func TestEnsureRootPreservesExistingMetadata(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store := NewDocumentStore(docs, "session")
	created := time.Unix(100, 0).UTC()
	old := Metadata{ID: "session", SessionID: "session", Path: RootPath, TaskName: "saved name", Role: "root", AgentProfile: "profile", LastTaskMessage: "saved task", CWD: "/workspace", Model: "model", Source: Source{Kind: SourceRoot, EdgeStatus: EdgeClosed}, Status: StatusWaitingChildren, CreatedAt: created, UpdatedAt: created.Add(time.Minute)}
	if err := store.UpsertThread(old); err != nil {
		t.Fatal(err)
	}
	desired := Metadata{ID: "session", SessionID: "session", Path: RootPath, CWD: old.CWD, Model: old.Model, CreatedAt: created.Add(time.Hour), UpdatedAt: created.Add(time.Hour)}
	got, err := store.EnsureRoot(desired)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("unchanged root: %+v %v", got, err)
	}
	desired.CWD = "/new-workspace"
	got, err = store.EnsureRoot(desired)
	want := old
	want.CWD, want.UpdatedAt = desired.CWD, desired.UpdatedAt
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace update: got=%+v want=%+v err=%v", got, want, err)
	}
}
