package tools

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/modelprofile"
)

// TestAdvancedToolsHiddenFromModelSurfaces verifies that the
// structured git tool — the one that Codex-style harnesses collapse
// into bash — stays registered in the toolkit so internal callers can
// still reach it, but never appears on a model-visible tool surface or
// a compiled profile hidden-tool contract.
//
// bash-first redesign: the model only ever sees bash. The former
// run_shell / run_test / managed-process tools were removed entirely;
// git remains registry-only. The compiler omits git from profile
// surfaces, and toolExposure keeps the registry-only implementation
// out of Definitions.
func TestAdvancedToolsHiddenFromModelSurfaces(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The advanced command tool that the bash-first surface demotes to
	// internal. The list is the authoritative source: toolExposure, the
	// model-profile compiler, and this test must agree.
	advancedTools := []string{
		"git",
	}

	// Baseline: without a profile, the legacy direct-tool surface
	// must not include any of the advanced tools. toolExposure
	// returns Hidden for them, so they never leak into Definitions.
	legacy := kit.Definitions()
	for _, name := range advancedTools {
		if containsProfileDef(legacy, name) {
			t.Errorf("legacy surface must not advertise %s, got %v", name, sortedProfileDefNames(legacy))
		}
	}

	// Per-profile: the compiler does not include advanced command
	// tools in either visible tools or hidden profile output.
	profiles := []struct {
		provider string
		model    string
	}{
		{provider: "openai", model: "gpt-5-codex"},
		{provider: "openai", model: "gpt-5.5"},
		{provider: "anthropic", model: "claude-sonnet-4-5"},
		{provider: "google", model: "gemini-2.5-pro"},
		{provider: "ollama", model: "llama-coder"},
	}
	for _, tt := range profiles {
		kit.SetActiveProfile(modelprofile.Resolve(tt.provider, tt.model), true)
		surface := kit.ActiveSurface()
		defs := kit.Definitions()
		for _, name := range advancedTools {
			if _, ok := surface.HiddenTools[name]; ok {
				t.Errorf("%s/%s: %s must not remain in surface.HiddenTools",
					tt.provider, tt.model, name)
			}
			if containsProfileDef(defs, name) {
				t.Errorf("%s/%s: %s must not appear in Definitions (advanced), got %v",
					tt.provider, tt.model, name, sortedProfileDefNames(defs))
			}
		}
	}
}
