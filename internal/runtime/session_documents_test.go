package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Isolated adapter fixtures explicitly share a durable document directory.
// Runtime composition tests use the authority selected by their service graph.
func testSessionDocuments(t *testing.T, dir string) *persistence.Store {
	t.Helper()
	documents, err := persistence.Open(context.Background(), filepath.Join(dir, "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := documents.Close(); err != nil {
			t.Error(err)
		}
	})
	return documents
}

func testSessionStore(t *testing.T, dir string) *session.Store {
	t.Helper()
	return session.NewStore(context.Background(), testSessionDocuments(t, dir))
}
