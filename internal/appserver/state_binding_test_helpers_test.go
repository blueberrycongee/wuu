package appserver

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func testRuntimeState(t *testing.T, rt *runtime.Session) *runtime.State {
	t.Helper()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := binding.Close(); err != nil {
			t.Error(err)
		}
	})
	return binding
}

func newAppserverTestStore(t *testing.T, dir string) *session.Store {
	t.Helper()
	documents, err := persistence.Open(context.Background(), dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := documents.Close(); err != nil {
			t.Error(err)
		}
	})
	return session.NewStore(context.Background(), documents)
}
