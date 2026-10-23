package execution

import (
	"context"
	"sync"
	"testing"

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
