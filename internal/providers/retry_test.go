package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

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
	for _, code := range []int{429, 500, 502, 503, 504, 529} {
		if !IsRetryable(&HTTPError{StatusCode: code, Body: "error"}) {
			t.Fatalf("expected HTTP %d to be retryable", code)
		}
	}
}

func TestIsRetryable_OpenAICompatible404UsesTypedSemantics(t *testing.T) {
	if !IsRetryable(&HTTPError{ProviderFamily: "openai", StatusCode: 404, Body: `{"error":"temporary route miss"}`}) {
		t.Fatal("expected unknown OpenAI-compatible 404 to receive bounded outer retry")
	}
	if IsRetryable(&HTTPError{ProviderFamily: "openai", StatusCode: 404, Body: `{"code":"model_not_found"}`}) {
		t.Fatal("expected definitive model_not_found to stop")
	}
	if IsRetryable(&HTTPError{ProviderFamily: "anthropic", StatusCode: 404, Body: "not found"}) {
		t.Fatal("expected non-OpenAI 404 to stop")
	}
}

func TestIsRetryable_TerminalUsageLimit(t *testing.T) {
	cases := []string{
		`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`,
		`{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`,
		`Monthly usage limit reached`,
		`available balance is too low`,
	}
	for _, body := range cases {
		if IsRetryable(&HTTPError{StatusCode: 429, Body: body}) {
			t.Fatalf("expected terminal usage limit to not be retryable: %s", body)
		}
	}
	if !IsRetryable(&HTTPError{StatusCode: 429, Body: "temporary rate limit, retry later"}) {
		t.Fatal("expected temporary rate limit to remain retryable")
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

func TestNewProviderStreamError_StreamReadErrorRetryable(t *testing.T) {
	err := NewProviderStreamError("stream_read_error", "stream_read_error")
	if !IsRetryable(err) {
		t.Fatal("expected provider stream read error to be retryable")
	}
}

func TestNewProviderStreamError_TerminalUsageLimit(t *testing.T) {
	err := NewProviderStreamError("usage_limit_reached", "The usage limit has been reached")
	if IsRetryable(err) {
		t.Fatal("expected terminal usage-limit stream error to not be retryable")
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

func TestNewProviderStreamError_ContextOverflowCode(t *testing.T) {
	err := NewProviderStreamError("context_length_exceeded", "")
	if !IsContextOverflow(err) {
		t.Fatal("expected context_length_exceeded code to be classified as context overflow")
	}
	if IsRetryable(err) {
		t.Fatal("expected context overflow stream error to not be retryable")
	}
}

func TestDetectContextOverflow_OpenAIResponsesMessage(t *testing.T) {
	msg := "Your input exceeds the context window of this model. Please adjust your input and try again."
	if !DetectContextOverflow(msg) {
		t.Fatal("expected Responses context-window message to be detected")
	}
}

func TestDetectContextOverflow_MiniMaxMessage(t *testing.T) {
	msg := `HTTP 400: {"type":"error","error":{"type":"invalid_request_error","message":"invalid params, context window exceeds limit (2013)"}}`
	if !DetectContextOverflow(msg) {
		t.Fatal("expected MiniMax context-window message to be detected")
	}
}

func TestDetectContextOverflow_KimiMessageSizeLimit(t *testing.T) {
	msg := `HTTP 400: 400 Bad Request: {"error":{"type":"invalid_request_error","message":"total message size 2306631 exceeds limit 2097152"},"type":"error"}`
	if !DetectContextOverflow(msg) {
		t.Fatal("expected Kimi message-size-limit message to be detected")
	}
}

func TestDetectContextOverflow_DoesNotGuessFromRateLimitPhrasing(t *testing.T) {
	msg := `HTTP 429: {"error":{"type":"rate_limit_error","message":"request rate exceeds limit, slow down"}}`
	if DetectContextOverflow(msg) {
		t.Fatal("rate-limit phrasing without a message-size signal must not be classified as context overflow")
	}
}

func TestDetectContextOverflow_ProviderPhrasings(t *testing.T) {
	overflows := []string{
		// Anthropic
		"prompt is too long: 213462 tokens > 200000 maximum",
		`413 {"error":{"type":"request_too_large","message":"Request exceeds the maximum size"}}`,
		// OpenAI / OpenAI-compatible
		"context_length_exceeded",
		"Requested token count exceeds the model's maximum context length of 131072 tokens",
		// Google
		"The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
		// xAI
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",
		"Failed to start sampling: [input_too_large] The prompt is too long for this model's context window (505759 tokens > 500000 tokens)",
		// Groq
		"Please reduce the length of the messages or completion",
		// OpenRouter
		"This endpoint's maximum context length is 200000 tokens. However, you requested about 240000 tokens",
		"Input length 265330 exceeds the maximum allowed input length of 262144 tokens.",
		// Together AI
		"The input (300000 tokens) is longer than the model's context length (262144 tokens).",
		// GitHub Copilot
		"prompt token count of 240000 exceeds the limit of 200000",
		// llama.cpp
		"the request exceeds the available context size, try increasing it",
		// LM Studio
		"tokens to keep from the initial prompt is greater than the context length",
		// Kimi For Coding
		"Your request exceeded model token limit: 2097152 (requested: 2306631)",
		"total message size 2306631 exceeds limit 2097152",
		// Ollama
		"prompt too long; exceeded max context length by 4021 tokens",
		// Mistral
		"Prompt contains 240000 tokens, too large for model with 128000 maximum context length",
	}
	for _, msg := range overflows {
		if !DetectContextOverflow(msg) {
			t.Errorf("expected overflow detection for %q", msg)
		}
	}

	nonOverflows := []string{
		// 429s and throttling must not trigger lossy compaction.
		`429 {"error":{"type":"rate_limit_error","message":"We're receiving too many requests at the moment."}}`,
		"request rate exceeds limit, slow down",
		"Throttling error: Too many tokens, please wait before trying again.",
		"429: too many tokens per minute",
		"monthly token limit exceeded",
		"request count exceeds the limit of 100",
		// Gateway byte-buffer limits are not context windows.
		"HTTP 507: 507 Insufficient Storage: exceeded request buffer limit while retrying upstream",
	}
	for _, msg := range nonOverflows {
		if DetectContextOverflow(msg) {
			t.Errorf("expected non-overflow for %q", msg)
		}
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
}

func TestStreamErrorSummary_IncompleteClose(t *testing.T) {
	err := NewIncompleteStreamError("stream closed before done")
	if got := StreamErrorSummary(err); got != "Connection ended before completion" {
		t.Fatalf("unexpected summary: %q", got)
	}
}

func TestStreamErrorSummary_EmptyAnswer(t *testing.T) {
	err := errors.New("model returned empty answer")
	if got := StreamErrorSummary(err); got != "Model returned empty response" {
		t.Fatalf("unexpected summary: %q", got)
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
