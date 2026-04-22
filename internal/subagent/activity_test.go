package subagent

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestDeriveWorkerActivity_EmptyForUninteresting(t *testing.T) {
	cases := []providers.StreamEventType{
		providers.EventMessage,
		providers.EventLifecycle,
		providers.EventReconnect,
		providers.EventCompact,
		providers.EventDone,
		providers.EventError,
		providers.EventToolUseDelta, // argument bytes; per-token noise
	}
	for _, tp := range cases {
		ev := providers.StreamEvent{Type: tp}
		if got := deriveWorkerActivity(ev); got != "" {
			t.Fatalf("type %q: expected empty phrase, got %q", tp, got)
		}
	}
}

func TestDeriveWorkerActivity_ThinkingAndResponding(t *testing.T) {
	if got := deriveWorkerActivity(providers.StreamEvent{
		Type: providers.EventThinkingDelta,
	}); got != "thinking" {
		t.Fatalf("thinking: got %q", got)
	}
	if got := deriveWorkerActivity(providers.StreamEvent{
		Type: providers.EventContentDelta,
	}); got != "responding" {
		t.Fatalf("responding: got %q", got)
	}
}

func TestDeriveWorkerActivity_ToolTransitions(t *testing.T) {
	start := providers.StreamEvent{
		Type:     providers.EventToolUseStart,
		ToolCall: &providers.ToolCall{Name: "read_file"},
	}
	if got := deriveWorkerActivity(start); got != "→ read_file" {
		t.Fatalf("tool start: got %q", got)
	}

	end := providers.StreamEvent{
		Type:     providers.EventToolUseEnd,
		ToolCall: &providers.ToolCall{Name: "grep"},
	}
	if got := deriveWorkerActivity(end); got != "✓ grep" {
		t.Fatalf("tool end: got %q", got)
	}
}

func TestSnapshotPropagatesActivity(t *testing.T) {
	// Snapshot() is the only observer surface; if the new fields weren't
	// copied, the TUI would never see activity updates even though the
	// runner wrote them.
	sa := &SubAgent{
		ID:       "w1",
		Activity: "→ read_file",
	}
	snap := sa.Snapshot()
	if snap.Activity != "→ read_file" {
		t.Fatalf("snapshot lost activity: %q", snap.Activity)
	}
}

func TestDeriveWorkerActivity_ToolWithoutName(t *testing.T) {
	// Some providers fire ToolUseStart before the name lands. We still
	// want a phrase so the UI can show *something* is happening.
	ev := providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: nil}
	if got := deriveWorkerActivity(ev); got != "→ tool" {
		t.Fatalf("nameless tool start: got %q", got)
	}
	ev.ToolCall = &providers.ToolCall{} // zero-value name
	if got := deriveWorkerActivity(ev); got != "→ tool" {
		t.Fatalf("empty-name tool start: got %q", got)
	}
}
