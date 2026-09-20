package providers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"regexp"
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
		cfg.MaxDelay = 60 * time.Second
	}
	if cfg.InitialDelay > cfg.MaxDelay {
		cfg.InitialDelay = cfg.MaxDelay
	}
	return cfg
}

// DefaultRetryConfig returns the provider default: one attempt unless a caller
// explicitly opts into retries.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   0,
		InitialDelay: time.Second,
		MaxDelay:     60 * time.Second,
	}
}

// HTTPError wraps an HTTP status code error.
type HTTPError struct {
	ProviderFamily  string
	ProviderCode    string
	StatusCode      int
	Body            string
	RetryAfter      time.Duration // parsed from Retry-After header, if present
	AuthRefreshable bool
	// ContextOverflow is true when the body indicates the prompt
	// exceeded the model's context window. Callers can use this
	// to trigger an auto-compact rather than a plain retry.
	ContextOverflow bool
}

func (e *HTTPError) Error() string {
	return "HTTP " + strconv.Itoa(e.StatusCode) + ": " + e.Body
}

// StreamError wraps a terminal provider-stream failure that arrived inside
// the live event stream rather than as an HTTP status code.
type StreamError struct {
	ProviderFamily  string
	Message         string
	Code            string
	Retryable       bool
	Auth            bool
	ContextOverflow bool
}

func (e *StreamError) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("stream error (%s): %s", e.Code, e.Message)
	case e.Message != "":
		return e.Message
	case e.Code != "":
		return "stream error (" + e.Code + ")"
	default:
		return "stream error"
	}
}

// NewIncompleteStreamError marks an early stream close as retryable so the
// runner can recover before any user-visible output has been committed.
func NewIncompleteStreamError(message string) *StreamError {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "stream closed before terminal event"
	}
	return &StreamError{
		Message:   msg,
		Retryable: true,
	}
}

// NewNonRetryableStreamError marks a provider stream failure as terminal.
// Use this when replaying the request could duplicate provider-side work.
func NewNonRetryableStreamError(message string) *StreamError {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "stream failed"
	}
	return &StreamError{Message: msg}
}

// NewProviderStreamError classifies a provider-reported streaming error that
// arrived as an SSE event payload rather than as an HTTP status code.
func NewProviderStreamError(code, message string) *StreamError {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "provider reported a streaming error"
	}
	err := &StreamError{
		Message: strings.TrimSpace(msg),
		Code:    strings.TrimSpace(code),
	}
	if isContextOverflowCode(err.Code) || DetectContextOverflow(err.Message) {
		err.ContextOverflow = true
		return err
	}
	if isStreamAuthError(err.Code, err.Message) {
		err.Auth = true
		return err
	}
	if isRetryableStreamError(err.Code, err.Message) {
		err.Retryable = true
	}
	return err
}

// StreamErrorSummary returns a short, stable user-facing summary for live
// stream failures so UIs can show consistent reconnect and failure states.
func StreamErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	var replayBlocked *ReplayBlockedError
	if errors.As(err, &replayBlocked) {
		return "Automatic replay blocked to avoid duplicate tool execution"
	}
	var backpressure *LocalBackpressureError
	if errors.As(err, &backpressure) {
		return "Local stream consumer could not keep up"
	}
	if isEmptyAnswerMessage(err.Error()) {
		return "Model returned empty response"
	}
	if IsAuthError(err) {
		return "Authentication failed"
	}
	if IsContextOverflow(err) {
		return "Context window reached"
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == 529 {
			return "Provider is overloaded"
		}
		if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
			return "Provider request failed"
		}
		switch httpErr.StatusCode {
		case 429:
			return "Provider is overloaded"
		}
	}

	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		if isProviderOverloaded(streamErr.Code, streamErr.Message) {
			return "Provider is overloaded"
		}
		if isTemporaryProviderFailure(streamErr.Code, streamErr.Message) {
			return "Provider request failed"
		}
		msg := normalizeErrorMessage(streamErr.Message)
		switch {
		case isTimeoutMessage(msg):
			return "Stream timed out"
		case isIncompleteStreamMessage(msg):
			return "Connection ended before completion"
		case isConnectionDropMessage(msg):
			return "Connection dropped"
		case msg != "":
			return msg
		}
	}

	msg := normalizeErrorMessage(err.Error())
	switch {
	case isTimeoutMessage(msg):
		return "Stream timed out"
	case isIncompleteStreamMessage(msg):
		return "Connection ended before completion"
	case isConnectionDropMessage(msg):
		return "Connection dropped"
	case msg != "":
		return msg
	default:
		return "Stream request failed"
	}
}

