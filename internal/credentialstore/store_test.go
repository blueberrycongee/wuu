package credentialstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringStoreUsesNamespacedAccountsWithoutFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := &fakeKeyringBackend{values: map[string]string{}}
	store := NewKeyringStore("wuu-test", backend)
	ctx := context.Background()
	secret := []byte{0, 1, 2, 255}
	if err := store.Set(ctx, "mcp:docs", "oauth", secret); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "mcp:docs", "oauth")
	if err != nil || string(got) != string(secret) {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if _, ok := backend.values["wuu-test\x00mcp:docs:oauth"]; !ok {
		t.Fatalf("namespaced keyring account missing: %+v", backend.values)
	}
	rejected := errors.New("conditional write rejected")
	if err := store.Update(ctx, "mcp:docs", "oauth", func(current []byte) ([]byte, error) {
		if string(current) != string(secret) {
			t.Fatalf("Update read = %v", current)
		}
		return []byte("must-not-save"), rejected
	}); !errors.Is(err, rejected) {
		t.Fatalf("Update error = %v", err)
	}
	if got, err := store.Get(ctx, "mcp:docs", "oauth"); err != nil || string(got) != string(secret) {
		t.Fatalf("rejected Update changed secret: %v", err)
	}
	if err := store.Delete(ctx, "mcp:docs", "oauth"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "mcp:docs", "oauth"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete = %v", err)
	}
}

func TestFileStoreIsExplicitAndOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := NewFileStore(path)
	ctx := context.Background()
	if err := store.Set(ctx, "mcp:docs", "oauth", []byte("secret-value")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	got, err := store.Get(ctx, "mcp:docs", "oauth")
	if err != nil || string(got) != "secret-value" {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

type fakeKeyringBackend struct {
	values map[string]string
}

func (f *fakeKeyringBackend) Get(service, account string) (string, error) {
	value, ok := f.values[service+"\x00"+account]
	if !ok {
		return "", errKeyringNotFound
	}
	return value, nil
}

func (f *fakeKeyringBackend) Set(service, account, value string) error {
	f.values[service+"\x00"+account] = value
	return nil
}

func (f *fakeKeyringBackend) Delete(service, account string) error {
	delete(f.values, service+"\x00"+account)
	return nil
}
