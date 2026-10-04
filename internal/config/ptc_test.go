package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPTCDefaultAndPersistence(t *testing.T) {
	defaults := Default().PTC
	if !defaults.Enabled {
		t.Fatal("PTC must default on")
	}
	cfg := PTCConfig{Enabled: false}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"code_mode":{"enabled":true,"host_path":"/retired/host"},"ptc":{"node_executable":"/custom/node"},"agent":{"max_steps":12},"default_provider":"test","providers":{"test":{"type":"openai","base_url":"https://example.test","model":"gpt-test"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, _, err := LoadPath(path)
	if err != nil || !legacy.PTC.Enabled || legacy.PTC.NodeExecutable != "/custom/node" {
		t.Fatalf("omitted switch did not use isolated PTC default: %+v %v", legacy.PTC, err)
	}
	if err := UpdateGeneralSettings(path, GeneralSettingsUpdate{PTC: &cfg}); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := LoadPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PTC.Enabled || loaded.PTC.NodeExecutable != "/custom/node" || len(loaded.LegacyCodeMode) != 0 {
		t.Fatalf("lost persisted settings: %+v", loaded.PTC)
	}
}

func TestPTCOmittedSettingsAndExplicitOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, setting string
		enabled       bool
	}{
		{"omitted", "", true},
		{"runtime only", `,"ptc":{"node_executable":"node"}`, true},
		{"explicit off", `,"ptc":{"enabled":false}`, false},
		{"explicit on", `,"ptc":{"enabled":true}`, true},
		{"retired family off", `,"ptc":{"enabled":true,"families":{"gpt":false}}`, true},
		{"retired family on", `,"ptc":{"enabled":false,"families":{"gpt":true}}`, false},
		{"retired off", `,"code_mode":{"enabled":false}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			raw := `{"default_provider":"test","providers":{"test":{"type":"openai","base_url":"https://example.test","model":"gpt-test"}}` + tc.setting + `}`
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := LoadPath(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PTC.Enabled != tc.enabled {
				t.Fatalf("PTC=%+v", cfg.PTC)
			}
			if err := UpdateGeneralSettings(path, GeneralSettingsUpdate{PTC: &cfg.PTC}); err != nil {
				t.Fatal(err)
			}
			again, _, err := LoadPath(path)
			if err != nil || again.PTC.Enabled != tc.enabled {
				t.Fatalf("round trip=%+v, %v", again.PTC, err)
			}
		})
	}
}
