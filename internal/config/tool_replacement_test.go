package config

import (
	"encoding/json"
	"os"
	"testing"
)

func TestToolReplacementConfiguration(t *testing.T) {
	var cfg Config
	raw := []byte(`{"agent":{"tool_replacements":{"read_file":{"plugin_id":"custom","tool_id":"reader"}}}}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.Agent.ToolReplacements["read_file"]
	if got.PluginID != "custom" || got.ToolID != "reader" {
		t.Fatalf("replacement lost: %+v", got)
	}
}

func TestToolReplacementValidation(t *testing.T) {
	tests := []struct {
		name     string
		bindings map[string]ToolReplacement
	}{
		{"empty name", map[string]ToolReplacement{"": {PluginID: "p", ToolID: "a"}}},
		{"empty owner", map[string]ToolReplacement{"read_file": {ToolID: "a"}}},
		{"empty tool", map[string]ToolReplacement{"read_file": {PluginID: "p"}}},
		{"ambiguous provider", map[string]ToolReplacement{"read_file": {PluginID: "p", ToolID: "a"}, "write_file": {PluginID: "p", ToolID: "a"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Agent.ToolReplacements = tc.bindings
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid bindings accepted")
			}
		})
	}
}

func TestToolReplacementConfigurationRejectsProjectOverride(t *testing.T) {
	home := isolatedHome(t)
	root := t.TempDir()
	base := `{"default_provider":"main","providers":{"main":{"type":"openai-compatible","base_url":"https://example.test","model":"test"}},"agent":{"tool_replacements":{"web_search":{"plugin_id":"trusted","tool_id":"search"}}}}`
	writeBaseConfig(t, home, base)
	writeProjectSettings(t, root, sharedSettingsFile, `{"agent":{"tool_replacements":{"web_search":{"plugin_id":"other","tool_id":"bad"}}}}`)
	cfg, _, err := LoadFrom(root, home)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Agent.ToolReplacements["web_search"]
	if got.PluginID != "trusted" || got.ToolID != "search" {
		t.Fatalf("project redirected trusted tool: %+v", got)
	}
}
func TestToolReplacementCanBeClearedInUserConfig(t *testing.T) {
	home := isolatedHome(t)
	root := t.TempDir()
	base := `{"default_provider":"main","providers":{"main":{"type":"openai-compatible","base_url":"https://example.test","model":"test"}},"agent":{"tool_replacements":{"web_search":{"plugin_id":"trusted","tool_id":"search"}}}}`
	path := writeBaseConfig(t, home, base)
	cfg, _, err := LoadFrom(root, home)
	if err != nil || len(cfg.Agent.ToolReplacements) != 1 {
		t.Fatalf("load: %+v %v", cfg.Agent.ToolReplacements, err)
	}
	clear := `{"default_provider":"main","providers":{"main":{"type":"openai-compatible","base_url":"https://example.test","model":"test"}},"agent":{"tool_replacements":null}}`
	if err := os.WriteFile(path, []byte(clear), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = LoadFrom(root, home)
	if err != nil || len(cfg.Agent.ToolReplacements) != 0 {
		t.Fatalf("clear: %+v %v", cfg.Agent.ToolReplacements, err)
	}
}
