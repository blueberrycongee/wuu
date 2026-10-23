package execution

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
)

type executionTestDocuments struct {
	*persistence.Store
	dir, backend string
}

type testExecutionStore struct {
	*Store
	documents *executionTestDocuments
	runtime   *session.InferenceJournalRuntime
}

func forEachExecutionStore(t *testing.T, run func(*testing.T, *testExecutionStore)) {
	t.Helper()
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			provider, err := persistence.Open(context.Background(), dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			documents := &executionTestDocuments{Store: provider, dir: dir, backend: backend}
			t.Cleanup(func() {
				if err := documents.Close(); err != nil {
					t.Error(err)
				}
			})
			runtime, err := session.NewStore(context.Background(), documents).NewInferenceJournalRuntime("test-workspace")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := runtime.Close(); err != nil {
					t.Error(err)
				}
			})
			store, err := NewStore(documents)
			if err != nil {
				t.Fatal(err)
			}
			run(t, &testExecutionStore{Store: store, documents: documents, runtime: runtime})
		})
	}
}

func restartExecutionStore(t *testing.T, store *testExecutionStore) (*Store, error) {
	t.Helper()
	if err := store.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.documents.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(context.Background(), store.documents.dir, store.documents.backend)
	if err != nil {
		t.Fatal(err)
	}
	store.documents.Store = reopened
	return NewStore(store.documents)
}

type pausedExecutionDocuments struct {
	persistence.Documents
	once         sync.Once
	read, resume chan struct{}
}

func (p *pausedExecutionDocuments) Read(ctx context.Context, query persistence.Query) (persistence.View, error) {
	view, err := p.Documents.Read(ctx, query)
	p.once.Do(func() { close(p.read); <-p.resume })
	return view, err
}

// Recent runs must be selected by a durable ordering index, including changes
// to status/workspace and terminal cleanup; a provider can reject broad scans.
func TestStoreListUsesBoundedAuthorityReads(t *testing.T) {
	forEachExecutionStore(t, func(t *testing.T, fixture *testExecutionStore) {
		ctx := context.Background()
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		var wanted []string
		for i := 0; i < 30; i++ {
			params := testCreateParams(fixture.runtime.RuntimeID(), ModeStart, "", fmt.Sprintf("thread-%d", i))
			if i%2 == 0 {
				params.Workspace.ID = "wanted"
			} else {
				params.Workspace.ID = "other"
			}
			run, err := fixture.Create(ctx, params)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.Complete(ctx, run.ID, Result{}, base.Add(time.Duration(i)*time.Second)); err != nil {
				t.Fatal(err)
			}
			if i%2 == 0 {
				wanted = append(wanted, run.ID)
			}
		}
		authority := &boundedExecutionDocuments{Documents: fixture.documents}
		store, err := NewStore(authority)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := store.List(ctx, ListOptions{WorkspaceID: "wanted", Status: StatusCompleted, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 3 {
			t.Fatalf("listed %d runs", len(listed))
		}
		for i, run := range listed {
			if run.ID != wanted[len(wanted)-1-i] {
				t.Fatalf("wrong ordering: %+v", listed)
			}
		}
		if authority.returned > 12 {
			t.Fatalf("loaded %d records for three results", authority.returned)
		}
	})
}

type boundedExecutionDocuments struct {
	persistence.Documents
	returned int
}

func (s *boundedExecutionDocuments) Read(ctx context.Context, query persistence.Query) (persistence.View, error) {
	if query.Keys == nil && query.Limit == 0 {
		return persistence.View{}, fmt.Errorf("provider rejects unbounded read of %s", query.Namespace)
	}
	view, err := s.Documents.Read(ctx, query)
	s.returned += len(view.Documents)
	return view, err
}
