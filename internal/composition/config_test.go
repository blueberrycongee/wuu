package composition

import (
	"encoding/json"
	"os"
	"reflect"
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

func TestDesktopContentRenderersCanBeReplacedIndependently(t *testing.T) {
	for _, surface := range []string{"conversation", "preview", "composer"} {
		t.Run(surface, func(t *testing.T) {
			module := "wuu.default-" + surface
			desktop, err := DefaultConfig("desktop").Resolve()
			if err != nil {
				t.Fatal(err)
			}
			headless, err := DefaultConfig("headless").Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if !desktop.Includes(module) || headless.Includes(module) {
				t.Fatal("incorrect product renderer selection")
			}
			cfg := DefaultConfig("desktop")
			cfg.Patches = []Patch{{Modules: map[string]bool{module: false, "example.surface": true}, Services: []Binding{{Service: "ui." + surface, Major: 1, Provider: "example.surface"}}}}
			resolved, err := cfg.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Includes(module) || !resolved.Includes("wuu.default-shell") || !resolved.Includes("example.surface") {
				t.Fatal("surface replacement changed shell selection")
			}
		})
	}
}

// Compatibility projection must preserve former behavior without rewriting the
// user's source or defeating a deliberate replacement/removal.
func TestLegacyCompositionAddsExtractedProviders(t *testing.T) {
	for _, version := range []int{1, 2} {
		cfg := &Config{Version: version, Modules: &[]string{"wuu.session-execution", "wuu.default-shell"}}
		original, _ := json.Marshal(cfg)
		resolved, err := cfg.Resolve()
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"wuu.agent.policy.default", "wuu.default-application", "wuu.model.native"} {
			if !resolved.Includes(id) || !strings.HasPrefix(resolved.SourceForModule(id), "migration:") {
				t.Fatalf("missing migration for %s: %+v", id, resolved)
			}
		}
		after, _ := json.Marshal(cfg)
		if string(original) != string(after) {
			t.Fatal("resolution rewrote recoverable configuration")
		}
		again, err := cfg.Resolve()
		if err != nil || len(again.Layers) != len(resolved.Layers) {
			t.Fatal("migration accumulates", err)
		}
	}
}

func TestExtractedProviderMigrationHonorsExplicitChoices(t *testing.T) {
	for _, tc := range []struct {
		name                string
		cfg                 Config
		policy, application bool
	}{
		{"current intentional omission", Config{Version: Version, Modules: &[]string{"wuu.session-execution", "wuu.default-shell"}}, false, false},
		{"empty legacy", Config{Version: 2, Modules: &[]string{}}, false, false},
		{"explicit legacy removal", Config{Version: 2, Modules: &[]string{"wuu.session-execution", "wuu.default-shell"}, Patches: []Patch{{Modules: map[string]bool{"wuu.agent.policy.default": false, "wuu.default-application": false}}}}, false, false},
		{"external replacement", Config{Version: 2, Modules: &[]string{"wuu.session-execution", "wuu.default-shell", "replacement"}, Services: []Binding{{Service: "agent.policy", Major: 1, Provider: "replacement"}, {Service: "ui.application", Major: 1, Provider: "replacement"}}}, false, false},
		{"headless legacy", Config{Version: 2, Profile: "headless"}, true, false},
		{"desktop legacy", Config{Version: 2, Profile: "desktop"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := tc.cfg.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Includes("wuu.agent.policy.default") != tc.policy || resolved.Includes("wuu.default-application") != tc.application {
				t.Fatalf("changed explicit choices: %+v", resolved)
			}
		})
	}
}

func TestLegacyModelAdapterMigrationPreservesExternalSelection(t *testing.T) {
	cfg := Config{Version: 2, Modules: &[]string{"wuu.session-execution", "external.model"}, Services: []Binding{{Service: "model.adapter", Major: 1, Provider: "external.model"}}}
	resolved, err := cfg.Resolve()
	if err != nil || resolved.Includes("wuu.model.native") {
		t.Fatalf("external model replaced: %+v %v", resolved, err)
	}
	cfg.Services = nil
	cfg.Patches = []Patch{{Modules: map[string]bool{"wuu.model.native": false}}}
	resolved, err = cfg.Resolve()
	if err != nil || resolved.Includes("wuu.model.native") {
		t.Fatalf("removed native model restored: %+v %v", resolved, err)
	}
	cfg.Version = Version
	cfg.Patches = nil
	resolved, err = cfg.Resolve()
	if err != nil || resolved.Includes("wuu.model.native") {
		t.Fatalf("current schema repaired missing model: %+v %v", resolved, err)
	}
}

