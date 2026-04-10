package providers

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxRetries   int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// NormalizeRetryConfig clamps invalid values and fills reasonable defaults.
func NormalizeRetryConfig(cfg RetryConfig) RetryConfig {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = time.Second
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if cfg.InitialDelay > cfg.MaxDelay {
		cfg.InitialDelay = cfg.MaxDelay
	}
	return cfg
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
	}
}

// HTTPError wraps an HTTP status code error.
type HTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration // parsed from Retry-After header, if present
	// ContextOverflow is true when the body indicates the prompt
	// exceeded the model's context window. Callers can use this
	// to trigger an auto-compact rather than a plain retry.
	ContextOverflow bool
}

func (e *HTTPError) Error() string {
	return "HTTP " + strconv.Itoa(e.StatusCode) + ": " + e.Body
}

// DetectContextOverflow inspects a provider error body and reports
// whether it represents a context-window-exceeded condition. The
// matching is provider-agnostic: it covers both OpenAI's
// `context_length_exceeded` code and Anthropic's "prompt is too long"
// style messages.
func DetectContextOverflow(body string) bool {
	if body == "" {
		return false
	}
	msg := strings.ToLower(body)
	return strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "context length exceeded") ||
		strings.Contains(msg, "maximum context length") ||
		strings.Contains(msg, "prompt is too long") ||
		strings.Contains(msg, "input is too long") ||
		strings.Contains(msg, "request too large") ||
		strings.Contains(msg, "too many tokens")
}

// IsContextOverflow returns true if err is an HTTPError flagged as
// context overflow.
func IsContextOverflow(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.ContextOverflow
	}
	return false
}

// IsRetryable returns true if the error is worth retrying.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		// Context-overflow needs compaction, not a blind retry.
		if httpErr.ContextOverflow {
			return false
		}
		switch httpErr.StatusCode {
		case 429, 529: // rate limit, overloaded
			return true
		case 500, 502, 503: // server errors
			return true
		case 401, 403: // auth errors - not retryable
			return false
		}
	}
	// Network errors are retryable
	if isNetworkError(err) {
		return true
	}
	return false
}

// IsAuthError returns true if the error is an authentication failure.
func IsAuthError(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 401 || httpErr.StatusCode == 403
	}
	return false
}

// IsContextWindowError returns true if the error indicates context window exceeded.
func IsContextWindowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "maximum context length") ||
		strings.Contains(msg, "max_tokens")
}

// WithRetry executes fn with exponential backoff retry.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}
		if attempt == cfg.MaxRetries {
			break
		}

		delay := backoffDelay(attempt, cfg.InitialDelay, cfg.MaxDelay, lastErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

func backoffDelay(attempt int, initial, maxDelay time.Duration, err error) time.Duration {
	// Check for Retry-After header
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		if httpErr.RetryAfter > maxDelay {
			return maxDelay
		}
		return httpErr.RetryAfter
	}

	// Exponential backoff with jitter
	base := float64(initial) * math.Pow(2, float64(attempt))
	if base > float64(maxDelay) {
		base = float64(maxDelay)
	}
	// Add 0-25% jitter
	jitter := base * 0.25 * rand.Float64()
	return time.Duration(base + jitter)
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "eof")
}

// ParseRetryAfter extracts Retry-After duration from an HTTP response header.
func ParseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	val := resp.Header.Get("Retry-After")
	if val == "" {
		return 0
	}
	// Try as seconds
	if seconds, err := strconv.Atoi(val); err == nil {
		return time.Duration(seconds) * time.Second
	}
	// Try as HTTP date
	if t, err := http.ParseTime(val); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
