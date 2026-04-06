package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthStore_RoundTrip(t *testing.T) {
	home := t.TempDir()
	if err := SaveAuthKey(home, "openai", "sk-test-key-123"); err != nil {
		t.Fatalf("SaveAuthKey: %v", err)
	}
	key, err := LoadAuthKey(home, "openai")
	if err != nil {
		t.Fatalf("LoadAuthKey: %v", err)
	}
	if key != "sk-test-key-123" {
		t.Fatalf("expected sk-test-key-123, got %q", key)
	}
	path := filepath.Join(home, ".config", "wuu", "auth.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", info.Mode().Perm())
	}
}

func TestAuthStore_UnknownProvider(t *testing.T) {
	home := t.TempDir()
	_, err := LoadAuthKey(home, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAuthStore_MultipleProviders(t *testing.T) {
	home := t.TempDir()
	SaveAuthKey(home, "openai", "sk-openai")
	SaveAuthKey(home, "anthropic", "sk-ant-xxx")
	k1, _ := LoadAuthKey(home, "openai")
	k2, _ := LoadAuthKey(home, "anthropic")
	if k1 != "sk-openai" {
		t.Fatalf("openai key mismatch: %q", k1)
	}
	if k2 != "sk-ant-xxx" {
		t.Fatalf("anthropic key mismatch: %q", k2)
	}
}
