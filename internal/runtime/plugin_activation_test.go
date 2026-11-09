package runtime

import (
	"fmt"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

func TestActivatedPluginsFailClosed(t *testing.T) {
	community := pluginpkg.Plugin{
		Manifest:             pluginpkg.Manifest{ID: "community"},
		SubjectID:            "project:community",
		Fingerprint:          "sha256:current",
		EffectivePermissions: []string{extensions.PermProcessSpawn},
	}
	official := community
	official.SubjectID = "bundled:official"
	official.ID = "official"
	official.Official = true

	if got := activatedPlugins(config.Config{}, []pluginpkg.Plugin{community}); len(got) != 0 {
		t.Fatalf("pending package activated: %+v", got)
	}
	if got := activatedPlugins(config.Config{}, []pluginpkg.Plugin{official}); len(got) != 1 {
		t.Fatalf("official package did not activate: %+v", got)
	}

	settings := &extensions.Settings{}
	if err := settings.RecordGrant(extensions.Grant{
		SubjectID: community.SubjectID, Fingerprint: community.Fingerprint,
		Scope: extensions.GrantScopeProject, Permissions: community.EffectivePermissions,
		ApprovedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{community}); len(got) != 1 {
		t.Fatalf("exactly granted package did not activate: %+v", got)
	}

	changed := community
	changed.Fingerprint = "sha256:changed"
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{changed}); len(got) != 0 {
		t.Fatalf("changed package activated using stale grant: %+v", got)
	}

	insufficient := &extensions.Settings{}
	if err := insufficient.RecordGrant(extensions.Grant{
		SubjectID: community.SubjectID, Fingerprint: community.Fingerprint,
		Scope: extensions.GrantScopeProject, ApprovedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := activatedPlugins(config.Config{Extensions: insufficient}, []pluginpkg.Plugin{community}); len(got) != 0 {
		t.Fatalf("package activated without all effective permissions: %+v", got)
	}

	if err := settings.RecordRejection(community.SubjectID, community.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{community}); len(got) != 0 {
		t.Fatalf("rejected package activated: %+v", got)
	}

	settings.Revoke(community.SubjectID)
	settings.SetDisabled(community.SubjectID, true)
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{community}); len(got) != 0 {
		t.Fatalf("disabled package activated: %+v", got)
	}
	settings.SetDisabled(official.SubjectID, true)
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{official}); len(got) != 0 {
		t.Fatalf("disabled official package activated: %+v", got)
	}
}

func TestActivatedPluginsHonorsPackageDefaultAndExplicitEnable(t *testing.T) {
	disabled := false
	official := pluginpkg.Plugin{
		Manifest:  pluginpkg.Manifest{ID: "experimental", DefaultEnabled: &disabled},
		SubjectID: "plugin:bundled:experimental",
		Official:  true,
	}

	if got := activatedPlugins(config.Config{}, []pluginpkg.Plugin{official}); len(got) != 0 {
		t.Fatalf("default-disabled official package activated: %+v", got)
	}
	settings := &extensions.Settings{}
	settings.SetEnabled(official.SubjectID, true)
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{official}); len(got) != 1 {
		t.Fatalf("explicitly enabled official package did not activate: %+v", got)
	}
	settings.SetEnabled(official.SubjectID, false)
	if got := activatedPlugins(config.Config{Extensions: settings}, []pluginpkg.Plugin{official}); len(got) != 0 {
		t.Fatalf("explicitly disabled official package activated: %+v", got)
	}
}

