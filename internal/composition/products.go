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
		base["wuu.tools.v1-batch"] = true
		base["wuu.agent.policy.default"] = true
		base["wuu.model.native"] = true
		base["wuu.model-execution"] = true
		base["wuu.model.capacity.default"] = true
		base["wuu.model.capacity.resources"] = true
		base["wuu.engines.native"] = true
		base["wuu.engine-execution"] = true
		desktop["wuu.default-application"] = true
	}
	if version >= 4 {
		// The API consumer can use an installed provider; no sharing business
		// implementation is activated by the default product.
		base["wuu.session-sharing-access"] = true
	}
	profiles := map[string]Profile{
		"desktop":  {Bundles: []string{"wuu.base", "wuu.desktop"}},
		"headless": {Bundles: []string{"wuu.base"}},
	}
	bundles := map[string]Bundle{
		"wuu.base":    {Patches: []Patch{{Modules: base}}},
		"wuu.desktop": {Patches: []Patch{{Modules: desktop}}},
	}
	// Personal is an explicitly selected product, not an identity-triggered
	// prompt override. Ordinary bindings and patches can replace its policy.
	bundles["wuu.personal"] = Bundle{Patches: []Patch{{
		Modules:  map[string]bool{"wuu.client-input-receipts": true, "personal": true, "personal-calendar": true, "automation": true, "wuu.personal-actions-consumer": true, "wuu.resource-access": true, "wuu.resource-policy.default": true, "wuu.execution-access": true, "wuu.execution-authorization.default": true, "wuu.prompt.default": false},
		Services: []Binding{{Service: "agent.prompt", Major: 1, Provider: "personal"}},
	}}}
	profiles["personal"] = Profile{Bundles: []string{"wuu.base", "wuu.desktop", "wuu.personal"}}
	if version >= 4 {
		// The default product extends this same host/state graph; minimum
		// compositions do not select unrelated execution or business providers.
		host := map[string]bool{
			"kernel": true, "wuu.state-consumer": true, "wuu.config.local": true,
			"wuu.session-state": true, "wuu.state.sqlite": true, "wuu.session.default": true,
			"wuu.engine-execution": true,
			"wuu.resource-access":  true, "wuu.resource-policy.default": true,
		}
		for id := range host {
			delete(base, id)
		}
		bundles["wuu.session-host"] = Bundle{Patches: []Patch{{Modules: host}}}
		bundles["wuu.execution"] = Bundle{Patches: []Patch{{Modules: base}}}
		// Keep the public complete-base bundle usable in existing custom
		// profiles; product profiles expose its two independently usable parts.
		bundles["wuu.base"] = Bundle{Patches: []Patch{{Modules: host}, {Modules: base}}}
		profiles["desktop"] = Profile{Bundles: []string{"wuu.session-host", "wuu.execution", "wuu.desktop"}}
		profiles["headless"] = Profile{Bundles: []string{"wuu.session-host", "wuu.execution"}}
		profiles["minimal"] = Profile{Bundles: []string{"wuu.session-host"}}

		profiles["personal"] = Profile{Bundles: []string{"wuu.session-host", "wuu.execution", "wuu.desktop", "wuu.personal"}}
	}
	return profiles, bundles
}
