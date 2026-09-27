package appserver

import (
	"strings"
	"testing"
)

func TestRenderLightweightSlashCommandPrompt(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("/debug login failure")
	if !ok {
		t.Fatal("expected /debug to render")
	}
	if display != "/debug login failure" {
		t.Fatalf("display = %q", display)
	}
	if !strings.Contains(content, "login failure") {
		t.Fatalf("rendered prompt missing arguments:\n%s", content)
	}
	if strings.Contains(content, "/debug") {
		t.Fatalf("rendered prompt should not include raw slash command:\n%s", content)
	}
}

func TestRenderLightweightSlashCommandPromptLeavesCompactRaw(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("/compact")
	if ok || display != "" || content != "/compact" {
		t.Fatalf("compact slash should not render as prompt, got content=%q display=%q ok=%v", content, display, ok)
	}
}

func TestIsManualCompactPrompt(t *testing.T) {
	cases := []struct {
		prompt string
		want   bool
	}{
		{"/compact", true},
		{"/compact 后续只保留结论", true},
		{"/COMPACT", true},
		{"/compress", true},
		{"//compact", false},
		{"/compaction", false},
		{"compact", false},
		{"/debug compact the logs", false},
		{"  /compact  ", true},
	}
	for _, c := range cases {
		if got := isManualCompactPrompt(c.prompt); got != c.want {
			t.Errorf("isManualCompactPrompt(%q) = %v, want %v", c.prompt, got, c.want)
		}
	}
}

func TestRenderLightweightSlashCommandPromptKeepsSkillSlashRaw(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("/slides quarterly roadmap")
	if ok || display != "" || content != "/slides quarterly roadmap" {
		t.Fatalf("skill slash should remain raw, got content=%q display=%q ok=%v", content, display, ok)
	}
}

func TestRenderLightweightSlashCommandPromptIgnoresEscapedSlash(t *testing.T) {
	content, display, ok := renderLightweightSlashCommandPrompt("//debug login failure")
	if ok || display != "" || content != "//debug login failure" {
		t.Fatalf("escaped slash should remain raw, got content=%q display=%q ok=%v", content, display, ok)
	}
}
