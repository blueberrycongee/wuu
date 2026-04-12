package agent

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBuildCacheHint_DefaultsToStableHistoryBeforeCurrentTurn(t *testing.T) {
	hint := buildCacheHint([]providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first ask"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "current ask"},
	})
	if hint == nil {
		t.Fatal("expected cache hint")
	}
	if !hint.StableSystem {
		t.Fatal("expected stable system to be enabled")
	}
	if hint.StablePrefixMessages != 2 {
		t.Fatalf("expected 2 stable non-system messages, got %d", hint.StablePrefixMessages)
	}
	if hint.HasCompactSummary {
		t.Fatal("did not expect compact summary flag")
	}
	if hint.PromptCacheKey == "" {
		t.Fatal("expected prompt cache key")
	}
}

func TestBuildCacheHint_OnlyCurrentTurnStillGetsPromptCacheKey(t *testing.T) {
	hint := buildCacheHint([]providers.ChatMessage{
		{Role: "user", Content: "current ask"},
	})
	if hint == nil {
		t.Fatal("expected cache hint")
	}
	if hint.StableSystem {
		t.Fatal("did not expect stable system without system message")
	}
	if hint.StablePrefixMessages != 0 {
		t.Fatalf("expected no stable prefix messages, got %d", hint.StablePrefixMessages)
	}
	if hint.HasCompactSummary {
		t.Fatal("did not expect compact summary flag")
	}
	if hint.PromptCacheKey == "" {
		t.Fatal("expected prompt cache key for current conversation root")
	}
}

func TestBuildCacheHint_CompactSummaryBecomesStableAnchor(t *testing.T) {
	messages := []providers.ChatMessage{
		{Role: "system", Content: "[Conversation summary]\nOlder turns were compacted."},
		{Role: "user", Content: "keep working"},
	}

	hint := buildCacheHint(messages)
	if hint == nil {
		t.Fatal("expected cache hint")
	}
	if !hint.StableSystem {
		t.Fatal("expected stable system")
	}
	if !hint.HasCompactSummary {
		t.Fatal("expected compact summary flag")
	}
	if hint.StablePrefixMessages != 0 {
		t.Fatalf("expected current turn to stay volatile, got %d stable prefix messages", hint.StablePrefixMessages)
	}

	otherCurrentTurn := []providers.ChatMessage{
		{Role: "system", Content: "[Conversation summary]\nOlder turns were compacted."},
		{Role: "user", Content: "do something else now"},
	}
	otherHint := buildCacheHint(otherCurrentTurn)
	if otherHint == nil {
		t.Fatal("expected second cache hint")
	}
	if hint.PromptCacheKey != otherHint.PromptCacheKey {
		t.Fatalf("expected compact summary to dominate cache key across turns, got %q vs %q", hint.PromptCacheKey, otherHint.PromptCacheKey)
	}
}
