package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestIsRetryable_ContextDeadlineExceeded(t *testing.T) {
	if !IsRetryable(context.DeadlineExceeded) {
		t.Fatal("expected context.DeadlineExceeded to be retryable")
	}
}

func TestIsRetryable_WrappedDeadlineExceeded(t *testing.T) {
	wrapped := fmt.Errorf("stream request failed: request failed: Post https://example.com: %w", context.DeadlineExceeded)
	if !IsRetryable(wrapped) {
		t.Fatal("expected wrapped context.DeadlineExceeded to be retryable")
	}
}

func TestIsRetryable_NetOpError(t *testing.T) {
	err := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("connection refused"),
	}
	if !IsRetryable(err) {
		t.Fatal("expected net.OpError to be retryable")
	}
}

type fakeNetError struct{ timeout bool }

func (e fakeNetError) Error() string   { return "fake net error" }
func (e fakeNetError) Timeout() bool   { return e.timeout }
func (e fakeNetError) Temporary() bool { return false }

func TestIsRetryable_NetError(t *testing.T) {
	if !IsRetryable(fakeNetError{timeout: true}) {
		t.Fatal("expected net.Error (timeout) to be retryable")
	}
	if !IsRetryable(fakeNetError{timeout: false}) {
		t.Fatal("expected net.Error (non-timeout) to be retryable")
	}
}

func TestIsRetryable_AuthError(t *testing.T) {
	if IsRetryable(&HTTPError{StatusCode: 401, Body: "unauthorized"}) {
		t.Fatal("expected 401 to not be retryable")
	}
	if IsRetryable(&HTTPError{StatusCode: 403, Body: "forbidden"}) {
		t.Fatal("expected 403 to not be retryable")
	}
}

func TestIsRetryable_HTTPServerErrors(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 529} {
		if !IsRetryable(&HTTPError{StatusCode: code, Body: "error"}) {
			t.Fatalf("expected HTTP %d to be retryable", code)
		}
	}
}

func TestIsRetryable_IncompleteStreamError(t *testing.T) {
	if !IsRetryable(NewIncompleteStreamError("stream closed before done")) {
		t.Fatal("expected incomplete stream error to be retryable")
	}
}

func TestNewProviderStreamError_Retryable(t *testing.T) {
	err := NewProviderStreamError("1305", "该模型当前访问量过大，请您稍后再试")
	if !IsRetryable(err) {
		t.Fatal("expected zhipu 1305 stream error to be retryable")
	}
}

func TestNewProviderStreamError_ContextOverflow(t *testing.T) {
	err := NewProviderStreamError("400", "prompt is too long for this model")
	if !IsContextOverflow(err) {
		t.Fatal("expected stream error to be classified as context overflow")
	}
	if IsRetryable(err) {
		t.Fatal("expected context overflow stream error to not be retryable")
	}
}

func TestNewProviderStreamError_Auth(t *testing.T) {
	err := NewProviderStreamError("authentication_error", "invalid api key")
	if !IsAuthError(err) {
		t.Fatal("expected stream error to be classified as auth")
	}
	if IsRetryable(err) {
		t.Fatal("expected auth stream error to not be retryable")
	}
}

func TestStreamErrorSummary_RetryableProviderOverload(t *testing.T) {
	err := NewProviderStreamError("1305", "该模型当前访问量过大，请您稍后再试")
	if got := StreamErrorSummary(err); got != "Provider is overloaded" {
		t.Fatalf("unexpected summary: %q", got)
	}
	if got := StreamErrorDisplay(err); got != "Provider is overloaded. Try again in a moment." {
		t.Fatalf("unexpected display: %q", got)
	}
}

func TestStreamErrorSummary_IncompleteClose(t *testing.T) {
	err := NewIncompleteStreamError("stream closed before done")
	if got := StreamErrorSummary(err); got != "Connection ended before completion" {
		t.Fatalf("unexpected summary: %q", got)
	}
	if got := StreamErrorDisplay(err); got != "The connection ended before the reply completed." {
		t.Fatalf("unexpected display: %q", got)
	}
}

func TestStreamErrorSummary_EmptyAnswer(t *testing.T) {
	err := errors.New("model returned empty answer")
	if got := StreamErrorSummary(err); got != "Model returned empty response" {
		t.Fatalf("unexpected summary: %q", got)
	}
	if got := StreamErrorDisplay(err); got != "The model returned an empty response. This is usually a provider compatibility issue — try again or rephrase your prompt." {
		t.Fatalf("unexpected display: %q", got)
	}
}

func TestStreamErrorSummary_EmptyAnswerWithStopReason(t *testing.T) {
	err := fmt.Errorf("stream request failed: %w", errors.New("model returned empty answer (stop_reason=stop)"))
	if got := StreamErrorSummary(err); got != "Model returned empty response" {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func TestStreamErrorSummary_Timeout(t *testing.T) {
	err := errors.New("stream idle timeout after 5m0s: context deadline exceeded")
	if got := StreamErrorSummary(err); got != "Stream timed out" {
		t.Fatalf("unexpected summary: %q", got)
	}
	if got := StreamErrorDisplay(err); got != "Stream timed out. No response chunks arrived in time." {
		t.Fatalf("unexpected display: %q", got)
	}
}

func TestIsRetryable_StringFallback(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"connection refused", true},
		{"connection reset by peer", true},
		{"no such host", true},
		{"context deadline exceeded", true},
		{"read tcp: i/o timeout", true},
		{"unexpected EOF", true},
		{"bad request", false},
	}
	for _, c := range cases {
		got := IsRetryable(errors.New(c.msg))
		if got != c.want {
			t.Fatalf("IsRetryable(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestWithRetry_BailsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	calls := 0
	err := WithRetry(ctx, RetryConfig{MaxRetries: 3, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}, func() error {
		calls++
		return errors.New("should not be called")
	})
	if calls != 0 {
		t.Fatalf("expected 0 fn calls on cancelled ctx, got %d", calls)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got %v", err)
	}
}
