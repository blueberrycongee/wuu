package authstorage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/credentialstore"
)

func TestBoundModelCredentialsPreserveConcurrentProvidersAndFailClosed(t *testing.T) {
	ctx := context.Background()
	base := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "selected.json"))
	store := NewBound(ctx, base)
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Set(id, Credentials{APIKey: id, Source: SourceSaved}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	file, err := store.Load()
	if err != nil || len(file.Providers) != 4 {
		t.Fatalf("lost selected provider entries: %v", err)
	}
	if err := store.Update("a", func(value *Credentials) { value.APIKey = "next" }); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProvider("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("b"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted selected entry returned")
	}
	if _, err := NewBound(ctx, nil).Get("a"); err == nil {
		t.Fatal("unbound model credentials fell back")
	}
	failed := credentialstore.NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("private-value")
	})
	if _, err := NewBound(ctx, failed).Get("a"); err == nil || strings.Contains(err.Error(), "private-value") {
		t.Fatal("selected failure fell back or leaked provider data")
	}
}

func TestNativeCredentialMigrationDoesNotOverwriteOrResurrect(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, ".wuu"))
	legacy, err := ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Set("saved", Credentials{APIKey: "legacy", Source: SourceSaved}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Set("existing", Credentials{AccessToken: "legacy-oauth"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	base := credentialstore.NewFileStore(path)
	if err := NewBound(ctx, base).Set("existing", Credentials{AccessToken: "selected-oauth"}); err != nil {
		t.Fatal(err)
	}
	native, err := NewNativeStore(base, filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatal(err)
	}
	bound := NewBound(ctx, native)
	existing, err := bound.Get("existing")
	if err != nil || existing.AccessToken != "selected-oauth" {
		t.Fatal("migration replaced selected credentials")
	}
	saved, err := bound.Get("saved")
	if err != nil || saved.APIKey != "legacy" || saved.Source != SourceSaved {
		t.Fatal("legacy saved credentials lost")
	}
	if err := bound.DeleteProvider("saved"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Get("saved"); err != nil {
		t.Fatal("migration removed recovery source")
	}
	if err := os.WriteFile(legacy.path, []byte(`invalid later legacy bytes`), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewNativeStore(credentialstore.NewFileStore(path), filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatalf("completed migration reread legacy file: %v", err)
	}
	if _, err := NewBound(ctx, reopened).Get("saved"); !errors.Is(err, ErrNotFound) {
		t.Fatal("restart resurrected deleted credentials")
	}
}

func TestNativeCLIRecordsAreReadOnlyAndPathsAreCaptured(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, ".wuu"))
	codexDir, grokDir := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv("GROK_HOME", grokDir)
	codexPath := filepath.Join(codexDir, "auth.json")
	if err := os.WriteFile(codexPath, []byte(`{"tokens":{"access_token":"codex-one","account_id":"account"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokDir, "auth.json"), []byte(`{"https://accounts.x.ai/sign-in":{"token":"grok-one"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	base := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	native, err := NewNativeStore(base, filepath.Join(home, ".wuu"), home)
	if err != nil {
		t.Fatal(err)
	}
	grok, err := ReadCLI(ctx, native, "grok")
	if err != nil || grok.AccessToken != "grok-one" {
		t.Fatalf("selected Grok CLI source failed: %v", err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	first, err := ReadCLI(ctx, native, "codex")
	if err != nil || first.AccessToken != "codex-one" {
		t.Fatalf("captured CLI source failed: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte(`{"tokens":{"access_token":"codex-two"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := ReadCLI(ctx, native, "codex")
	if err != nil || next.AccessToken != "codex-two" {
		t.Fatal("explicit CLI source stopped observing account changes")
	}
	for _, write := range []func() error{func() error { return native.Set(ctx, CLIRecordNamespace, "codex", []byte(`changed`)) }, func() error { return native.Delete(ctx, CLIRecordNamespace, "codex") }, func() error {
		return native.Update(ctx, CLIRecordNamespace, "codex", func([]byte) ([]byte, error) { return nil, nil })
	}} {
		if err := write(); err == nil {
			t.Fatal("CLI credential source was writable")
		}
	}
	if err := base.Set(ctx, CLIRecordNamespace, "codex", []byte(`{"access_token":"external"}`)); err != nil {
		t.Fatal(err)
	}
	external, err := ReadCLI(ctx, base, "codex")
	if err != nil || external.AccessToken != "external" {
		t.Fatal("selected external CLI record opened native source")
	}
}