// StreamErrorDisplay returns the final user-visible failure text shown when
// a live response cannot be recovered.
func StreamErrorDisplay(err error) string {
	switch StreamErrorSummary(err) {
	case "":
		return "Unknown stream error"
	case "Authentication failed":
		return "Authentication failed. Check your API key and provider permissions."
	case "Context window reached":
		return "Context window reached. Compact history or start a new thread."
	case "Provider is overloaded":
		return "Provider is overloaded. Try again in a moment."
	case "Provider request failed":
		return "The provider returned a temporary server error. Try again in a moment."
	case "Stream timed out":
		return "Stream timed out. No response chunks arrived in time."
	case "Connection ended before completion":
		return "The connection ended before the reply completed."
	case "Connection dropped":
		return "The connection dropped while streaming the reply."
	case "Model returned empty response":
		return "The model returned an empty response. This is usually a provider compatibility issue — try again or rephrase your prompt."
	case "Automatic replay blocked to avoid duplicate tool execution":
		return "The connection ended after a tool started. Wuu stopped automatic replay to avoid running the tool twice."
	case "Local stream consumer could not keep up":
		return "Wuu could not process provider events fast enough, so it stopped instead of generating the response again."
	default:
		return StreamErrorSummary(err)
	}
}

// contextOverflowPatterns match provider error text that means the prompt
// exceeded the model's context window: one entry per phrasing observed in
// the wild, attributed per provider. Classification gates compaction-vs-
// retry, so entries must be specific enough to never match transient
// failures; numeric fields stay inside the patterns so a rate/quota
// number can't widen the match.
var contextOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),                                 // OpenAI code and prose forms
	regexp.MustCompile(`(?i)exceeds the context window`),                                    // OpenAI Completions & Responses
	regexp.MustCompile(`(?i)context window exceeds limit`),                                  // MiniMax
	regexp.MustCompile(`(?i)maximum context length`),                                        // OpenAI-compatible proxies, OpenRouter, Mistral
	regexp.MustCompile(`(?i)model_context_window_exceeded`),                                 // z.ai finish_reason surfaced as error text
	regexp.MustCompile(`(?i)prompt is too long`),                                            // Anthropic
	regexp.MustCompile(`(?i)request_too_large`),                                             // Anthropic HTTP 413
	regexp.MustCompile(`(?i)input is too long`),                                             // AWS Bedrock
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),                        // Google Gemini
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),                                  // xAI
	regexp.MustCompile(`(?i)reduce the length of the messages`),                             // Groq
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length`),                 // OpenRouter/Poolside
	regexp.MustCompile(`(?i)is longer than the model'?s context length`),                    // Together AI
	regexp.MustCompile(`(?i)prompt token count(?: of)? [\d,]+ exceeds the limit of [\d,]+`), // GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the available context size`),                            // llama.cpp
	regexp.MustCompile(`(?i)greater than the context length`),                               // LM Studio
	regexp.MustCompile(`(?i)exceeded model token limit`),                                    // Kimi For Coding (legacy phrasing)
	regexp.MustCompile(`(?i)message size [\d,]+ exceeds limit`),                             // Kimi For Coding k3: "total message size 2306631 exceeds limit 2097152"
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),             // Ollama
	regexp.MustCompile(`(?i)too long for this model'?s context window`),                     // Grok Build / xAI sampling
	regexp.MustCompile(`(?i)\[input_too_large\]`),                                           // Grok Build sampling code
}

