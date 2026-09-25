package exec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestLoadExecConfigUsesExplicitConfigPath(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, "custom-wuu.json")
	writeExecTestConfig(t, configPath, "explicit")

	cfg, loadedPath, err := loadExecConfig(workdir, t.TempDir(), Options{ConfigPath: "custom-wuu.json"})
	if err != nil {
		t.Fatalf("loadExecConfig: %v", err)
	}
	if loadedPath != configPath {
		t.Fatalf("loadedPath = %q, want %q", loadedPath, configPath)
	}
	if cfg.DefaultProvider != "explicit" {
		t.Fatalf("DefaultProvider = %q", cfg.DefaultProvider)
	}
}

func TestLoadExecConfigCanIgnoreUserConfig(t *testing.T) {
	workdir := t.TempDir()
	home := t.TempDir()
	userConfig := filepath.Join(home, ".config", "wuu", "config.json")
	writeExecTestConfig(t, userConfig, "user")

	cfg, _, err := loadExecConfig(workdir, home, Options{})
	if err != nil {
		t.Fatalf("loadExecConfig: %v", err)
	}
	if cfg.DefaultProvider != "user" {
		t.Fatalf("DefaultProvider = %q", cfg.DefaultProvider)
	}

	_, _, err = loadExecConfig(workdir, home, Options{IgnoreUserConfig: true})
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Fatalf("loadExecConfig ignore user error = %v, want ErrConfigNotFound", err)
	}
}

func TestApplyConfigOverridesRejectsNegativeMaxTurns(t *testing.T) {
	cfg := config.Default()
	if err := applyConfigOverrides(&cfg, Options{MaxTurns: -1}); err == nil {
		t.Fatal("expected negative max turns error")
	}
}

func TestApplyRunEnvSetsAndRestoresValues(t *testing.T) {
	const existingKey = "WUU_EXEC_EXISTING_ENV_TEST"
	const newKey = "WUU_EXEC_NEW_ENV_TEST"
	t.Setenv(existingKey, "old")
	os.Unsetenv(newKey)

	restore, err := applyRunEnv([]string{existingKey + "=new", newKey + "=value"})
	if err != nil {
		t.Fatalf("applyRunEnv: %v", err)
	}
	if got := os.Getenv(existingKey); got != "new" {
		t.Fatalf("%s = %q", existingKey, got)
	}
	if got := os.Getenv(newKey); got != "value" {
		t.Fatalf("%s = %q", newKey, got)
	}
	restore()
	if got := os.Getenv(existingKey); got != "old" {
		t.Fatalf("%s after restore = %q", existingKey, got)
	}
	if _, ok := os.LookupEnv(newKey); ok {
		t.Fatalf("%s should be unset after restore", newKey)
	}
}

func TestApplyRunEnvRejectsInvalidEntry(t *testing.T) {
	if _, err := applyRunEnv([]string{"not-an-assignment"}); err == nil {
		t.Fatal("expected invalid env assignment error")
	}
}

func writeExecTestConfig(t *testing.T, path, providerName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	data := []byte(`{
  "default_provider": "` + providerName + `",
  "providers": {
    "` + providerName + `": {
      "type": "openai-compatible",
      "base_url": "https://example.invalid/v1",
      "model": "test-model"
    }
  }
}
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
