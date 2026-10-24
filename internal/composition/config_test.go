package composition

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProductProfilesShareOverrides(t *testing.T) {
	for _, profile := range []string{"desktop", "headless"} {
		t.Run(profile, func(t *testing.T) {
			cfg := DefaultConfig(profile)
			cfg.Patches = []Patch{{Modules: map[string]bool{"wuu.state.sqlite": false, "example.documents": true}, Services: []Binding{{Service: "state.documents", Major: 1, Provider: "example.documents"}}}}
			got, err := cfg.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if got.Includes("wuu.state.sqlite") || !got.Includes("example.documents") {
				t.Fatalf("replacement not applied: %+v", got)
			}
			if got.Includes("wuu.default-shell") != (profile == "desktop") {
				t.Fatal("incorrect UI selection")
			}
			if got.SourceForModule("example.documents") != "composition:patch:0" {
				t.Fatal("missing provenance")
			}
			if err := got.ValidateAvailable([]string{"example.documents"}); err == nil {
				t.Fatal("unavailable defaults accepted")
			}
			fresh, err := DefaultConfig(profile).Resolve()
			if err != nil || !fresh.Includes("wuu.state.sqlite") {
				t.Fatal("catalog was mutated", err)
			}
		})
	}
}

func TestCustomProfileOverridesShippedProfile(t *testing.T) {
	cfg := DefaultConfig("desktop")
	cfg.Profiles = map[string]Profile{"desktop": {Modules: []string{"custom"}}}
	got, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(*got.Modules) != 1 || !got.Includes("custom") {
		t.Fatalf("custom profile changed: %+v", got)
	}
	cfg.Patches = []Patch{{Modules: map[string]bool{"custom": false}}}
	got, err = cfg.Resolve()
	if err != nil || len(*got.Modules) != 0 {
		t.Fatal("removal restored defaults", err)
	}
}

func TestCompositionJSONRejectsAmbiguousSelections(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"moduels":[]}`,
		`{"version":2,"modules":null}`,
		`{"version":2,"modules":[],"modules":["oops"]}`,
		`{"version":2,"profiles":{"test":{"modules":null}}}`,
		`{"version":2,"patches":[{"module":{"oops":true}}]}`,
		`{"version":2,"services":[{"service":"state.documents","major":1,"provider":"a","source":"fake"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var cfg Config
			if err := json.Unmarshal([]byte(raw), &cfg); err == nil {
				t.Fatal("ambiguous selection accepted")
			}
		})
	}
	var cfg Config
	if err := json.Unmarshal([]byte(`{"version":2,"modules":[]}`), &cfg); err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Resolve()
	if err != nil || got.Modules == nil || len(*got.Modules) != 0 {
		t.Fatal("explicit empty modules lost", err)
	}
}

func TestExcludedBindingReportsSource(t *testing.T) {
	cfg := &Config{Version: Version, Modules: &[]string{}, Services: []Binding{{Service: "example.service", Major: 1, Provider: "missing"}}}
	_, err := cfg.Resolve()
	if err == nil || !strings.Contains(err.Error(), "composition") {
		t.Fatal("missing selection diagnostic", err)
	}
}

func TestDefaultCompositionPreservesDiscoveredPlugins(t *testing.T) {
	cfg := DefaultConfig("headless", "installed.one", "installed.two")
	got, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Includes("installed.one") || !got.Includes("installed.two") || got.Includes("undiscovered") {
		t.Fatal("default discovery set changed")
	}
	cfg.Patches = append(cfg.Patches, Patch{Modules: map[string]bool{"installed.one": false}})
	got, err = cfg.Resolve()
	if err != nil || got.Includes("installed.one") {
		t.Fatal("removed plugin restored", err)
	}
}

func TestNullNonSelectionFieldsRemainCompatible(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"version":2,"modules":[],"services":null,"profiles":null,"bundles":null,"patches":null}`), &cfg); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyProviderReplacementAcrossProductLayers(t *testing.T) {
	for _, profile := range []string{"desktop", "headless", "custom"} {
		t.Run(profile, func(t *testing.T) {
			cfg := DefaultConfig(profile)
			cfg.Profiles = map[string]Profile{"custom": {Bundles: []string{"wuu.base", "custom.policy"}}}
			cfg.Bundles = map[string]Bundle{"custom.policy": {Patches: []Patch{{
				Modules:  map[string]bool{"wuu.prompt.default": false, "example.prompt": true},
				Services: []Binding{{Service: "agent.prompt", Major: 1, Provider: "example.prompt"}},
			}}}}
			before, err := cfg.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if !before.Includes("wuu.compaction.default") || before.Includes("wuu.prompt.default") != (profile != "custom") {
				t.Fatal("product did not apply base and custom policy layers")
			}
			cfg.Patches = []Patch{{
				Modules: map[string]bool{"wuu.prompt.default": false, "example.prompt": false, "wuu.compaction.default": false, "replacement.policy": true},
				Services: []Binding{
					{Service: "agent.prompt", Major: 1, Provider: "replacement.policy"},
					{Service: "agent.compaction", Major: 1, Provider: "replacement.policy"},
				},
			}}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var saved Config
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			after, err := saved.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if after.Includes("wuu.prompt.default") || after.Includes("wuu.compaction.default") || after.Includes("example.prompt") || !after.Includes("replacement.policy") {
				t.Fatal("saved replacement restored a removed policy provider")
			}
			if len(after.Services) != 2 {
				t.Fatalf("bindings: %+v", after.Services)
			}
			for _, binding := range after.Services {
				if binding.Provider != "replacement.policy" || binding.Source != "composition:patch:0" {
					t.Fatalf("wrong selected provider or provenance: %+v", binding)
				}
			}
			saved.Patches = append(saved.Patches, Patch{Modules: map[string]bool{"replacement.policy": false}})
			if _, err := saved.Resolve(); err == nil {
				t.Fatal("removing bound policy silently restored a default")
			}
			saved.Patches[len(saved.Patches)-1].RemoveServices = []ServiceKey{{Service: "agent.prompt", Major: 1}, {Service: "agent.compaction", Major: 1}}
			empty, err := saved.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if len(empty.Services) != 0 || empty.Includes("wuu.prompt.default") || empty.Includes("wuu.compaction.default") {
				t.Fatal("explicit policy removal restored defaults")
			}
		})
	}
}

func TestDesktopSettingsCanBeRemovedWithoutShellRemoval(t *testing.T) {
	desktop, err := DefaultConfig("desktop").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !desktop.Includes("wuu.default-settings") || !desktop.Includes("wuu.default-navigation") {
		t.Fatal("desktop has no settings provider")
	}
	headless, err := DefaultConfig("headless").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if headless.Includes("wuu.default-settings") || headless.Includes("wuu.default-navigation") {
		t.Fatal("headless activated a settings renderer")
	}
	cfg := DefaultConfig("desktop")
	cfg.Patches = []Patch{{Modules: map[string]bool{"wuu.default-settings": false, "example.settings": true}, Services: []Binding{{Service: "ui.settings", Major: 1, Provider: "example.settings"}}}}
	resolved, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Includes("wuu.default-settings") || !resolved.Includes("wuu.default-shell") || !resolved.Includes("example.settings") {
		t.Fatal("settings replacement changed shell selection")
	}
}
