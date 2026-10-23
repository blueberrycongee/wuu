package xaisub

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func testSelectedCredentials(t *testing.T, home string) credentialstore.Store {
	t.Helper()
	return credentialstore.NewFileStore(filepath.Join(home, "selected-credentials.json"))
}
func testSelectedAuth(t *testing.T, home string) *authstorage.Store {
	return authstorage.NewBound(context.Background(), testSelectedCredentials(t, home))
}
func TestOAuthSourceNeverUsesUnselectedNativeAccount(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", filepath.Join(home, ".wuu"))
	if err := PersistTokens(home, TokenResponse{AccessToken: "native", RefreshToken: "refresh"}, ""); err != nil {
		t.Fatal(err)
	}
	selected := testSelectedCredentials(t, home)
	if _, err := NewOAuthSource(OAuthConfig{Credentials: selected}).Credentials(ctx, false); err == nil {
		t.Fatal("selected missing account fell back to native auth")
	}
	if _, err := NewOAuthSource(OAuthConfig{}).Credentials(ctx, false); err == nil {
		t.Fatal("unbound client opened native auth")
	}
}
