package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSubmitPromptFlow(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return "answer to: " + prompt, nil
		},
	})
	m.width = 120
	m.height = 40
	m.relayout()

	m.input.SetValue("hello world")
	nextModel, cmd := m.submit()
	if cmd == nil {
		t.Fatal("expected async command from submit")
	}
	next := nextModel.(Model)
	if !next.pendingRequest {
		t.Fatal("expected pendingRequest=true after submit")
	}

	msg := cmd()
	afterModel, streamCmd := next.Update(msg)
	after := afterModel.(Model)
	if after.pendingRequest {
		t.Fatal("expected pendingRequest=false after model response")
	}
	if !after.streaming {
		t.Fatal("expected streaming=true before stream ticks")
	}

	for after.streaming {
		if streamCmd == nil {
			t.Fatal("expected stream tick command while streaming")
		}
		tick := streamCmd()
		afterModel, streamCmd = after.Update(tick)
		after = afterModel.(Model)
	}
	content := renderEntries(after.entries)
	if !strings.Contains(content, "USER\nhello world") {
		t.Fatalf("missing user entry: %s", content)
	}
	if !strings.Contains(content, "ASSISTANT\nanswer to: hello world") {
		t.Fatalf("missing assistant entry: %s", content)
	}
}

func TestJumpToBottomToggle(t *testing.T) {
	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: "/tmp/.wuu.json",
		RunPrompt: func(_ctx context.Context, prompt string) (string, error) {
			return prompt, nil
		},
	})
	m.width = 100
	m.height = 16
	for i := 0; i < 30; i++ {
		m.appendEntry("assistant", "line")
	}
	m.relayout()
	m.refreshViewport(true)

	if m.showJump {
		t.Fatal("expected no jump hint at bottom")
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	paged := updated.(Model)
	if !paged.showJump {
		t.Fatal("expected jump hint after page up")
	}

	updated, _ = paged.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	jumped := updated.(Model)
	if jumped.showJump {
		t.Fatal("expected jump hint cleared after jump-to-bottom")
	}
}

func renderEntries(entries []transcriptEntry) string {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(e.Role)
		b.WriteString("\n")
		b.WriteString(e.Content)
	}
	return b.String()
}