func TestActivatedPluginsEnforcesMinimumWuuVersion(t *testing.T) {
	original := currentWuuVersion
	t.Cleanup(func() { currentWuuVersion = original })
	currentWuuVersion = func() string { return "0.15.0" }

	community := pluginpkg.Plugin{
		Manifest:             pluginpkg.Manifest{ID: "community"},
		SubjectID:            "project:community",
		Fingerprint:          "sha256:current",
		EffectivePermissions: []string{extensions.PermProcessSpawn},
	}
	settings := &extensions.Settings{}
	if err := settings.RecordGrant(extensions.Grant{
		SubjectID: community.SubjectID, Fingerprint: community.Fingerprint,
		Scope: extensions.GrantScopeProject, Permissions: community.EffectivePermissions,
		ApprovedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Extensions: settings}

	compatible := community
	compatible.MinimumWuuVersion = "0.14.0"
	if got := activatedPlugins(cfg, []pluginpkg.Plugin{compatible}); len(got) != 1 {
		t.Fatalf("package below host minor version did not activate: %+v", got)
	}

	incompatible := community
	incompatible.MinimumWuuVersion = "0.16.0"
	if got := activatedPlugins(cfg, []pluginpkg.Plugin{incompatible}); len(got) != 0 {
		t.Fatalf("package above host version activated: %+v", got)
	}

	// The gate applies to every trust tier: an official package declaring a
	// future minimum must not activate either.
	official := incompatible
	official.SubjectID = "bundled:official"
	official.ID = "official"
	official.Official = true
	if got := activatedPlugins(config.Config{}, []pluginpkg.Plugin{official}); len(got) != 0 {
		t.Fatalf("incompatible official package activated: %+v", got)
	}

	// Dev builds carry a pre-release marker and satisfy constraints at base.
	currentWuuVersion = func() string { return "v0.15.0-dev" }
	devCompatible := community
	devCompatible.MinimumWuuVersion = "0.15.0"
	if got := activatedPlugins(cfg, []pluginpkg.Plugin{devCompatible}); len(got) != 1 {
		t.Fatalf("dev host did not satisfy base-version constraint: %+v", got)
	}
}

// A product may suggest an optional default without granting trust or turning a
// user's explicit disable into a startup failure.
func TestOptionalCompositionPluginDefaults(t *testing.T) {
	defaultOff := false
	plugin := pluginpkg.Plugin{
		Manifest:  pluginpkg.Manifest{ID: "fixture.optional", DefaultEnabled: &defaultOff},
		SubjectID: "plugin:bundled:optional", Official: true,
	}
	selection := &composition.Config{Version: composition.Version, Profile: "fixture", Profiles: map[string]composition.Profile{
		"fixture": {Bundles: []string{"fixture"}},
	}, Bundles: map[string]composition.Bundle{
		"fixture": {Patches: []composition.Patch{}, OptionalModules: []string{plugin.ID}},
	}}
	cfg := config.Config{Composition: selection}
	plan, err := ResolvePluginActivationPlan(cfg, []pluginpkg.Plugin{plugin})
	if err != nil || len(plan.Plugins) != 1 {
		t.Fatalf("optional default did not activate: %+v, %v", plan.Plugins, err)
	}
	settings := &extensions.Settings{}
	settings.SetDisabled(plugin.SubjectID, true)
	cfg.Extensions = settings
	plan, err = ResolvePluginActivationPlan(cfg, []pluginpkg.Plugin{plugin})
	if err != nil || len(plan.Plugins) != 0 {
		t.Fatalf("explicit disable must leave optional plugin inactive without blocking the product: %+v, %v", plan.Plugins, err)
	}
	cfg.Extensions = nil
	plan, err = ResolvePluginActivationPlan(cfg, nil)
	if err != nil || len(plan.Plugins) != 0 {
		t.Fatalf("missing optional plugin blocked the product: %+v, %v", plan.Plugins, err)
	}
	selection.Patches = []composition.Patch{{Modules: map[string]bool{plugin.ID: false}}}
	plan, err = ResolvePluginActivationPlan(cfg, []pluginpkg.Plugin{plugin})
	if err != nil || len(plan.Plugins) != 0 {
		t.Fatalf("composition exclusion ignored: %+v, %v", plan.Plugins, err)
	}
	selection.Patches = nil
	plugin.Official = false
	plan, err = ResolvePluginActivationPlan(cfg, []pluginpkg.Plugin{plugin})
	if err != nil || len(plan.Plugins) != 0 {
		t.Fatalf("optional default granted trust: %+v, %v", plan.Plugins, err)
	}
}

func TestPersonalProductEnablesGenerativeUIDefault(t *testing.T) {
	disabled := false
	plugin := pluginpkg.Plugin{
		Manifest:  pluginpkg.Manifest{ID: "generative-ui", DefaultEnabled: &disabled},
		SubjectID: "plugin:bundled:generative-ui", Official: true,
	}
	personal := config.Config{Composition: composition.DefaultConfig("personal")}
	if got := activatedPlugins(personal, []pluginpkg.Plugin{plugin}); len(got) != 1 {
		t.Fatalf("personal default did not enable GenUI: %+v", got)
	}
	desktop := config.Config{Composition: composition.DefaultConfig("desktop")}
	if got := activatedPlugins(desktop, []pluginpkg.Plugin{plugin}); len(got) != 0 {
		t.Fatalf("desktop default unexpectedly enabled GenUI: %+v", got)
	}
}

func TestExistingPersonalHostCompositionEnablesGenerativeUI(t *testing.T) {
	disabled := false
	plugin := pluginpkg.Plugin{
		Manifest:  pluginpkg.Manifest{ID: "generative-ui", DefaultEnabled: &disabled},
		SubjectID: "plugin:bundled:generative-ui", Official: true,
	}
	for _, version := range []int{2, 3, 4, composition.Version} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			cfg := config.Config{Composition: &composition.Config{Version: version, Profile: "desktop"}}
			owner := &Session{configOverrides: ConfigOverrides{CompositionProfile: "personal"}}
			effective, err := owner.applyRuntimeConfig(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := activatedPlugins(effective, []pluginpkg.Plugin{plugin}); len(got) != 1 {
				t.Fatalf("personal host did not enable GenUI with existing v%d composition: %+v", version, got)
			}
			settings := &extensions.Settings{}
			settings.SetDisabled(plugin.SubjectID, true)
			effective.Extensions = settings
			if got := activatedPlugins(effective, []pluginpkg.Plugin{plugin}); len(got) != 0 {
				t.Fatalf("personal host ignored explicit disable: %+v", got)
			}
		})
	}
}
