package agent

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestUsageTracker_EmptyIsZero(t *testing.T) {
	tr := NewUsageTracker()
	if got := tr.EstimateCurrent(); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestUsageTracker_NilSafe(t *testing.T) {
	var tr *UsageTracker // intentionally nil
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 10})
	tr.RecordPendingMessages([]providers.ChatMessage{{Role: "user", Content: "hi"}})
	tr.Reset()
	if got := tr.EstimateCurrent(); got != 0 {
		t.Fatalf("nil tracker should return 0, got %d", got)
	}
}

func TestUsageTracker_GroundTruthFromResponse(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 1000, OutputTokens: 250})

	if got := tr.EstimateCurrent(); got != 1250 {
		t.Fatalf("expected 1250, got %d", got)
	}
	if got := tr.LastResponseTotal(); got != 1250 {
		t.Fatalf("expected last 1250, got %d", got)
	}
	if got := tr.PendingDelta(); got != 0 {
		t.Fatalf("expected pending 0, got %d", got)
	}
}

func TestUsageTracker_PendingDeltaAccumulates(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 1000, OutputTokens: 0})

	// 400 chars ≈ 100 tokens + 4 overhead = 104
	longMsg := providers.ChatMessage{
		Role:    "user",
		Content: strings.Repeat("x", 400),
	}
	tr.RecordPendingMessages([]providers.ChatMessage{longMsg})

	got := tr.EstimateCurrent()
	if got <= 1000 || got > 1200 {
		t.Fatalf("expected 1000 < estimate <= 1200, got %d", got)
	}
}

func TestUsageTracker_ResponseResetsPendingDelta(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 500})
	tr.RecordPendingMessages([]providers.ChatMessage{
		{Role: "user", Content: strings.Repeat("y", 200)},
	})
	if tr.PendingDelta() == 0 {
		t.Fatal("delta should be non-zero before second response")
	}

	// Second response collapses the delta into ground truth.
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 1500, OutputTokens: 100})
	if got := tr.PendingDelta(); got != 0 {
		t.Fatalf("delta should be zero after RecordResponse, got %d", got)
	}
	if got := tr.EstimateCurrent(); got != 1600 {
		t.Fatalf("expected 1600, got %d", got)
	}
}

func TestUsageTracker_ToolCallEnvelopeCounted(t *testing.T) {
	tr := NewUsageTracker()
	withTool := providers.ChatMessage{
		Role:    "assistant",
		Content: "ok",
		ToolCalls: []providers.ToolCall{
			{Name: "run_shell", Arguments: `{"command":"ls"}`},
		},
	}
	tr.RecordPendingMessages([]providers.ChatMessage{withTool})
	if got := tr.PendingDelta(); got <= 0 {
		t.Fatalf("tool-call message should produce non-zero delta, got %d", got)
	}
}

func TestUsageTracker_ImagePendingDeltaUsesVisualBudget(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordPendingMessages([]providers.ChatMessage{{
		Role: "user",
		Images: []providers.InputImage{{
			MediaType: "image/png",
			Data:      strings.Repeat("a", 1_000_000),
			Width:     2048,
			Height:    2048,
		}},
	}})

	if got := tr.PendingDelta(); got > 5000 {
		t.Fatalf("image delta should use visual budget, got %d", got)
	}
}

func TestUsageTracker_CacheReadCountsTowardContext(t *testing.T) {
	// Critical regression: cached tokens still occupy context. A
	// session with 100k of cache_read + 1k of fresh input must
	// report ~101k of context usage, not 1k.
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{
		InputTokens:     1000,
		CacheReadTokens: 100_000,
		OutputTokens:    500,
	})

	got := tr.EstimateCurrent()
	want := 1000 + 100_000 + 500 // 101_500
	if got != want {
		t.Fatalf("expected cache_read in fill: got %d, want %d", got, want)
	}
}

func TestUsageTracker_CacheCreationCountsTowardContext(t *testing.T) {
	// Anthropic's input_tokens EXCLUDES cache_creation and cache_read: the
	// full prompt is input + cache_creation + cache_read. On a cold-cache
	// first request cache_creation is roughly the entire history; dropping it
	// made the session look nearly empty and suppressed proactive
	// auto-compact exactly when it was most needed.
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{
		InputTokens:         1000,
		CacheCreationTokens: 90_000, // cold cache: history written to cache
		OutputTokens:        100,
	})

	got := tr.EstimateCurrent()
	want := 1000 + 90_000 + 100
	if got != want {
		t.Fatalf("expected cache_creation in fill: got %d, want %d", got, want)
	}
}

func TestUsageTracker_RaiseLocalEstimateReplacesPartialTotal(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordPendingMessages([]providers.ChatMessage{{Role: "user", Content: "short"}})
	before := tr.EstimateCurrent()
	if !tr.RaiseLocalEstimate(before + 5000) {
		t.Fatal("expected partial local total to rise")
	}
	if got := tr.EstimateCurrent(); got != before+5000 {
		t.Fatalf("estimate = %d, want %d", got, before+5000)
	}
	if got := tr.Breakdown().Adjustment; got != UsageAdjustmentLocalHistoryReconcile {
		t.Fatalf("adjustment = %q", got)
	}
	if tr.RaiseLocalEstimate(before + 1000) {
		t.Fatal("reconcile must not lower the running total")
	}
	if got := tr.EstimateCurrent(); got != before+5000 {
		t.Fatalf("estimate dropped to %d", got)
	}
}

func TestUsageTracker_RaiseLocalEstimateKeepsProviderBaseline(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 1000, OutputTokens: 50})
	if tr.RaiseLocalEstimate(50_000) {
		t.Fatal("provider baseline must not be replaced by a local estimate")
	}
	if got := tr.EstimateCurrent(); got != 1050 {
		t.Fatalf("estimate = %d, want 1050", got)
	}
}

func TestUsageTracker_Reset(t *testing.T) {
	tr := NewUsageTracker()
	tr.RecordResponse(&providers.TokenUsage{InputTokens: 1000})
	tr.RecordPendingMessages([]providers.ChatMessage{{Role: "user", Content: "hi"}})
	tr.Reset()
	if got := tr.EstimateCurrent(); got != 0 {
		t.Fatalf("expected 0 after Reset, got %d", got)
	}
	if got := tr.LastResponseTotal(); got != 0 {
		t.Fatalf("expected last 0 after Reset, got %d", got)
	}
}
