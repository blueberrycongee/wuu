package coordinator

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{
			name: "nil → fatal",
			err:  nil,
			want: ErrorClassFatal,
		},
		{
			name: "context cancelled",
			err:  context.Canceled,
			want: ErrorClassCancelled,
		},
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
			want: ErrorClassRetryable,
		},
		{
			name: "wrapped idle timeout stays retryable",
			err:  errors.New("stream request failed: stream idle timeout after 5m0s: context deadline exceeded"),
			want: ErrorClassRetryable,
		},
		{
			name: "context overflow takes precedence over retryable",
			err:  &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true},
			want: ErrorClassContextOverflow,
		},
		{
			name: "auth error",
			err:  &providers.HTTPError{StatusCode: 401, Body: "unauthorized"},
			want: ErrorClassAuth,
		},
		{
			name: "rate limit",
			err:  &providers.HTTPError{StatusCode: 429, Body: "too many requests"},
			want: ErrorClassRetryable,
		},
		{
			name: "server error",
			err:  &providers.HTTPError{StatusCode: 503, Body: "service unavailable"},
			want: ErrorClassRetryable,
		},
		{
			name: "wrapped retryable HTTPError still detected",
			err: &wrappedErr{
				inner: &providers.HTTPError{StatusCode: 502, Body: "bad gateway"},
			},
			want: ErrorClassRetryable,
		},
		{
			name: "string-only retryable hint",
			err:  errors.New("upstream connection refused"),
			want: ErrorClassRetryable,
		},
		{
			name: "completely unknown error → fatal",
			err:  errors.New("model returned empty answer"),
			want: ErrorClassFatal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.err)
			if got != tc.want {
				t.Fatalf("ClassifyError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

type wrappedErr struct {
	inner error
}

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }
