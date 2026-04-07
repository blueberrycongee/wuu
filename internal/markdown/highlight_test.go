package markdown

import (
	"strings"
	"testing"
)

func TestHighlightCode_EmptyLang(t *testing.T) {
	code := "fmt.Println(\"hello\")"
	got := HighlightCode(code, "")
	if got != code {
		t.Fatalf("expected unchanged for empty lang, got %q", got)
	}
}

func TestHighlightCode_KnownLang(t *testing.T) {
	code := "package main\n"
	got := HighlightCode(code, "go")
	// chroma should produce ANSI escape codes
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI escapes in output, got %q", got)
	}
}

func TestHighlightCode_UnknownLang(t *testing.T) {
	code := "some text"
	got := HighlightCode(code, "nonexistent-language-xyz")
	// fallback lexer still produces output, but should not crash
	if got == "" {
		t.Fatal("expected non-empty output even for unknown lang")
	}
}
