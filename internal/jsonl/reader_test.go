package jsonl

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestForEachLineHandlesLargeRecord(t *testing.T) {
	large := strings.Repeat("x", 3*1024*1024)
	input := large + "\nsmall\n"

	var got []string
	err := ForEachLine(strings.NewReader(input), func(line []byte) error {
		got = append(got, string(bytes.TrimSpace(line)))
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachLine: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0] != large {
		t.Fatalf("unexpected large line length: got %d want %d", len(got[0]), len(large))
	}
	if got[1] != "small" {
		t.Fatalf("unexpected second line: %q", got[1])
	}
}

func TestForEachLineStopsEarly(t *testing.T) {
	input := "first\nsecond\nthird\n"
	var got []string

	err := ForEachLine(strings.NewReader(input), func(line []byte) error {
		got = append(got, string(bytes.TrimSpace(line)))
		if len(got) == 2 {
			return ErrStop
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachLine: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines before stop, got %d", len(got))
	}
	if got[0] != "first" || got[1] != "second" {
		t.Fatalf("unexpected lines before stop: %#v", got)
	}
}

func TestForEachLineReturnsCallbackError(t *testing.T) {
	want := errors.New("boom")
	err := ForEachLine(strings.NewReader("line\n"), func(_ []byte) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected callback error, got %v", err)
	}
}

func TestForEachLineZig_HandlesLargeRecord(t *testing.T) {
	large := strings.Repeat("x", 3*1024*1024)
	input := large + "\nsmall\n"

	var got []string
	err := ForEachLineZig(strings.NewReader(input), func(line []byte) error {
		got = append(got, string(bytes.TrimSpace(line)))
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachLineZig: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0] != large {
		t.Fatalf("unexpected large line length: got %d want %d", len(got[0]), len(large))
	}
	if got[1] != "small" {
		t.Fatalf("unexpected second line: %q", got[1])
	}
}

func TestForEachLineZig_StopsEarly(t *testing.T) {
	input := "first\nsecond\nthird\n"
	var got []string

	err := ForEachLineZig(strings.NewReader(input), func(line []byte) error {
		got = append(got, string(bytes.TrimSpace(line)))
		if len(got) == 2 {
			return ErrStop
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachLineZig: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines before stop, got %d", len(got))
	}
	if got[0] != "first" || got[1] != "second" {
		t.Fatalf("unexpected lines before stop: %#v", got)
	}
}

func TestForEachLineZig_ReturnsCallbackError(t *testing.T) {
	want := errors.New("boom")
	err := ForEachLineZig(strings.NewReader("line\n"), func(_ []byte) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected callback error, got %v", err)
	}
}

func TestForEachLineZig_EmptyInput(t *testing.T) {
	var count int
	err := ForEachLineZig(strings.NewReader(""), func(_ []byte) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachLineZig: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 lines for empty input, got %d", count)
	}
}

func TestForEachLineZig_NoTrailingNewline(t *testing.T) {
	input := "first\nsecond"
	var got []string
	err := ForEachLineZig(strings.NewReader(input), func(line []byte) error {
		got = append(got, string(line))
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachLineZig: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got))
	}
	if got[0] != "first\n" {
		t.Fatalf("first line: got %q, want %q", got[0], "first\n")
	}
	if got[1] != "second" {
		t.Fatalf("second line: got %q, want %q", got[1], "second")
	}
}
