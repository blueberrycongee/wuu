package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
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

func testStateModules() []string {
	return []string{sessionStateConsumer, persistence.SQLiteModule, StateServicesConsumer,
		credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule}
}

func testWorkspaceState(t *testing.T, root string) (string, string) {
	t.Helper()
	home := t.TempDir()
	state, err := statepath.WorkspaceDir(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	return home, state
}

func testKernelStateProviders(t *testing.T, root string) []pluginhost.Client {
	t.Helper()
	home, state := testWorkspaceState(t, root)
	// The file credentials provider keeps fixture authority inside its temp home.
	clients, err := nativeStateProviders(home, root, state, []string{credentialstore.OSModule})
	if err != nil {
		t.Fatal(err)
	}
	return clients
}
