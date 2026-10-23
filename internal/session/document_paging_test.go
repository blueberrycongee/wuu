package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

type boundedSessionDocuments struct {
	persistence.Documents
	maxRead int
}

func (d *boundedSessionDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if q.Keys == nil && q.Limit == 0 {
		return persistence.View{}, fmt.Errorf("unbounded read: %s", q.Namespace)
	}
	v, err := d.Documents.Read(ctx, q)
	d.maxRead = max(d.maxRead, len(v.Documents))
	return v, err
}

// Historical pages must seek physical sequence ranges rather than hydrate the
// entire thread; list ordering/index updates and revision cursors survive restarts.
func TestDocumentBoundedPages(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		now := time.Now().UTC()
		for i := 0; i < 30; i++ {
			if _, err := store.CreateInitialized(Session{ID: fmt.Sprintf("s%02d", i), UpdatedAt: now.Add(time.Duration(i) * time.Minute)}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.UpdatePinned("s01", true); err != nil {
			t.Fatal(err)
		}
		records := make([]HistoryRecord, 600)
		for i := range records {
			records[i] = HistoryRecord{Role: "user", Content: fmt.Sprintf("record-%04d", i+1)}
		}
		if err := store.AppendHistoryRecords("s29", records); err != nil {
			t.Fatal(err)
		}
		restartSessionStore(t, store)
		bounded := &boundedSessionDocuments{Documents: store.documents}
		reader := NewStore(context.Background(), bounded)
		page, err := reader.ReadHistoryPage("s29", 300, 3)
		if err != nil || len(page.Records) != 3 || page.Records[0].Seq != 300 || !page.HasMore {
			t.Fatalf("history: %+v %v", page, err)
		}
		search, err := reader.SearchHistoryPage("s29", "record-0599", 0, 1)
		if err != nil || len(search.Records) != 1 || search.Records[0].Seq != 599 {
			t.Fatalf("search: %+v %v", search, err)
		}
		listed, err := reader.List(3)
		if err != nil || len(listed) != 3 || listed[0].ID != "s01" || listed[1].ID != "s29" {
			t.Fatalf("list: %+v %v", listed, err)
		}
		first, err := reader.ListPage(SessionListQuery{Limit: 2})
		if err != nil || len(first.Sessions) != 2 || first.Next == nil {
			t.Fatalf("first list page: %+v %v", first, err)
		}
		second, err := reader.ListPage(SessionListQuery{Limit: 2, Cursor: first.Next})
		if err != nil || len(second.Sessions) != 2 || second.Sessions[0].ID != "s28" {
			t.Fatalf("second list page: %+v %v", second, err)
		}
		if _, err := store.UpdateTitle("s02", "changed"); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ListPage(SessionListQuery{Limit: 2, Cursor: first.Next}); !errors.Is(err, persistence.ErrConflict) {
			t.Fatalf("stale cursor: %v", err)
		}
		lease, acquired, err := reader.TryAcquireThreadExecutionLeaseWithProjectLimit(t.TempDir(), "s29", 1)
		if err != nil || !acquired {
			t.Fatalf("bounded admission: %v %v", acquired, err)
		}
		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}

		if bounded.maxRead > 257 {
			t.Fatalf("read %d documents at once", bounded.maxRead)
		}
	})
}

func TestDocumentIndexUpgradeIsExplicit(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		if err := store.transaction(func(tx *persistence.Tx) error {
			return tx.Put(documentNamespace("sessions"), "before-index", Session{ID: "before-index", CreatedAt: time.Now().UTC()})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.List(1); !errors.Is(err, persistence.ErrMigrationRequired) {
			t.Fatalf("missing index silently read: %v", err)
		}
		if err := store.RebuildSessionIndexes(); err != nil {
			t.Fatal(err)
		}
		listed, err := store.List(1)
		if err != nil || len(listed) != 1 || listed[0].ID != "before-index" {
			t.Fatalf("upgraded: %+v %v", listed, err)
		}
		if _, err := store.Delete("before-index"); err != nil {
			t.Fatal(err)
		}
		listed, err = store.List(1)
		if err != nil || len(listed) != 0 {
			t.Fatalf("orphan index: %+v %v", listed, err)
		}
		if err := store.transaction(func(tx *persistence.Tx) error { return tx.Put(documentNamespace("session_indexes"), "listing", 2) }); err != nil {
			t.Fatal(err)
		}
		if err := store.RebuildSessionIndexes(); !errors.Is(err, persistence.ErrMigrationRequired) {
			t.Fatalf("unknown index version downgraded: %v", err)
		}
		if _, err := store.Create("new-session"); !errors.Is(err, persistence.ErrMigrationRequired) {
			t.Fatalf("write downgraded unknown index: %v", err)
		}
		if _, err := store.ListPage(SessionListQuery{Limit: 1, Cursor: &SessionListCursor{}}); !errors.Is(err, persistence.ErrInvalid) {
			t.Fatalf("malformed cursor: %v", err)
		}

	})
}
