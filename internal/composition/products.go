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

// productCatalog creates owned values so resolving or editing one product cannot
// mutate another. User definitions replace a whole named profile or bundle.
func productCatalog() (map[string]Profile, map[string]Bundle) {
	base := map[string]bool{}
	for _, id := range []string{
		"kernel", "wuu.default-loop", "wuu.session-execution", "wuu.tool-execution",
		"wuu.session.default", "wuu.tools.default", "wuu.prompt.default", "wuu.compaction.default",
		"wuu.session-state", "wuu.state.sqlite", "wuu.state-consumer", "wuu.credentials.os",
		"wuu.settings.local", "wuu.config.local", "wuu.permissions.default",
		"wuu.artifacts.local", "wuu.index.trace",
	} {
		base[id] = true
	}
	return map[string]Profile{
			"desktop":  {Bundles: []string{"wuu.base", "wuu.desktop"}},
			"headless": {Bundles: []string{"wuu.base"}},
		}, map[string]Bundle{
			"wuu.base":    {Patches: []Patch{{Modules: base}}},
			"wuu.desktop": {Patches: []Patch{{Modules: map[string]bool{"wuu.default-shell": true}}}},
		}
}
