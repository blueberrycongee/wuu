package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolEngineSettingsMergeAndDisableDefault(t *testing.T) {
	for _, id := range []string{"cursor", "devin", "grok", "hermes", "pi", "opencode", "antigravity"} {
		t.Run(id, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			base := `{"default_provider":"home","providers":{"home":{"type":"openai","base_url":"http://local","model":"m","api_key_env":"K"}},"engines":{"claude":{"binary_path":"preserved"}}}`
			if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
				t.Fatal(err)
			}
			apply := func(payload string) {
				t.Helper()
				var update EnginesSettingsUpdate
				if err := json.Unmarshal([]byte(payload), &update); err != nil {
					t.Fatal(err)
				}
				if err := UpdateEnginesSettings(path, update); err != nil {
					t.Fatal(err)
				}
			}
			apply(`{"default_engine":"` + id + `","` + id + `":{"enabled":true,"binary_path":"custom-agent"}}`)
			cfg, _, err := LoadPath(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Engines.DefaultEngine != id || cfg.Engines.Binary(id) == nil || cfg.Engines.Binary(id).BinaryPath != "custom-agent" {
				t.Fatalf("engine selection did not survive reload: %+v", cfg.Engines)
			}
			apply(`{"` + id + `":{"enabled":false}}`)
			cfg, _, err = LoadPath(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Engines.DefaultEngine != "" || cfg.Engines.Binary(id).BinaryPath != "custom-agent" || cfg.Engines.Claude.BinaryPath != "preserved" {
				t.Fatalf("disable lost settings or retained unavailable default: %+v", cfg.Engines)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DefaultProvider != "home" || cfg.Providers["home"].Model != "m" {
				t.Fatal("unrelated settings lost")
			}
			if err := UpdateEnginesSettings(path, EnginesSettingsUpdate{DefaultEngine: &id}); err == nil {
				t.Fatal("disabled default accepted")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Fatal("rejected update changed persisted settings")
			}
		})
	}
}

func TestUpdateEnginesSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "settings.json")
	base := `{"default_provider": "home", "providers": {"home": {"type": "openai", "base_url": "http://local", "model": "m", "api_key_env": "K"}}}`
	if err := os.WriteFile(configPath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}

	disabled := false
	defaultEngine := "codex"
	binaryPath := "/opt/codex/bin/codex"
	if err := UpdateEnginesSettings(configPath, EnginesSettingsUpdate{
		DefaultEngine: &defaultEngine,
		Codex: &EngineBinaryUpdate{
			Enabled:    &disabled,
			BinaryPath: &binaryPath,
		},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	cfg, _, err := LoadPath(configPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Engines == nil {
		t.Fatal("engines section missing after update")
	}
	if cfg.Engines.DefaultEngine != "" {
		t.Fatalf("disabled default engine = %q, want wuu fallback", cfg.Engines.DefaultEngine)
	}
	if cfg.Engines.Codex == nil {
		t.Fatal("codex section missing")
	}
	if cfg.Engines.Codex.Enabled == nil || *cfg.Engines.Codex.Enabled {
		t.Fatalf("codex enabled = %v, want explicit false", cfg.Engines.Codex.Enabled)
	}
	if cfg.Engines.Codex.BinaryPath != "/opt/codex/bin/codex" {
		t.Fatalf("binary path = %q", cfg.Engines.Codex.BinaryPath)
	}

	// Re-enable while preserving the existing path override.
	enabled := true
	if err := UpdateEnginesSettings(configPath, EnginesSettingsUpdate{
		Codex: &EngineBinaryUpdate{Enabled: &enabled},
	}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	cfg, _, err = LoadPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Engines.Codex.Enabled == nil || !*cfg.Engines.Codex.Enabled {
		t.Fatalf("codex enabled after re-enable = %v", cfg.Engines.Codex.Enabled)
	}
	// binary_path persists (empty update leaves it untouched).
	if cfg.Engines.Codex.BinaryPath != "/opt/codex/bin/codex" {
		t.Fatalf("binary path lost = %q", cfg.Engines.Codex.BinaryPath)
	}

	// An explicit empty path clears the override and restores PATH lookup.
	emptyPath := ""
	if err := UpdateEnginesSettings(configPath, EnginesSettingsUpdate{
		Codex: &EngineBinaryUpdate{BinaryPath: &emptyPath},
	}); err != nil {
		t.Fatalf("clear binary path: %v", err)
	}
	cfg, _, err = LoadPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Engines.Codex.BinaryPath != "" {
		t.Fatalf("binary path after clear = %q", cfg.Engines.Codex.BinaryPath)
	}
}

func TestEnginesConfigRejectsUnknownOrDisabledDefault(t *testing.T) {
	base := Default()
	base.Engines = &EnginesConfig{DefaultEngine: "other"}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Validate unknown default = %v", err)
	}
	disabled := false
	base.Engines = &EnginesConfig{
		DefaultEngine: "claude",
		Claude:        &EngineBinaryConfig{Enabled: &disabled},
	}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Validate disabled default = %v", err)
	}
}

func TestEngineEnabledSemantics(t *testing.T) {
	var c *EngineBinaryConfig
	if enabled, explicit := c.EngineEnabled(); !enabled || explicit {
		t.Fatalf("nil config = enabled %v explicit %v, want true/false", enabled, explicit)
	}
	falseValue := false
	c = &EngineBinaryConfig{Enabled: &falseValue}
	if enabled, explicit := c.EngineEnabled(); enabled || !explicit {
		t.Fatalf("explicit false = enabled %v explicit %v", enabled, explicit)
	}
}
