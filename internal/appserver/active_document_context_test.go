package appserver

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestActiveDocumentRequestContext(t *testing.T) {
	if got := activeDocumentRequestContext(nil); got != nil {
		t.Fatalf("nil document context = %#v, want nil", got)
	}
	if got := activeDocumentRequestContext(&ActiveDocument{Path: "  "}); got != nil {
		t.Fatalf("blank document context = %#v, want nil", got)
	}

	segments := activeDocumentRequestContext(&ActiveDocument{Path: " docs/plan.md "})
	if len(segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(segments))
	}
	segment := segments[0]
	if segment.Lifecycle != agent.ContextSegmentRequestOnly || segment.Durable || segment.VisibleInUI {
		t.Fatalf("document context lifecycle = %+v, want hidden request-only", segment)
	}
	if len(segment.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(segment.Blocks))
	}
	block := segment.Blocks[0]
	if block.Kind != wuucontext.BlockActiveFiles || block.Source != "desktop.document_focus" {
		t.Fatalf("block identity = %+v", block)
	}
	if !strings.Contains(block.Content, `"docs/plan.md"`) {
		t.Fatalf("block content %q does not identify the active document", block.Content)
	}
}

func TestActiveDocumentContextForTurnUsesLatestSteerSnapshot(t *testing.T) {
	base := append(
		agent.RequestOnlyContextBlocks([]wuucontext.Block{{
			Kind: wuucontext.BlockTaskState, Source: "test.task_state", Content: "keep me",
		}}),
		activeDocumentRequestContext(&ActiveDocument{Path: "docs/original.md"})...,
	)
	overridden := activeDocumentContextForTurn(base, true, &ActiveDocument{Path: "docs/latest.md"})
	if len(overridden) != 2 || !strings.Contains(overridden[1].Blocks[0].Content, `"docs/latest.md"`) {
		t.Fatalf("overridden context = %#v, want latest steer document", overridden)
	}
	if overridden[0].Blocks[0].Source != "test.task_state" {
		t.Fatalf("overridden context dropped non-document segment: %#v", overridden)
	}
	if strings.Contains(overridden[1].Blocks[0].Content, "original.md") {
		t.Fatalf("overridden context retained original document: %#v", overridden)
	}
	if cleared := activeDocumentContextForTurn(base, true, nil); len(cleared) != 1 || cleared[0].Blocks[0].Source != "test.task_state" {
		t.Fatalf("cleared context = %#v, want only non-document context", cleared)
	}
	retained := activeDocumentContextForTurn(base, false, nil)
	if len(retained) != 2 || !strings.Contains(retained[1].Blocks[0].Content, `"docs/original.md"`) {
		t.Fatalf("retained context = %#v, want original turn context", retained)
	}
}

func TestRemovingSteerDocumentOverrideRestoresPreviousSnapshot(t *testing.T) {
	th := &threadState{steerDocumentOverrides: []activeDocumentOverride{
		{steerID: "steer-1", document: &ActiveDocument{Path: "docs/first.md"}},
		{steerID: "steer-2", document: &ActiveDocument{Path: "docs/latest.md"}},
	}}
	th.applyLatestSteerDocumentOverrideLocked()

	th.removeSteerDocumentOverrideLocked("steer-2")

	if !th.activeSteerContextSet || th.activeSteerDocument == nil || th.activeSteerDocument.Path != "docs/first.md" {
		t.Fatalf("active steer document = %#v, want previous snapshot", th.activeSteerDocument)
	}
	th.removeSteerDocumentOverrideLocked("steer-1")
	if th.activeSteerContextSet || th.activeSteerDocument != nil {
		t.Fatalf("active steer document survived final unsteer: %#v", th.activeSteerDocument)
	}
}

func TestApplyingSteerDocumentOverridesPreservesQueuedSnapshot(t *testing.T) {
	th := &threadState{steerDocumentOverrides: []activeDocumentOverride{{
		steerID: "steer-1", document: &ActiveDocument{Path: "docs/guide.md"},
	}}}
	turns := queuedTurnsFromSteers([]providers.ChatMessage{{
		Role: "user", Content: "guide", ClientID: "steer-1", Steered: true,
	}})

	th.applySteerDocumentOverridesLocked(turns)

	if len(turns) != 1 || turns[0].snapshot.ActiveDocument == nil || turns[0].snapshot.ActiveDocument.Path != "docs/guide.md" {
		t.Fatalf("queued steer snapshot = %+v", turns)
	}
}
