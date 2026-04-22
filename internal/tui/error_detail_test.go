package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestFormatErrorDetails_Nil(t *testing.T) {
	if got := formatErrorDetails(nil); got != "" {
		t.Fatalf("nil error should return empty, got %q", got)
	}
}

func TestFormatErrorDetails_HTTPError_Basic(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 500,
		Body:       `{"error":{"type":"server_error","message":"internal"}}`,
	}
	out := formatErrorDetails(err)
	if !strings.Contains(out, "HTTP 500") {
		t.Fatalf("want HTTP 500 in output, got %q", out)
	}
	if !strings.Contains(out, "server_error") {
		t.Fatalf("want body snippet in output, got %q", out)
	}
}

func TestFormatErrorDetails_HTTPError_RetryAfter(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 429,
		Body:       "rate limited",
		RetryAfter: 45 * time.Second,
	}
	out := formatErrorDetails(err)
	if !strings.Contains(out, "HTTP 429") {
		t.Fatalf("want HTTP 429, got %q", out)
	}
	if !strings.Contains(out, "45s") {
		t.Fatalf("want retry-after 45s, got %q", out)
	}
}

func TestFormatErrorDetails_HTTPError_TruncatesLargeBody(t *testing.T) {
	// 2000 char body: must be trimmed to the budget.
	body := strings.Repeat("x", 2000)
	err := &providers.HTTPError{StatusCode: 400, Body: body}
	out := formatErrorDetails(err)
	if !strings.Contains(out, "…") {
		t.Fatalf("oversize body should be truncated with ellipsis, got len=%d", len(out))
	}
	if len(out) > errorDetailBodyBudget+64 {
		t.Fatalf("truncated output still too big: %d chars", len(out))
	}
}

func TestFormatErrorDetails_StreamError_WithCode(t *testing.T) {
	err := providers.NewProviderStreamError("overloaded_error", "we're busy")
	out := formatErrorDetails(err)
	if !strings.Contains(out, "code: overloaded_error") {
		t.Fatalf("want code line, got %q", out)
	}
	if !strings.Contains(out, "we're busy") {
		t.Fatalf("want message, got %q", out)
	}
}

func TestFormatErrorDetails_StreamError_NoCode(t *testing.T) {
	err := providers.NewIncompleteStreamError("stream closed before message_stop")
	out := formatErrorDetails(err)
	if strings.Contains(out, "code:") {
		t.Fatalf("should not emit code line when empty, got %q", out)
	}
	if !strings.Contains(out, "stream closed") {
		t.Fatalf("want message, got %q", out)
	}
}

func TestFormatErrorDetails_GenericErrorFallback(t *testing.T) {
	err := fmt.Errorf("wrap: %w", errors.New("bad thing happened"))
	out := formatErrorDetails(err)
	if !strings.Contains(out, "bad thing happened") {
		t.Fatalf("want wrapped message in output, got %q", out)
	}
}

func TestFormatErrorDetails_EmptyMessageReturnsEmpty(t *testing.T) {
	// An error with no Error() payload shouldn't produce a dangling
	// empty detail block.
	err := errors.New("")
	if got := formatErrorDetails(err); got != "" {
		t.Fatalf("empty error should return empty, got %q", got)
	}
}

func TestTruncateDetail_PrefersNewlineBoundary(t *testing.T) {
	// Budget = 30. Content has a newline at position 12. We expect the
	// cut to align to that newline, not slice mid-word.
	content := "first line.\nsecond line is much longer than the budget allows"
	out := truncateDetail(content, 30)
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", out)
	}
	// The first line must survive intact.
	if !strings.HasPrefix(out, "first line.") {
		t.Fatalf("expected first line preserved, got %q", out)
	}
}
