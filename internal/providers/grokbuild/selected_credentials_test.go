package grokbuild

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func TestGrokCredentialSourceUsesSelectedCLIRecord(t *testing.T) {
	ctx := context.Background()
	selected := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err := selected.Set(ctx, authstorage.CLIRecordNamespace, "grok", []byte(`{"access_token":"selected-token"}`)); err != nil {
		t.Fatal(err)
	}
	source := credentialSource{credentials: selected, reuseCLI: true}
	token, err := source.token(ctx)
	if err != nil || token != "selected-token" {
		t.Fatalf("selected CLI account: %v", err)
	}
	if err := selected.Delete(ctx, authstorage.CLIRecordNamespace, "grok"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.token(ctx); err == nil {
		t.Fatal("removed selected account survived")
	}
}
