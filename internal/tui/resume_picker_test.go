package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestPeekFirstUserMessage_HandlesLargeFirstRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	large := strings.Repeat("u", 1100*1024)

	if err := appendChatMessage(path, providers.ChatMessage{Role: "user", Content: large}); err != nil {
		t.Fatalf("appendChatMessage: %v", err)
	}
	if err := appendChatMessage(path, providers.ChatMessage{Role: "assistant", Content: "ok"}); err != nil {
		t.Fatalf("appendChatMessage: %v", err)
	}

	got := peekFirstUserMessage(path)
	if got != large {
		t.Fatalf("unexpected title length: got %d want %d", len(got), len(large))
	}
}
