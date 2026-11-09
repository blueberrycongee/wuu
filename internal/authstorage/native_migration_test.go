package authstorage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func TestMissingLegacyCredentialsDoNotWriteSelectedStore(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "credentials.json")
	base := credentialstore.NewFileStore(path)
	native, err := NewNativeStore(base, filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := NewBound(ctx, native).Get("absent"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing credentials: %v", err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created selected credential storage: %v", err)
	}
	if err := NewBound(ctx, base).Set("existing", Credentials{APIKey: "selected"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewNativeStore(base, filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewBound(ctx, reopened).Get("existing")
	if err != nil || got.APIKey != "selected" {
		t.Fatalf("selected credentials lost: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("read changed selected credentials without legacy source: %v", err)
	}
}
