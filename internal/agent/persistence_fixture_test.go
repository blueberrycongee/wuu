package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func agentTestDocuments(t *testing.T, dir string) persistence.Documents {
	t.Helper()
	store, err := persistence.Open(context.Background(), filepath.Join(dir, "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}
