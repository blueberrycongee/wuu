package tui

import (
	"testing"
	"time"
)

func TestShouldRenderInlineStatusSuppressesTranscriptDuplicates(t *testing.T) {
	m := Model{streaming: true, statusLine: "thinking"}
	m.entries = []transcriptEntry{{Role: "ASSISTANT", ThinkingContent: "working", ThinkingDone: false}}

	if m.shouldRenderInlineStatus() {
		t.Fatal("expected inline thinking status to be hidden when transcript already shows a live thinking block")
	}
}

func TestShouldRenderInlineStatusKeepsGlobalRespondingStatus(t *testing.T) {
	m := Model{streaming: true, statusLine: "streaming"}
	m.entries = []transcriptEntry{{Role: "ASSISTANT", ThinkingContent: "working", ThinkingDone: false}}

	if !m.shouldRenderInlineStatus() {
		t.Fatal("expected inline responding status to remain visible as the main global status")
	}
}

func TestShouldRenderInlineStatusSuppressesToolDuplicate(t *testing.T) {
	m := Model{streaming: true, statusLine: "tool: grep"}
	m.entries = []transcriptEntry{{
		Role:      "ASSISTANT",
		ToolCalls: []ToolCallEntry{{Name: "grep", Status: ToolCallRunning}},
	}}

	if m.shouldRenderInlineStatus() {
		t.Fatal("expected inline tool status to be hidden when a live tool card is already visible")
	}
}

func TestStatusAnimationIntervalSlowerForCalmerShimmer(t *testing.T) {
	if statusAnimationInterval != 300*time.Millisecond {
		t.Fatalf("expected status animation interval to be 300ms, got %s", statusAnimationInterval)
	}
}
