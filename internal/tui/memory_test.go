package tui

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAppendAndLoadMemoryEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	if err := appendMemoryEntry(path, transcriptEntry{Role: "USER", Content: "hello"}); err != nil {
		t.Fatalf("append user entry: %v", err)
	}
	if err := appendMemoryEntry(path, transcriptEntry{Role: "ASSISTANT", Content: "world"}); err != nil {
		t.Fatalf("append assistant entry: %v", err)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Role != "USER" || entries[0].Content != "hello" {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Role != "ASSISTANT" || entries[1].Content != "world" {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}
}

func TestResumeSlashLoadsMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := appendMemoryEntry(path, transcriptEntry{Role: "USER", Content: "resume me"}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: filepath.Join(dir, ".wuu.json"),
		MemoryPath: path,
		RunPrompt: func(_ctx context.Context, _prompt string) (string, error) {
			return "", nil
		},
	})
	m.entries = nil

	msg, handled := m.handleSlash("/resume")
	if !handled {
		t.Fatal("expected /resume to be handled")
	}
	if msg == "" {
		t.Fatal("expected resume response message")
	}
	if len(m.entries) != 1 {
		t.Fatalf("expected one entry after resume, got %d", len(m.entries))
	}
}
