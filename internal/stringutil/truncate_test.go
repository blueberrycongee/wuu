package stringutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate_ASCII(t *testing.T) {
	got := Truncate("hello world", 5, "...")
	if got != "hello..." {
		t.Fatalf("got %q", got)
	}
}

func TestTruncate_NoOp(t *testing.T) {
	got := Truncate("hi", 10, "...")
	if got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncate_Emoji(t *testing.T) {
	// 😀 is 4 bytes. Cutting at byte 2 must not split it.
	s := "😀abc"
	got := Truncate(s, 2, "...")
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	// Should cut before the emoji (0 bytes of content) + suffix.
	if got != "..." {
		t.Fatalf("got %q, expected %q", got, "...")
	}
}

func TestTruncate_CJK(t *testing.T) {
	// 你 is 3 bytes. "你好世界" = 12 bytes.
	s := "你好世界"
	got := Truncate(s, 7, "…")
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	// 7 bytes fits 你好 (6 bytes), not 世 (would be 9).
	if got != "你好…" {
		t.Fatalf("got %q, expected %q", got, "你好…")
	}
}

func TestTruncate_ExactBoundary(t *testing.T) {
	s := "abc"
	got := Truncate(s, 3, "...")
	if got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	s := "😀🎉你好"
	got := TruncateRunes(s, 2, "...")
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if got != "😀🎉..." {
		t.Fatalf("got %q", got)
	}
}

func TestHeadTail(t *testing.T) {
	s := strings.Repeat("x", 100)
	got := HeadTail(s, 10, 10, " ... ")
	if len(got) != 10+5+10 {
		t.Fatalf("len=%d, got %q", len(got), got)
	}
}

func TestHeadTail_UTF8(t *testing.T) {
	// Ensure head/tail don't split multi-byte chars.
	s := "你好世界end"
	got := HeadTail(s, 4, 3, "|")
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
}
