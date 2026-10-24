package session

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

type sessionTestDocuments struct {
	*persistence.Store
	dir     string
	backend string
}

func readSessionDocument[T any](t *testing.T, store *documentStore, table, id string) T {
	t.Helper()
	var value T
	err := store.transaction(func(tx *persistence.Tx) error {
		var err error
		value, err = requireInferenceDocument[T](tx, table, id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func restartSessionStore(t *testing.T, store *documentStore) {
	t.Helper()
	documents := store.documents.(*sessionTestDocuments)
	if err := documents.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(context.Background(), documents.dir, documents.backend)
	if err != nil {
		t.Fatal(err)
	}
	documents.Store = reopened
}

func forEachSessionStore(t *testing.T, run func(*testing.T, *documentStore)) {
	t.Helper()
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			provider, err := persistence.Open(context.Background(), dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			documents := &sessionTestDocuments{Store: provider, dir: dir, backend: backend}
			t.Cleanup(func() {
				if err := documents.Close(); err != nil {
					t.Error(err)
				}
			})
			run(t, newDocumentStore(context.Background(), documents))
		})
	}
}

// The session domain must preserve control fencing, append-only checkpoints,
// explicit deletion admission, and the same data after a stopped migration.
func TestDocumentSessionRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	documents, err := persistence.Open(ctx, dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	store := newDocumentStore(ctx, documents)
	created, err := store.CreateInitialized(Session{ID: "one", CWD: "/workspace"}, []HistoryRecord{{Role: "user", Content: "first"}})
	if err != nil || created.ID != "one" {
		t.Fatalf("create: %+v, %v", created, err)
	}
	control, err := store.ChangeControl("one", "manager", ControlActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendControlledHistoryRecord("one", HistoryRecord{Role: "user", Content: "automatic"}, &control); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChangeControl("one", "manager", ControlTakenOver, control.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendControlledHistoryRecord("one", HistoryRecord{Role: "user", Content: "stale"}, &control); !errors.Is(err, ErrControlChanged) {
		t.Fatalf("stale control admitted: %v", err)
	}
	checkpoint, err := store.StoreHistoryCheckpointAtBaseline("one", "compaction", []HistoryRecord{{Role: "assistant", Content: "summary"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendHistoryRecord("one", HistoryRecord{Role: "user", Content: "tail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateTitle("one", "Review"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteArchived("one"); !errors.Is(err, ErrSessionNotArchived) {
		t.Fatalf("active session deleted: %v", err)
	}
	if err := documents.Close(); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Migrate(ctx, dir, "sqlite", "jsonl"); err != nil {
		t.Fatal(err)
	}
	documents, err = persistence.Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	store = newDocumentStore(ctx, documents)
	restored, found, err := store.Find("one")
	if err != nil || !found || restored.Title != "Review" || restored.TitleRevision != 1 {
		t.Fatalf("restored metadata: %+v, %v, %v", restored, found, err)
	}
	history, err := store.LoadProviderHistorySnapshot("one")
	if err != nil || len(history.Records) != 2 || history.Records[0].Content != "summary" || history.Records[1].Content != "tail" {
		t.Fatalf("restored provider history: %+v, %v", history, err)
	}
	latest, found, err := store.LatestHistoryCheckpoint("one")
	if err != nil || !found || latest.Version != checkpoint.Version {
		t.Fatalf("restored checkpoint: %+v, %v, %v", latest, found, err)
	}
	physical, err := store.LoadHistoryRecords("one", true)
	if err != nil || len(physical) != 4 {
		t.Fatalf("physical history lost: %+v, %v", physical, err)
	}
	if _, err := store.UpdateArchived("one", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteArchived("one"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Find("one"); err != nil || found {
		t.Fatalf("deleted session returned: %v, %v", found, err)
	}
}

// Native-domain tests use the same host admission mechanism as selected clients.
func (s *documentStore) TryAcquireThreadExecutionLease(dir, id string) (*DocumentThreadExecutionLease, bool, error) {
	return NewStore(s.ctx, s.documents).TryAcquireThreadExecutionLease(dir, id)
}
func (s *documentStore) TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id string, limit int) (*DocumentThreadExecutionLease, bool, error) {
	return NewStore(s.ctx, s.documents).TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id, limit)
}
func (s *documentStore) RequestThreadExecutionReset(dir, id string) (bool, error) {
	return NewStore(s.ctx, s.documents).RequestThreadExecutionReset(dir, id)
}