func TestLegacyModelOnlyCompositionKeepsExplicitConsumer(t *testing.T) {
	cfg := Config{Version: 2, Modules: &[]string{"kernel", "external.model"}, Services: []Binding{{Service: "model.adapter", Major: 1, Provider: "external.model"}}}
	got, err := cfg.Resolve()
	if err != nil || !got.Includes("wuu.model-execution") || got.Includes("wuu.model.native") {
		t.Fatalf("model-only external selection: %+v %v", got, err)
	}
	cfg.Patches = []Patch{{Modules: map[string]bool{"wuu.model-execution": false}}}
	got, err = cfg.Resolve()
	if err != nil || got.Includes("wuu.model-execution") {
		t.Fatalf("explicit model consumer removal lost: %+v %v", got, err)
	}
}

func TestLegacyEngineProvidersPreserveExplicitExclusions(t *testing.T) {
	cfg := Config{Version: 2, Modules: &[]string{"wuu.session-execution", "external.engine"}, Services: []Binding{{Service: "engine.wuu", Major: 1, Provider: "external.engine"}}}
	got, err := cfg.Resolve()
	if err != nil || !got.Includes("wuu.engine-execution") || !got.Includes("wuu.engines.native") {
		t.Fatalf("legacy engine services lost: %+v %v", got, err)
	}
	cfg.Patches = []Patch{{Modules: map[string]bool{"wuu.engines.native": false, "wuu.engine-execution": false}}}
	got, err = cfg.Resolve()
	if err != nil || got.Includes("wuu.engines.native") || got.Includes("wuu.engine-execution") {
		t.Fatalf("explicit engine exclusions lost: %+v %v", got, err)
	}
	cfg.Version = Version
	cfg.Patches = nil
	got, err = cfg.Resolve()
	if err != nil || got.Includes("wuu.engines.native") || got.Includes("wuu.engine-execution") {
		t.Fatalf("current config received hidden defaults: %+v %v", got, err)
	}
}

func TestLegacyCapacityBindingsPreserveReplacement(t *testing.T) {
	cfg := Config{Version: 2, Modules: &[]string{"wuu.session-execution", "capacity"}, Services: []Binding{{Service: "model.capacity", Major: 1, Provider: "capacity"}}}
	got, err := cfg.Resolve()
	if err != nil || got.Includes("wuu.model.capacity.default") || !got.Includes("wuu.model.capacity.resources") {
		t.Fatalf("capacity replacement: %+v %v", got, err)
	}
	cfg.Patches = []Patch{{Modules: map[string]bool{"wuu.model.capacity.resources": false}}}
	got, err = cfg.Resolve()
	if err != nil || got.Includes("wuu.model.capacity.resources") {
		t.Fatalf("excluded shared resource restored: %+v %v", got, err)
	}
}

func TestLegacyToolBatchAdapterPreservesExplicitReplacement(t *testing.T) {
	for _, version := range []int{1, 2} {
		modules := []string{"wuu.tool-execution", "wuu.tools.default"}
		resolved, err := (&Config{Version: version, Modules: &modules}).Resolve()
		if err != nil || !resolved.Includes("wuu.tools.v1-batch") {
			t.Fatalf("v%d legacy batch adapter: %+v %v", version, resolved, err)
		}
		custom := []string{"wuu.tool-execution", "external.batch"}
		resolved, err = (&Config{Version: version, Modules: &custom, Services: []Binding{{Service: "agent.tool.batch", Major: 1, Provider: "external.batch"}}}).Resolve()
		if err != nil || resolved.Includes("wuu.tools.v1-batch") {
			t.Fatalf("v%d replaced batch adapter: %+v %v", version, resolved, err)
		}
	}
}

