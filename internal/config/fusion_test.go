package config

import "testing"

func TestFusionValidatesExplicitExecutablePair(t *testing.T) {
	lead := ModelRoleConfig{Provider: "one", Model: "lead"}
	sidekick := ModelRoleConfig{Provider: "two", Model: "worker"}
	for _, tc := range []struct {
		name      string
		edit      func(*Config)
		wantError bool
	}{
		{"cross-provider pair", func(*Config) {}, false},
		{"missing provider", func(c *Config) { c.Agent.Fusion.Sidekick.Provider = "missing" }, true},
		{"missing model", func(c *Config) { c.Agent.Fusion.Lead.Model = "" }, true},
		{"padded recursive pair", func(c *Config) { c.Agent.Fusion.Sidekick.Model = " " + FusionID + " " }, true},
		{"recursive pair", func(c *Config) { c.Agent.Fusion.Sidekick.Model = FusionID }, true},
		{"disabled model", func(c *Config) {
			p := c.Providers["two"]
			p.Models = map[string]ProviderModelConfig{"worker": {Disabled: true}}
			c.Providers["two"] = p
		}, true},
		{"local ID upstream", func(c *Config) { p := c.Providers["one"]; p.Model = FusionID; c.Providers["one"] = p }, true},
		{"disabled stale settings", func(c *Config) { c.Agent.Fusion.Enabled = false; c.Agent.Fusion.Sidekick.Provider = "removed" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			cfg.Providers = map[string]ProviderConfig{"one": {Model: "lead"}, "two": {Model: "worker"}}
			cfg.Agent.Fusion = &FusionConfig{Enabled: true, Lead: lead, Sidekick: sidekick}
			tc.edit(&cfg)
			if err := cfg.ValidateFusion(); (err != nil) != tc.wantError {
				t.Fatalf("ValidateFusion()=%v", err)
			}
		})
	}
}
