package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const migrateTestConfigJSON = `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://legacy.example/v1",
      "api_key": "sk-legacy",
      "model": "legacy-model"
    }
  }
}`

const migrateTestNewConfigJSON = `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://new.example/v1",
      "api_key": "sk-new",
      "model": "new-model"
    }
  }
}`

// writeLegacyConfig writes a config.json into the legacy ~/.config/wuu dir.
func writeLegacyConfig(t *testing.T, home, contents string) string {
	t.Helper()
	dir := filepath.Join(home, ".config", "wuu")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir legacy config dir: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}
	return path
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	_ = w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

func TestLoadFrom_MigratesLegacyConfigToUnifiedHome(t *testing.T) {
	t.Setenv("WUU_HOME", "")
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyPath := writeLegacyConfig(t, home, migrateTestConfigJSON)

	var cfg Config
	var path string
	captureStderr(t, func() {
		var err error
		cfg, path, err = LoadFrom(workdir, home)
		if err != nil {
			t.Fatalf("LoadFrom: %v", err)
		}
	})

	newPath := filepath.Join(home, ".wuu", "config.json")
	if path != newPath {
		t.Fatalf("LoadFrom path = %q, want migrated %q", path, newPath)
	}
	if cfg.Providers["main"].Model != "legacy-model" {
		t.Fatalf("migrated config not loaded: %+v", cfg.Providers["main"])
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected migrated config at %q: %v", newPath, err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy config must be preserved, not moved: %v", err)
	}
}

func TestLoadFrom_PrefersUnifiedOverLegacyConfig(t *testing.T) {
	t.Setenv("WUU_HOME", "")
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeLegacyConfig(t, home, migrateTestConfigJSON)

	newDir := filepath.Join(home, ".wuu")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("mkdir new dir: %v", err)
	}
	newPath := filepath.Join(newDir, "config.json")
	if err := os.WriteFile(newPath, []byte(migrateTestNewConfigJSON), 0o644); err != nil {
		t.Fatalf("write new config: %v", err)
	}

	cfg, path, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if path != newPath {
		t.Fatalf("expected unified config path %q, got %q", newPath, path)
	}
	if cfg.Providers["main"].Model != "new-model" {
		t.Fatalf("unified config should win over legacy: %+v", cfg.Providers["main"])
	}
	// The pre-existing unified config must not be clobbered by the legacy copy.
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read new config: %v", err)
	}
	if !strings.Contains(string(data), "new-model") {
		t.Fatalf("unified config was overwritten by legacy migration:\n%s", string(data))
	}
}

func TestLoadFrom_DoesNotFallBackToLegacyOnCanonicalReadError(t *testing.T) {
	t.Setenv("WUU_HOME", "")
	workdir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeLegacyConfig(t, home, migrateTestConfigJSON)
	canonicalPath := filepath.Join(home, ".wuu", "config.json")
	writeSelfReferentialSymlink(t, canonicalPath)

	_, _, err := LoadFrom(workdir, home)
	if err == nil {
		t.Fatal("expected the canonical config read error")
	}
	if errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("canonical read error classified as not found: %v", err)
	}
	if !strings.Contains(err.Error(), canonicalPath) {
		t.Fatalf("error %q does not identify canonical config %q", err, canonicalPath)
	}
}

func TestLoadFrom_MigratesUnderWuuHomeOverride(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()
	override := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", override)
	t.Setenv("HOME", home)

	writeLegacyConfig(t, home, migrateTestConfigJSON)

	cfg, path, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	wantPath := filepath.Join(override, "config.json")
	if path != wantPath {
		t.Fatalf("WUU_HOME override path = %q, want %q", path, wantPath)
	}
	if cfg.Providers["main"].Model != "legacy-model" {
		t.Fatalf("migrated config not loaded under WUU_HOME: %+v", cfg.Providers["main"])
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected migrated config under WUU_HOME: %v", err)
	}
}
