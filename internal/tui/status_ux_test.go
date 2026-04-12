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

func TestShouldRenderInlineStatusKeepsRespondingWhileQueuedMessageAdded(t *testing.T) {
	m := Model{pendingRequest: true, streaming: true, statusLine: "streaming"}
	m.messageQueue = []queuedMessage{{Text: "queued follow-up"}}

	if !m.shouldRenderInlineStatus() {
		t.Fatal("expected inline responding status to stay visible while follow-up messages are queued")
	}
	if got := deriveWorkStatus(m.statusLine).Label; got != "Responding" {
		t.Fatalf("expected responding work status, got %q", got)
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
	if statusAnimationInterval != 100*time.Millisecond {
		t.Fatalf("expected status animation interval to be 100ms, got %s", statusAnimationInterval)
	}
}
