package agent

import "testing"

// TestProactiveCompactThresholdPrefersBudgetValue locks the single-owner rule:
// when modelbudget resolved a threshold, the loop consumes it verbatim instead
// of re-deriving its own window math, so trigger, trace, and UI cannot drift.
func TestProactiveCompactThresholdPrefersBudgetValue(t *testing.T) {
	cfg := LoopConfig{
		MaxContextTokens:       1_000_000,
		OutputReserveTokens:    128_000,
		CompactThresholdTokens: 967_000,
	}
	if got := proactiveCompactThreshold(cfg); got != 967_000 {
		t.Fatalf("threshold = %d, want budget-provided 967000", got)
	}

	// Legacy fallback still works when no budget threshold is plumbed.
	cfg.CompactThresholdTokens = 0
	if got := proactiveCompactThreshold(cfg); got <= 0 {
		t.Fatalf("legacy fallback threshold = %d, want > 0", got)
	}

	// Pct override keeps precedence over the absolute budget value.
	cfg.CompactThresholdTokens = 967_000
	cfg.CompactThresholdPct = 0.5
	if got := proactiveCompactThreshold(cfg); got != 500_000 {
		t.Fatalf("pct threshold = %d, want 500000", got)
	}
}