// contextNonOverflowPatterns veto overflow classification for transient quota
// failures whose wording collides with a generic overflow pattern (Bedrock
// throttling formats as "Too many tokens, please wait"; 429 phrasing varies).
var contextNonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)throttl`),
}

// DetectContextOverflow inspects a provider error body and reports
// whether it represents a context-window-exceeded condition. The
// matching is provider-agnostic: exclusion patterns run first, then
// the overflow table.
func DetectContextOverflow(body string) bool {
	if body == "" {
		return false
	}
	for _, pattern := range contextNonOverflowPatterns {
		if pattern.MatchString(body) {
			return false
		}
	}
	for _, pattern := range contextOverflowPatterns {
		if pattern.MatchString(body) {
			return true
		}
	}
	return false
}

func isContextOverflowCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "context_length_exceeded", "input_too_large":
		return true
	default:
		return false
	}
}

// IsContextOverflow returns true if err is an HTTPError flagged as
// context overflow.
func IsContextOverflow(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.ContextOverflow
	}
	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		return streamErr.ContextOverflow
	}
	return false
}

// IsRetryable returns true if the error is worth retrying.
func IsRetryable(err error) bool {
	return PlanRecovery(NormalizeFailure(err)).Retryable()
}

// IsTerminalUsageLimit reports whether a provider code/message describes a
// durable quota or billing limit. Workflow layers use the same classifier as
// inference recovery so a terminal provider failure cannot gain a fresh retry
// budget after the underlying request has already stopped.
func IsTerminalUsageLimit(code, message string) bool {
	return isTerminalUsageLimit(code, message)
}

// IsAuthError returns true if the error is an authentication failure.
func IsAuthError(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 401 || httpErr.StatusCode == 403
	}
	var streamErr *StreamError
	if errors.As(err, &streamErr) {
		return streamErr.Auth
	}
	return false
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
	// Type-based checks first — reliable across Go versions.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true // covers timeouts, DNS, connection errors
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	// String fallback for wrapped errors that lose type info.
	msg := strings.ToLower(err.Error())
	// HTTP/2 transport resets arrive as RST_STREAM with no error code, e.g.
	// "stream error: stream ID 1; NO_ERROR; received from peer", and are not
	// covered by the net.Error/net.OpError type checks above.
	if strings.Contains(msg, "received from peer") || strings.Contains(msg, "stream id") {
		return true
	}
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "eof")
}

func isRetryableStreamError(code, message string) bool {
	if isTerminalUsageLimit(code, message) {
		return false
	}
	if isProviderOverloaded(code, message) || isTemporaryProviderFailure(code, message) {
		return true
	}
	return false
}

func isTerminalUsageLimit(code, message string) bool {
	needle := strings.ToLower(strings.TrimSpace(code + " " + message))
	if needle == "" {
		return false
	}
	terminal := []string{
		"usage_limit_reached",
		"usage limit has been reached",
		"monthly usage limit reached",
		"gousagelimiterror",
		"freeusagelimiterror",
		"insufficient_quota",
		"quota exceeded",
		"out of budget",
		"available balance",
	}
	for _, marker := range terminal {
		if strings.Contains(needle, marker) {
			return true
		}
	}
	return false
}

func isProviderOverloaded(code, message string) bool {
	if isProviderRateLimited(code, message) {
		return true
	}
	code = strings.ToLower(strings.TrimSpace(code))
	switch code {
	case "529", "overloaded_error":
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "overloaded") ||
		strings.Contains(msg, "temporarily unavailable") ||
		strings.Contains(msg, "访问量过大") ||
		strings.Contains(msg, "稍后再试")
}

func isProviderRateLimited(code, message string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "429", "1305", "rate_limit_error", "rate_limit_exceeded":
		return true
	}
	msg := strings.ToLower(message)
	return strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests")
}

func isTemporaryProviderFailure(code, message string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	switch code {
	case "500", "502", "503", "internal_error", "server_error", "api_error", "stream_read_error":
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(msg, "server error") ||
		strings.Contains(msg, "internal error") ||
		strings.Contains(msg, "upstream error")
}

func isStreamAuthError(code, message string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	switch code {
	case "401", "403", "authentication_error", "permission_error", "invalid_api_key":
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "api key")
}

func normalizeErrorMessage(message string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
}

func isTimeoutMessage(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "idle timeout") ||
		strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "timeout")
}

func isIncompleteStreamMessage(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "before done") ||
		strings.Contains(msg, "before [done]") ||
		strings.Contains(msg, "before message_stop") ||
		strings.Contains(msg, "before completion") ||
		strings.Contains(msg, "before response.completed")
}

func isConnectionDropMessage(message string) bool {
	msg := strings.ToLower(message)
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "stream closed") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, " eof") ||
		strings.HasSuffix(msg, "eof") ||
		strings.Contains(msg, "no such host")
}

func isEmptyAnswerMessage(message string) bool {
	return strings.Contains(strings.ToLower(message), "model returned empty answer")
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
