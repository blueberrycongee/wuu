package plugin

import "testing"

func TestClassifyReloadManifestChange(t *testing.T) {
	t.Parallel()

	manifest := Manifest{ID: "full", Desktop: &DesktopSpec{Entry: "dist/renderer.js"}, Runtime: &RuntimeSpec{Command: "dist/runtime.js"}}
	hint := ClassifyReload(manifest, []string{"plugin.json"})

	if hint.Effect != ReloadEffectTrust {
		t.Fatalf("effect = %q, want %q", hint.Effect, ReloadEffectTrust)
	}
	if hint.Message == "" {
		t.Fatal("manifest change must produce a hint")
	}
}

func TestClassifyReloadAgentAndCapabilityPaths(t *testing.T) {
	t.Parallel()

	manifest := Manifest{
		ID:          "full",
		Desktop:     &DesktopSpec{Entry: "dist/renderer.js"},
		Runtime:     &RuntimeSpec{Command: "dist/runtime.js"},
		RuntimePath: "dist/runtime.js",
		Skills:      []string{"skills"},
		HookPaths:   []string{"hooks/setup.sh"},
	}

	agent := ClassifyReload(manifest, []string{"skills/commit/SKILL.md"})
	if agent.Effect != ReloadEffectMind {
		t.Fatalf("skills change effect = %q, want %q", agent.Effect, ReloadEffectMind)
	}

	frontend := ClassifyReload(manifest, []string{"dist/renderer.js"})
	if frontend.Effect != ReloadEffectCapability {
		t.Fatalf("desktop entry change effect = %q, want %q", frontend.Effect, ReloadEffectCapability)
	}

	both := ClassifyReload(manifest, []string{"dist/renderer.js", "hooks/setup.sh"})
	if both.Effect != ReloadEffectMind {
		t.Fatalf("mixed change effect = %q, want %q", both.Effect, ReloadEffectMind)
	}
}

func TestClassifyReloadFallsBackToDeclaredSurfaces(t *testing.T) {
	t.Parallel()

	pureDesktop := Manifest{ID: "desktop", Desktop: &DesktopSpec{Entry: "dist/index.js"}}
	if got := ClassifyReload(pureDesktop, []string{"src/index.ts"}); got.Effect != ReloadEffectCapability {
		t.Fatalf("unmatched change in pure desktop plugin effect = %q, want %q", got.Effect, ReloadEffectCapability)
	}

	pureAgent := Manifest{ID: "agent", Runtime: &RuntimeSpec{Command: "bin/plugin"}}
	if got := ClassifyReload(pureAgent, []string{"src/runtime.ts"}); got.Effect != ReloadEffectMind {
		t.Fatalf("unmatched change in pure agent plugin effect = %q, want %q", got.Effect, ReloadEffectMind)
	}

	mixed := Manifest{ID: "full", Desktop: &DesktopSpec{Entry: "dist/renderer.js"}, Runtime: &RuntimeSpec{Command: "dist/runtime.js"}}
	if got := ClassifyReload(mixed, []string{"src/renderer.ts"}); got.Effect != ReloadEffectMind {
		t.Fatalf("unattributed change in mixed plugin effect = %q, want %q", got.Effect, ReloadEffectMind)
	}
}

func TestClassifyReloadUsesConventionalSkillsDir(t *testing.T) {
	t.Parallel()

	manifest := Manifest{ID: "skills-only"}
	hint := ClassifyReload(manifest, []string{"skills/docs/SKILL.md"})
	if hint.Effect != ReloadEffectMind {
		t.Fatalf("conventional skills change effect = %q, want %q", hint.Effect, ReloadEffectMind)
	}
}