// Legacy products acquire the newly explicit admission boundary, but a chosen
// replacement or exclusion must never be repaired into the native policy.
func TestLegacyResourcePolicySelection(t *testing.T) {
	for _, version := range []int{1, 2} {
		cfg := &Config{Version: version, Modules: &[]string{"kernel"}}
		got, err := cfg.Resolve()
		if err != nil || !got.Includes("wuu.resource-access") || !got.Includes("wuu.resource-policy.default") {
			t.Fatalf("legacy admission boundary: %+v %v", got, err)
		}
		if !strings.HasPrefix(got.SourceForModule("wuu.resource-access"), "migration:") {
			t.Fatal("auth migration lost provenance")
		}
		cfg.Modules = &[]string{"kernel", "example.resource-policy"}
		cfg.Services = []Binding{{Service: "resource.authorization", Major: 1, Provider: "example.resource-policy"}}
		got, err = cfg.Resolve()
		if err != nil || got.Includes("wuu.resource-policy.default") || !got.Includes("wuu.resource-access") {
			t.Fatalf("legacy replacement ignored: %+v %v", got, err)
		}
	}
	cfg := &Config{Version: 2, Profile: "headless", Patches: []Patch{{Modules: map[string]bool{"wuu.resource-policy.default": false, "wuu.resource-access": false}}}}
	got, err := cfg.Resolve()
	if err != nil || got.Includes("wuu.resource-access") || got.Includes("wuu.resource-policy.default") {
		t.Fatalf("explicit exclusion repaired: %+v %v", got, err)
	}
}

// Frozen pre-resource-policy headless selection from 00c2811. Do not regenerate it from
// the current catalog: downstream v3 explicit module lists are the contract.
func TestFrozenV3CompositionMigratesOnlyResourceAuthority(t *testing.T) {
	raw, err := os.ReadFile("testdata/v3-headless.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(cfg)
	got, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]bool)
	for _, id := range *cfg.Modules {
		want[id] = true
	}
	for _, id := range []string{"wuu.resource-access", "wuu.resource-policy.default"} {
		want[id] = true
		if got.SourceForModule(id) != "migration:v3-to-v4" {
			t.Fatalf("resource authority lacks versioned provenance: %+v", got)
		}
	}
	if len(*got.Modules) != len(want) {
		t.Fatalf("legacy composition acquired unrelated modules: %+v", got)
	}
	for _, id := range *got.Modules {
		if !want[id] {
			t.Fatalf("unexpected migrated module %s", id)
		}
	}
	again, err := cfg.Resolve()
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("migration not stable: %+v %v", again, err)
	}
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("migration rewrote old configuration")
	}
	profile, err := (&Config{Version: 3, Profile: "headless"}).Resolve()
	if err != nil || !reflect.DeepEqual(*got.Modules, *profile.Modules) {
		t.Fatalf("frozen v3 catalog changed: %+v %v", profile, err)
	}
}

