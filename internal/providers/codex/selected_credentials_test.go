package codex

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func testSelectedCredentials(t *testing.T, home string) credentialstore.Store {
	t.Helper()
	selected, err := authstorage.NewNativeStore(credentialstore.NewFileStore(filepath.Join(home, "selected-credentials.json")), filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}
func testSelectedAuth(t *testing.T, home string) *authstorage.Store {
	return authstorage.NewBound(context.Background(), testSelectedCredentials(t, home))
}

func TestOAuthSourceFollowsSelectedCredentialRevocation(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	selected := testSelectedCredentials(t, home)
	store := authstorage.NewBound(ctx, selected)
	source := NewOAuthSource(OAuthConfig{Credentials: selected})
	for _, account := range []string{"first", "second"} {
		token := fakeJWT(t, time.Now().Add(time.Hour), account)
		if err := store.Set("openai-codex", authstorage.Credentials{AccessToken: token}); err != nil {
			t.Fatal(err)
		}
		got, err := source.Credentials(ctx, false)
		if err != nil || got.accessToken != token {
			t.Fatalf("selected account change was ignored: %v", err)
		}
	}
	if err := store.DeleteProvider("openai-codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Credentials(ctx, false); err == nil {
		t.Fatal("signed-out account survived in cached client")
	}
	t.Setenv("HOME", home)
	if _, err := NewOAuthSource(OAuthConfig{}).Credentials(ctx, false); err == nil {
		t.Fatal("unbound source opened native credentials")
	}
}
