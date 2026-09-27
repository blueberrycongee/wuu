package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPTCOptInAndFamilyPersistence(t *testing.T) {
	var defaults PTCConfig
	if defaults.EnabledFor("gpt") {
		t.Fatal("PTC must default off")
	}
	cfg := PTCConfig{Enabled: true, Families: map[string]bool{"claude": false}}
	if !cfg.EnabledFor("gpt") || cfg.EnabledFor("claude") {
		t.Fatal("family override was ignored")
	}
	cfg.Enabled = false
	cfg.Families["deepseek"] = true
	if cfg.EnabledFor("gpt") || !cfg.EnabledFor("deepseek") {
		t.Fatal("family opt-in was ignored")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"code_mode":{"enabled":true,"host_path":"/retired/host"},"ptc":{"node_executable":"/custom/node"},"agent":{"max_steps":12},"default_provider":"test","providers":{"test":{"type":"openai","base_url":"https://example.test","model":"gpt-test"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, _, err := LoadPath(path)
	if err != nil || legacy.PTC.EnabledFor("gpt") {
		t.Fatalf("legacy config activated PTC: %+v %v", legacy.PTC, err)
	}
	if err := UpdateGeneralSettings(path, GeneralSettingsUpdate{PTC: &cfg}); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.PTC.EnabledFor("deepseek") || loaded.PTC.NodeExecutable != "/custom/node" || len(loaded.LegacyCodeMode) != 0 {
		t.Fatalf("lost persisted settings: %+v", loaded.PTC)
	}
}