func TestV3ResourceMigrationHonorsExplicitAuthorityChoices(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cfg              Config
		consumer, policy bool
	}{
		{"v3 extraction is already explicit", Config{Version: 3, Modules: &[]string{"wuu.session-execution"}}, true, true},
		{"v3 empty recovery", Config{Version: 3, Modules: &[]string{}}, false, false},
		{"v3 excluded boundary", Config{Version: 3, Modules: &[]string{"kernel"}, Patches: []Patch{{Modules: map[string]bool{"wuu.resource-access": false, "wuu.resource-policy.default": false}}}}, false, false},
		{"v3 scoped replacement", Config{Version: 3, Modules: &[]string{"kernel", "external.policy"}, Services: []Binding{{Consumer: "wuu.resource-access", Service: "resource.authorization", Major: 1, Provider: "external.policy"}}}, true, false},
		{"v3 replacement", Config{Version: 3, Modules: &[]string{"kernel", "external.policy"}, Services: []Binding{{Service: "resource.authorization", Major: 1, Provider: "external.policy"}}}, true, false},
		{"v4 omission stays omitted", Config{Version: 4, Modules: &[]string{"kernel"}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if got.Includes("wuu.resource-access") != tc.consumer || got.Includes("wuu.resource-policy.default") != tc.policy {
				t.Fatalf("explicit authority changed: %+v", got)
			}
			for _, id := range []string{"wuu.model.native", "wuu.model-execution", "wuu.engines.native", "wuu.agent.policy.default"} {
				if got.Includes(id) {
					t.Fatalf("reapplied v1/v2 extraction to v3: %s", id)
				}
			}
		})
	}
}
func TestConsumerScopedBindingsResolveAndRemove(t *testing.T) {
	modules := []string{"wrapper", "base", "reader"}
	cfg := &Config{Version: Version, Modules: &modules, Services: []Binding{
		{Service: "state.documents", Major: 1, Provider: "wrapper"},
		{Service: "state.documents", Major: 1, Provider: "base", Consumer: "wrapper"},
	}}
	got, err := cfg.Resolve()
	if err != nil || len(got.Services) != 2 {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	cfg.Patches = []Patch{{RemoveServices: []ServiceKey{{Service: "state.documents", Major: 1, Consumer: "wrapper"}}}}
	got, err = cfg.Resolve()
	if err != nil || len(got.Services) != 1 || got.Services[0].Consumer != "" {
		t.Fatalf("scoped removal: %+v %v", got, err)
	}
	cfg.Patches = nil
	cfg.Services[1].Consumer = "excluded"
	if _, err := cfg.Resolve(); err == nil {
		t.Fatal("excluded consumer accepted")
	}
	cfg.Services[1].Consumer = " wrapper "
	if _, err := cfg.Resolve(); err == nil {
		t.Fatal("invalid consumer accepted")
	}
}

func TestSharingConsumerIsCurrentProductOnly(t *testing.T) {
	for _, version := range []int{2, 3, 4} {
		got, err := (&Config{Version: version, Profile: "headless"}).Resolve()
		if err != nil {
			t.Fatal(err)
		}
		if got.Includes("wuu.session-sharing-access") != (version >= 4) {
			t.Fatalf("version %d changed sharing consumer selection", version)
		}
	}
}

func TestPersonalProfileSelectsReplaceablePrompt(t *testing.T) {
	for _, version := range []int{2, 3, Version} {
		legacy := &Config{Version: version, Profile: "personal"}
		resolved, err := legacy.Resolve()
		if err != nil || !resolved.Includes("personal") || !resolved.Includes("wuu.resource-access") {
			t.Fatalf("explicit Personal selection required rewriting config v%d: %v", version, err)
		}
	}
	cfg := DefaultConfig("personal")
	resolved, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Includes("personal") || !resolved.Includes("automation") || !resolved.Includes("personal-calendar") || resolved.Includes("wuu.prompt.default") {
		t.Fatal("Personal profile did not select its complete provider composition")
	}
	found := false
	for _, binding := range resolved.Services {
		if binding.Service == "agent.prompt" && binding.Major == 1 && binding.Provider == "personal" {
			found = true
		}
	}
	if !found {
		t.Fatal("Personal prompt is not an explicit service binding")
	}
	cfg.Patches = []Patch{{Modules: map[string]bool{"replacement.prompt": true}, Services: []Binding{{Service: "agent.prompt", Major: 1, Provider: "replacement.prompt"}}}}
	resolved, err = cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range resolved.Services {
		if binding.Service == "agent.prompt" && binding.Provider != "replacement.prompt" {
			t.Fatal("Personal prompt cannot be replaced")
		}
	}
	cfg.Patches = append(cfg.Patches, Patch{Modules: map[string]bool{"replacement.prompt": false}})
	if _, err := cfg.Resolve(); err == nil {
		t.Fatal("missing selected prompt restored native or Personal fallback")
	}
}

// Durable grants are product capability selection, not an implicit mutation of
// every Coding/minimal document provider's storage and namespace contract.
func TestExecutionAuthorizationRequiresExplicitProductSelection(t *testing.T) {
	for _, profile := range []string{"desktop", "headless", "minimal"} {
		resolved, err := DefaultConfig(profile).Resolve()
		if err != nil {
			t.Fatal(err)
		}
		for _, module := range []string{"wuu.execution-access", "wuu.execution-authorization.default"} {
			if resolved.Includes(module) {
				t.Errorf("%s implicitly selected %s", profile, module)
			}
		}
	}
	resolved, err := DefaultConfig("personal").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"wuu.execution-access", "wuu.execution-authorization.default"} {
		if !resolved.Includes(module) {
			t.Errorf("explicit Personal product omitted %s", module)
		}
	}
}
