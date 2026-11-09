package authstorage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestNewHeadlessCredentialStoreIsExplicitAndSeparate(t *testing.T) {
	home := t.TempDir()
	store, path, err := NewHeadlessCredentialStore(home)
	if err != nil {
		t.Fatalf("NewHeadlessCredentialStore: %v", err)
	}
	if filepath.Base(path) != "credentials.json" {
		t.Fatalf("path = %q, want credentials.json", path)
	}
	if err := store.Set(context.Background(), "mcp:docs", "oauth", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("credential file: %v", err)
	}
	authPath, err := statepath.AuthPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("headless MCP credentials should not create provider auth file: %v", err)
	}
}

func TestStoreRoundTripAllCredentialKinds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store := New(path)
	if err := store.Set("api", Credentials{Type: "api_key", APIKey: "sk-test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("bearer", Credentials{Type: "auth_token", AuthToken: "token"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai-codex", Credentials{Type: "oauth", AccessToken: "access", RefreshToken: "refresh", AccountID: "account"}); err != nil {
		t.Fatal(err)
	}
	file, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if file.Version != CurrentVersion || file.Providers["api"].APIKey != "sk-test" || file.Providers["bearer"].AuthToken != "token" || file.Providers["openai-codex"].RefreshToken != "refresh" {
		t.Fatalf("unexpected file: %+v", file)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", info, err)
	}
}

func TestStoreSerializesAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	commands := make([]*exec.Cmd, 8)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=TestStoreProcessHelper")
		commands[i].Env = append(os.Environ(), "WUU_AUTH_HELPER=1", "WUU_AUTH_PATH="+path, fmt.Sprintf("WUU_AUTH_PROVIDER=p%d", i))
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	file, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Providers) != len(commands) {
		t.Fatalf("providers = %d, want %d", len(file.Providers), len(commands))
	}
}

func TestStoreProcessHelper(t *testing.T) {
	if os.Getenv("WUU_AUTH_HELPER") != "1" {
		return
	}
	if err := New(os.Getenv("WUU_AUTH_PATH")).Set(os.Getenv("WUU_AUTH_PROVIDER"), Credentials{Type: "api_key", APIKey: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreInstancesSerializeUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			if err := New(path).Set(id, Credentials{Type: "api_key", APIKey: id}); err != nil {
				t.Errorf("set: %v", err)
			}
		}(i)
	}
	wg.Wait()
	file, err := New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Providers) != 20 {
		t.Fatalf("providers = %d", len(file.Providers))
	}
}

func TestStoreRejectsUnversionedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"provider":{"api_key":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(path).Load(); err == nil {
		t.Fatal("expected unsupported version error")
	}
}
