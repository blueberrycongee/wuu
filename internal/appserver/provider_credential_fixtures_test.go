package appserver

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"path/filepath"
	"testing"
)

func nativeCredentialFixture(t *testing.T, home string) credentialstore.Store {
	t.Helper()
	store, err := authstorage.NewNativeStore(credentialstore.NewFileStore(filepath.Join(home, "test-credentials.json")), filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func selectedCredentialFixture(t *testing.T, rt *runtime.Session) credentialstore.Store {
	t.Helper()
	bindConfigSourceForTest(t, rt)
	services, release, err := rt.RetainStateServices()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return services.Credentials
}

func selectedAuthFixture(t *testing.T, rt *runtime.Session) *authstorage.Store {
	t.Helper()
	return authstorage.NewBound(context.Background(), selectedCredentialFixture(t, rt))
}
