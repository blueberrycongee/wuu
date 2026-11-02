package agentthread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// One-time migration, cross-client claims and failure fencing protect the
// public state provider contract, not the implementation of the file adapter.
func TestDocumentStoreMigrationAndClaimOwnership(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	legacyDir := t.TempDir()
	legacy := NewStore(legacyDir)
	if err := legacy.UpsertThread(Metadata{ID: "child", Path: "0/1"}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.AppendEvent(Event{Type: EventResultReady, ResultID: "result"}); err != nil {
		t.Fatal(err)
	}
	repeated := Event{Type: EventMessage, ThreadID: "child", Message: "repeat", CreatedAt: time.Unix(1, 0).UTC()}
	if err := legacy.AppendEvent(repeated); err != nil {
		t.Fatal(err)
	}
	if err := legacy.AppendEvent(repeated); err != nil {
		t.Fatal(err)
	}
	first := NewDocumentStore(docs, "session", legacyDir)
	threads, err := first.ListThreads()
	if err != nil || len(threads) != 1 {
		t.Fatalf("migration: %+v %v", threads, err)
	}
	events, eventErr := first.ReadEvents()
	if eventErr != nil || len(events) != 4 {
		t.Fatalf("migration erased repeated facts: %+v %v", events, eventErr)
	}
	if err := legacy.UpsertThread(Metadata{ID: "stale", Path: "0/2"}); err != nil {
		t.Fatal(err)
	}
	second := NewDocumentStore(docs, "session", legacyDir)
	threads, err = second.ListThreads()
	if err != nil || len(threads) != 1 {
		t.Fatalf("reimported legacy state: %+v %v", threads, err)
	}
	start := make(chan struct{})
	results := make(chan bool, 2)
	var group sync.WaitGroup
	for i, store := range []*Store{first, second} {
		group.Add(1)
		go func(i int, store *Store) {
			defer group.Done()
			<-start
			consumer := []string{"one", "two"}[i]
			claimed, _, err := store.ClaimResultDelivery(Event{ResultID: "result", Consumer: consumer})
			if err != nil {
				t.Error(err)
			}
			results <- claimed
		}(i, store)
	}
	close(start)
	group.Wait()
	close(results)
	claims := 0
	for claimed := range results {
		if claimed {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("claimed result %d times", claims)
	}
	if err := docs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ListThreads(); !errors.Is(err, persistence.ErrClosed) {
		t.Fatalf("closed provider fell back to legacy files: %v", err)
	}
}

func TestDocumentStoreResolvesLegacyPathsOnlyForUnmigratedData(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	legacyDir := t.TempDir()
	if err := NewStore(legacyDir).UpsertThread(Metadata{ID: "legacy-child", Path: "/root/child"}); err != nil {
		t.Fatal(err)
	}
	if err := NewDocumentStore(docs, "already-migrated").UpsertThread(Metadata{ID: "current-child", Path: "/root/current"}); err != nil {
		t.Fatal(err)
	}
	lookupErr := errors.New("legacy roots unavailable")
	lookup := func() ([]string, error) { return nil, lookupErr }
	current := NewDocumentStoreWithLegacyResolver(docs, "already-migrated", lookup)
	threads, err := current.ListThreads()
	if err != nil || len(threads) != 1 || threads[0].ID != "current-child" {
		t.Fatalf("migrated data depended on unavailable legacy roots: %+v %v", threads, err)
	}
	unmigrated := NewDocumentStoreWithLegacyResolver(docs, "unmigrated", lookup)
	if _, err := unmigrated.ListThreads(); !errors.Is(err, lookupErr) {
		t.Fatalf("legacy lookup failure was not reported: %v", err)
	}
	unmigrated = NewDocumentStoreWithLegacyResolver(docs, "unmigrated", func() ([]string, error) {
		return []string{legacyDir}, nil
	})
	threads, err = unmigrated.ListThreads()
	if err != nil || len(threads) != 1 || threads[0].ID != "legacy-child" {
		t.Fatalf("retry did not import legacy data: %+v %v", threads, err)
	}
	if err := docs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDocumentStoreWithLegacyResolver(docs, "closed-provider", lookup).ListThreads(); !errors.Is(err, persistence.ErrClosed) {
		t.Fatalf("provider failure fell back to legacy roots: %v", err)
	}
}

type lostCommitResponse struct {
	persistence.Documents
	fail    bool
	commits int
}

func (d *lostCommitResponse) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	d.commits++
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil && d.fail {
		return persistence.Receipt{}, errors.New("response lost after commit")
	}
	return receipt, err
}

func TestDocumentStorePreservesUncertainCommitIntent(t *testing.T) {
	backend, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	docs := &lostCommitResponse{Documents: backend}
	store := NewDocumentStore(docs, "session", "")
	if _, err := store.ListThreads(); err != nil {
		t.Fatal(err)
	}
	before := docs.commits
	docs.fail = true
	err = store.UpsertThread(Metadata{ID: "child", Path: "0/1"})
	var uncertain *persistence.CommitError
	if !errors.As(err, &uncertain) || docs.commits != before+1 {
		t.Fatalf("uncertain write retried or lost intent: commits=%d error=%v", docs.commits-before, err)
	}
	docs.fail = false
	if _, err := docs.Commit(context.Background(), uncertain.Transaction); err != nil {
		t.Fatal(err)
	}
	threads, err := store.ListThreads()
	if err != nil || len(threads) != 1 {
		t.Fatalf("recover committed state: %+v %v", threads, err)
	}
	events, err := store.ReadEvents()
	if err != nil || len(events) != 1 {
		t.Fatalf("uncertain result duplicated effect: %+v %v", events, err)
	}
}

func TestDocumentMigrationRejectsCorruptLegacyEvents(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{broken}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = NewDocumentStore(docs, "session", dir).ListThreads()
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("corruption treated as empty migration: %v", err)
	}
}
