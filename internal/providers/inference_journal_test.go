package providers

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeInferenceFailureMessage(t *testing.T) {
	if got := sanitizeInferenceFailureMessage(nil); got != "" {
		t.Fatalf("nil cause = %q, want empty", got)
	}

	tests := []struct {
		name  string
		cause error
		want  string
	}{
		{
			name:  "plain transport error",
			cause: errors.New(`Post "https://api.example.com/v1/messages": EOF`),
			want:  `Post "https://api.example.com/v1/messages": EOF`,
		},
		{
			name:  "bearer token redacted, prefix kept",
			cause: errors.New(`Get "https://api.example.com": Bearer abc123def456 rejected`),
			want:  `Get "https://api.example.com": Bearer [redacted] rejected`,
		},
		{
			name:  "api key assignment redacted",
			cause: fmt.Errorf("dial failed (api_key=%s)", "sk-live-9f8e7d6c5b"),
			want:  "dial failed (api_key=[redacted])",
		},
		{
			name:  "sk prefixed secret redacted",
			cause: errors.New("unauthorized: sk-ant-1234567890abcdef"),
			want:  "unauthorized: sk-[redacted]",
		},
		{
			name:  "token and password redacted case-insensitively",
			cause: errors.New("config Token: t0ken123 and PASSWORD=hunter2"),
			want:  "config Token: [redacted] and PASSWORD=[redacted]",
		},
		{
			name:  "wrapped multiline error collapses to one line",
			cause: fmt.Errorf("stream failed:\n%s", errors.New("connection reset\n\tby peer")),
			want:  "stream failed: connection reset by peer",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeInferenceFailureMessage(tc.cause); got != tc.want {
				t.Fatalf("sanitize = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("message truncated at 200 runes", func(t *testing.T) {
		long := strings.Repeat("超时", 150) // 300 runes
		got := sanitizeInferenceFailureMessage(errors.New(long))
		if n := len([]rune(got)); n != 200 {
			t.Fatalf("truncated length = %d runes, want 200", n)
		}
	})
}

func TestDurableInferenceFailureSanitizesCause(t *testing.T) {
	failure := DurableInferenceFailure(NormalizedFailure{
		Origin:   FailureOriginNetwork,
		Category: FailureNetwork,
		Cause:    errors.New(`Post "https://api.example.com" (token=t0psecret123): connection reset`),
	})
	if failure.Message != `Post "https://api.example.com" (token=[redacted]): connection reset` {
		t.Fatalf("durable message = %q", failure.Message)
	}
	if failure.Origin != FailureOriginNetwork || failure.Category != FailureNetwork {
		t.Fatalf("durable failure lost classification: %+v", failure)
	}
}
