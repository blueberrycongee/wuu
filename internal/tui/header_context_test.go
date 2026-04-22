package tui

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
)

// newStreamRunner builds just enough of a stream runner for the helpers
// that read ContextWindowOverride / SystemPrompt / Tools. The runner is
// only introspected, never driven, so a zero-value Client is fine.
func newStreamRunner(system string, override int) *agent.StreamRunner {
	return &agent.StreamRunner{
		Model:                 "claude-sonnet-4",
		SystemPrompt:          system,
		ContextWindowOverride: override,
	}
}

func TestRenderContextUsage_NoTokensYet(t *testing.T) {
	m := Model{}
	if got := m.renderContextUsage(); got != "" {
		t.Fatalf("expected empty output before any usage, got %q", got)
	}
}

func TestRenderContextUsage_UsesOverrideWindow(t *testing.T) {
	m := Model{
		contextTokens: 10_000,
		modelName:     "claude-sonnet-4",
		streamRunner:  newStreamRunner("", 20_000),
	}
	out := m.renderContextUsage()
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	// 10000 / 20000 = 50%. Must mention the pct and both absolute numbers.
	// formatCompactNum emits lowercase units ("10k"), not "10K".
	if !strings.Contains(out, "50%") {
		t.Fatalf("expected 50%% in output, got %q", out)
	}
	if !strings.Contains(out, "10k") || !strings.Contains(out, "20k") {
		t.Fatalf("expected 10k/20k in output, got %q", out)
	}
}

// TestRenderContextUsage_FallsBackToRegistry pins behavior against the
// ContextWindowFor resolution path. We don't assert the exact registry
// value (it's sourced from catwalk + substring fallback and changes as
// catwalk data updates), but we do verify the percentage is computed
// against *some* positive window — a non-empty colored label.
func TestRenderContextUsage_FallsBackToRegistry(t *testing.T) {
	m := Model{
		contextTokens: 100_000,
		modelName:     "claude-sonnet-4",
	}
	out := m.renderContextUsage()
	if out == "" {
		t.Fatal("expected fallback registry to produce a non-empty label")
	}
	if !strings.Contains(out, "ctx ") || !strings.Contains(out, "%") {
		t.Fatalf("expected 'ctx N%%' format, got %q", out)
	}
}

func TestRenderContextUsage_SmallModelAdaptsNaturally(t *testing.T) {
	// Override to a known 32K window so the assertion is independent of
	// whatever ContextWindowFor("mistral-small") resolves to.
	m := Model{
		contextTokens: 24_000,
		modelName:     "mistral-small",
		streamRunner:  newStreamRunner("", 32_000),
	}
	out := m.renderContextUsage()
	if !strings.Contains(out, "75%") {
		t.Fatalf("expected 75%% for 24K/32K override, got %q", out)
	}
}

func TestRenderContextUsage_ClampsAbove999(t *testing.T) {
	// Pathological: usage >> window. Without clamping the printed %
	// could push other header segments off-screen.
	m := Model{
		contextTokens: 1_000_000,
		modelName:     "irrelevant",
		streamRunner:  newStreamRunner("", 1_000), // absurdly small override
	}
	out := m.renderContextUsage()
	if !strings.Contains(out, "999%") {
		t.Fatalf("expected 999%% clamp, got %q", out)
	}
}

func TestRenderContextUsage_ColorThresholds(t *testing.T) {
	// Force a deterministic 200K window via override so the percentage
	// math doesn't depend on ContextWindowFor's registry resolution.
	cases := []struct {
		name        string
		used        int
		requireBold bool
	}{
		{"low_50pct", 100_000, false},   // 50% / Text
		{"warn_80pct", 160_000, false},  // 80% / Warning
		{"error_92pct", 184_000, true},  // 92% / Error bold
	}
	for _, tc := range cases {
		m := Model{
			contextTokens: tc.used,
			modelName:     "claude-sonnet-4",
			streamRunner:  newStreamRunner("", 200_000),
		}
		out := m.renderContextUsage()
		if !strings.Contains(out, "\x1b[") {
			t.Fatalf("%s: expected SGR in output, got %q", tc.name, out)
		}
		if tc.requireBold && !strings.Contains(out, "\x1b[1") && !strings.Contains(out, ";1") {
			// lipgloss emits Bold as SGR parameter 1, possibly combined
			// (e.g. `\x1b[1;38;2;...m`). Accept either standalone or as
			// part of a combined sequence.
			t.Fatalf("%s: expected bold SGR in error-band output, got %q", tc.name, out)
		}
	}
}
