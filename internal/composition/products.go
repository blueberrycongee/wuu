package composition

// DefaultConfig selects a shipped product using the ordinary profile resolver.
// Callers use it only when no user composition exists. Explicit configurations,
// including empty module sets, always take precedence.
func DefaultConfig(profile string, discovered ...string) *Config {
	cfg := &Config{Version: Version, Profile: profile}
	if len(discovered) > 0 {
		modules := make(map[string]bool, len(discovered))
		for _, id := range discovered {
			modules[id] = true
		}
		cfg.Patches = []Patch{{Modules: modules}}
	}
	return cfg
}

// productCatalogVersion returns owned values and preserves the defaults used
// by each supported configuration version. User definitions replace named entries.
func productCatalogVersion(version int) (map[string]Profile, map[string]Bundle) {
	base := map[string]bool{}
	for _, id := range []string{
		"kernel", "wuu.default-loop", "wuu.session-execution", "wuu.tool-execution",
		"wuu.session.default", "wuu.title.default", "wuu.tools.default", "wuu.prompt.default", "wuu.compaction.default",
		"wuu.session-state", "wuu.state.sqlite", "wuu.state-consumer", "wuu.credentials.os",
		"wuu.settings.local", "wuu.config.local", "wuu.permissions.default",
		"wuu.artifacts.local", "wuu.index.trace",
	} {
		base[id] = true
	}
	desktop := map[string]bool{"wuu.default-shell": true, "wuu.default-settings": true, "wuu.default-navigation": true, "wuu.default-conversation": true, "wuu.default-preview": true, "wuu.default-composer": true}
	if version >= 3 {
		base["wuu.agent.policy.default"] = true
		base["wuu.model.native"] = true
		base["wuu.model-execution"] = true
		base["wuu.model.capacity.default"] = true
		base["wuu.model.capacity.resources"] = true
		base["wuu.engines.native"] = true
		base["wuu.engine-execution"] = true
		desktop["wuu.default-application"] = true
	}
	return map[string]Profile{
		"desktop":  {Bundles: []string{"wuu.base", "wuu.desktop"}},
		"headless": {Bundles: []string{"wuu.base"}},
	}, map[string]Bundle{
		"wuu.base":    {Patches: []Patch{{Modules: base}}},
		"wuu.desktop": {Patches: []Patch{{Modules: desktop}}},
	}
}
