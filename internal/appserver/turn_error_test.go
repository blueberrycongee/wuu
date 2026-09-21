package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBuildTurnError_SerializesFactsWithoutActions(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 404,
		Body:       `{"error":{"code":"internal_error","message":"resource not found"}}`,
	}
	out := BuildTurnError(err, "compatible")

	encoded, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		t.Fatalf("marshal turn error: %v", marshalErr)
	}
	wire := string(encoded)
	if strings.Contains(wire, `"action"`) {
		t.Fatalf("turn error must contain diagnostic facts only, got %s", wire)
	}
	for _, fact := range []string{`"code":"internal_error"`, `"provider":"compatible"`, `"status_code":404`} {
		if !strings.Contains(wire, fact) {
			t.Errorf("turn error lost diagnostic fact %s: %s", fact, wire)
		}
	}
}

func TestBuildTurnError_InvalidRequestCategory(t *testing.T) {
	tests := []error{
		&providers.HTTPError{StatusCode: 400},
		&providers.HTTPError{
			StatusCode: 400,
			Body:       `{"error":{"type":"invalid_request_error","message":"Invalid request Error"},"type":"error"}`,
		},
		&providers.StreamError{Code: "400", Message: "bad request", Retryable: true},
		fmt.Errorf("HTTP 400: malformed request"),
	}
	for _, err := range tests {
		out := BuildTurnError(err, "kimi-code")
		if out.Category != "invalid_request" {
			t.Fatalf("%T category = %q, want invalid_request", err, out.Category)
		}
	}
}

func TestBuildTurnError_InternalMessageSequenceIsNotNetwork(t *testing.T) {
	err := errors.New("stream request failed: invalid message sequence after tool-call history repair: message 2: system message must precede all non-system messages")
	out := BuildTurnError(err, "openai-codex")
	if out.Category != "internal" || out.Code != "invalid_message_sequence" {
		t.Fatalf("turn error = %#v, want internal invalid_message_sequence", out)
	}
}

// TestBuildTurnError_Nil covers the no-error path. BuildTurnError
// must not panic on nil and must still surface the provider so the
// front-end can show "Provider: openai" even when the body is empty.
func TestBuildTurnError_Nil(t *testing.T) {
	out := BuildTurnError(nil, "openai")
	if out.Message != "" {
		t.Errorf("expected empty message, got %q", out.Message)
	}
	if out.Provider != "openai" {
		t.Errorf("expected provider=openai, got %q", out.Provider)
	}
}

func TestBuildTurnError_GrokPromptStallIsProvider(t *testing.T) {
	err := errors.New("Grok did not respond to the prompt at all (no wire activity for 30s). the agent process is likely wedged by a stale shared leader or a hung startup check")
	out := BuildTurnError(err, "grok")
	if out.Category != "provider" {
		t.Fatalf("category = %q, want provider", out.Category)
	}
}

// TestBuildTurnError_HTTP401_Auth covers the OpenAI "invalid API key"
// path: HTTP 401 with no parseable code in the body, classified as
// auth while retaining the provider and HTTP status facts.
func TestBuildTurnError_HTTP401_Auth(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 401,
		Body:       `{"error": {"message": "Incorrect API key provided."}}`,
	}
	out := BuildTurnError(err, "openai")
	if out.Category != string("auth") {
		t.Errorf("expected category=auth, got %q", out.Category)
	}
	if out.StatusCode != 401 {
		t.Errorf("expected status_code=401, got %d", out.StatusCode)
	}
	if out.Provider != "openai" {
		t.Errorf("expected provider=openai, got %q", out.Provider)
	}
}

// TestBuildTurnError_HTTP429_OpenAIQuota covers the OpenAI
// insufficient_quota path: HTTP 429 with code=insufficient_quota
// in the body and classified as provider.
func TestBuildTurnError_HTTP429_OpenAIQuota(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 429,
		Body:       `{"error": {"code": "insufficient_quota", "message": "You exceeded your current quota."}}`,
	}
	out := BuildTurnError(err, "openai")
	if out.Category != string("provider") {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
	if out.Code != "insufficient_quota" {
		t.Errorf("expected code=insufficient_quota, got %q", out.Code)
	}
}

func TestBuildTurnError_HTTP429PrefixedProviderBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "openai compatible error.code",
			body: `429 Too Many Requests: {"error": {"code": "insufficient_quota", "message": "You exceeded your current quota."}}`,
			want: "insufficient_quota",
		},
		{
			name: "anthropic compatible error.type",
			body: `429 Too Many Requests: {"error": {"type": "rate_limit_error", "message": "Rate limit exceeded."}}`,
			want: "rate_limit_error",
		},
		{
			name: "glm compatible error.status",
			body: `429 Too Many Requests: {"error": {"code": 7, "message": "Resource has been exhausted", "status": "RESOURCE_EXHAUSTED"}}`,
			want: "RESOURCE_EXHAUSTED",
		},
		{
			name: "sse data top-level code",
			body: "429 Too Many Requests: event:error\n" +
				`data:{"request_id":"req_1","code":"Throttling","message":"Allocated quota exceeded. Please increase your quota limit."}`,
			want: "Throttling",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &providers.HTTPError{
				StatusCode: 429,
				Body:       tt.body,
			}
			out := BuildTurnError(err, "compatible")
			if out.Category != "provider" {
				t.Fatalf("expected category=provider, got %q", out.Category)
			}
			if out.Code != tt.want {
				t.Fatalf("expected code=%q, got %q", tt.want, out.Code)
			}
		})
	}
}

// TestBuildTurnError_HTTPContextOverflow covers the typed context
// overflow path: the HTTPError has ContextOverflow=true so the
// category is provider regardless of the HTTP status code.
func TestBuildTurnError_HTTPContextOverflow(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode:      400,
		ContextOverflow: true,
		Body:            `{"error": {"code": "context_length_exceeded", "message": "input too long"}}`,
	}
	out := BuildTurnError(err, "anthropic")
	if out.Category != string("provider") {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
	if out.Code != "context_length_exceeded" {
		t.Errorf("expected code=context_length_exceeded, got %q", out.Code)
	}
}

// TestBuildTurnError_StreamAnthropicRateLimit covers the Anthropic
// stream-error path: Code=rate_limit_error sets the category to
// provider and the code field carries the wire-level error name.
func TestBuildTurnError_StreamAnthropicRateLimit(t *testing.T) {
	err := &providers.StreamError{
		Code:    "rate_limit_error",
		Message: "Rate limit exceeded.",
	}
	out := BuildTurnError(err, "anthropic")
	if out.Category != string("provider") {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
	if out.Code != "rate_limit_error" {
		t.Errorf("expected code=rate_limit_error, got %q", out.Code)
	}
}

func TestBuildTurnError_StreamProviderQuotaCodes(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		message string
	}{
		{
			name:    "throttling",
			code:    "Throttling",
			message: "Allocated quota exceeded. Please increase your quota limit.",
		},
		{
			name:    "resource exhausted",
			code:    "RESOURCE_EXHAUSTED",
			message: "Resource has been exhausted.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := providers.NewProviderStreamError(tt.code, tt.message)
			out := BuildTurnError(err, "compatible")
			if out.Category != "provider" {
				t.Fatalf("expected category=provider, got %q", out.Category)
			}
			if out.Code != tt.code {
				t.Fatalf("expected code=%q, got %q", tt.code, out.Code)
			}
		})
	}
}

// TestBuildTurnError_StreamAuth covers the typed auth path via
// StreamError.Auth = true.
func TestBuildTurnError_StreamAuth(t *testing.T) {
	err := &providers.StreamError{
		Code:    "authentication_error",
		Message: "Invalid API key.",
		Auth:    true,
	}
	out := BuildTurnError(err, "anthropic")
	if out.Category != string("auth") {
		t.Errorf("expected category=auth, got %q", out.Category)
	}
	if out.Code != "authentication_error" {
		t.Errorf("expected code=authentication_error, got %q", out.Code)
	}
}

// TestBuildTurnError_StreamContextOverflow covers typed
// StreamError.ContextOverflow — the diagnostic category stays provider.
func TestBuildTurnError_StreamContextOverflow(t *testing.T) {
	err := &providers.StreamError{
		Code:            "context_length_exceeded",
		Message:         "input too long",
		ContextOverflow: true,
	}
	out := BuildTurnError(err, "openai")
	if out.Category != string("provider") {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
}

// TestBuildTurnError_ResponseCompletedMissing covers a Responses stream that
// produced partial output but closed before the terminal response.completed
// event. This should not collapse to the generic network-error chip.
func TestBuildTurnError_ResponseCompletedMissing(t *testing.T) {
	err := providers.NewNonRetryableStreamError("websocket stream closed after provider event: websocket stream closed before response.completed")

	out := BuildTurnError(err, "openai-codex")
	if out.Category != "provider" {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
	if out.Code != "stream_closed_before_response.completed" {
		t.Errorf("expected stream_closed_before_response.completed code, got %q", out.Code)
	}
}

// TestBuildTurnError_OverloadedAnthropic covers the Anthropic
// 529 "overloaded" path. The body is the stream-error wrapper from
// wuu's Go core ("stream request failed: stream error
// (overloaded_error)"), and the code extraction pulls "overloaded_error"
// out of the parens.
func TestBuildTurnError_OverloadedAnthropic(t *testing.T) {
	err := errors.New("stream request failed: stream error (overloaded_error)")
	out := BuildTurnError(err, "anthropic")
	if out.Code != "overloaded_error" {
		t.Errorf("expected code=overloaded_error, got %q", out.Code)
	}
	if out.Category != string("provider") {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
}

// TestBuildTurnError_Cancellation covers the user-stop path. A plain
// "context canceled" error remains a cancellation fact.
func TestBuildTurnError_Cancellation(t *testing.T) {
	err := errors.New("context canceled")
	out := BuildTurnError(err, "openai")
	if out.Category != string("cancelled") {
		t.Errorf("expected category=cancelled, got %q", out.Category)
	}
}

// TestBuildTurnError_InternalFallback covers the unrecognized-error
// path. The message has no category-specific token so the classifier
// falls back to "internal".
func TestBuildTurnError_InternalFallback(t *testing.T) {
	err := errors.New("panic: nil pointer dereference")
	out := BuildTurnError(err, "")
	if out.Category != string("internal") {
		t.Errorf("expected category=internal, got %q", out.Category)
	}
}

// TestBuildTurnError_MaxStepsExceededDoesNotExposeLimitAsCode covers
// the agent loop's local safety limit. The "(8)" suffix is the max-step
// value, not a provider error code, so it must not become the visible
// turn chip title.
func TestBuildTurnError_MaxStepsExceededDoesNotExposeLimitAsCode(t *testing.T) {
	err := errors.New("max steps exceeded (8)")
	out := BuildTurnError(err, "anthropic-gateway")
	if out.Category != string("internal") {
		t.Errorf("expected category=internal, got %q", out.Category)
	}
	if out.Code != "" {
		t.Errorf("expected no code for local max-step limit, got %q", out.Code)
	}
}

// TestBuildTurnError_LocalPermissionDenied covers the local
// permissions path. The "permission denied: file" combination
// triggers isLocalOperationError.
func TestBuildTurnError_LocalPermissionDenied(t *testing.T) {
	err := errors.New("permission denied: file /etc/hosts")
	out := BuildTurnError(err, "")
	if out.Category != string("local") {
		t.Errorf("expected category=local, got %q", out.Category)
	}
}

// TestBuildTurnError_HTTPNetwork5xx covers a 503 with no parseable
// body. The category is network.
func TestBuildTurnError_HTTPNetwork5xx(t *testing.T) {
	err := &providers.HTTPError{StatusCode: 503, Body: "Service Unavailable"}
	out := BuildTurnError(err, "openai")
	if out.Category != string("network") {
		t.Errorf("expected category=network, got %q", out.Category)
	}
}

// TestBuildTurnError_GeminiResourceExhausted covers a Gemini-style
// response where the code is in "error.code" (number) and the
// status is a string like "RESOURCE_EXHAUSTED". extractCodeFromBody
// pulls the string from error.type.
func TestBuildTurnError_GeminiResourceExhausted(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 429,
		Body:       `{"error": {"code": 7, "message": "Resource has been exhausted", "status": "RESOURCE_EXHAUSTED"}}`,
	}
	out := BuildTurnError(err, "gemini")
	if out.Code != "RESOURCE_EXHAUSTED" {
		t.Errorf("expected code=RESOURCE_EXHAUSTED, got %q", out.Code)
	}
}

// TestBuildTurnError_AnthropicRequestTooLarge covers the
// "request_too_large" Anthropic 413 body, where the code is in
// error.type.
func TestBuildTurnError_AnthropicRequestTooLarge(t *testing.T) {
	err := &providers.HTTPError{
		StatusCode: 413,
		Body:       `{"error": {"type": "request_too_large", "message": "Request exceeds the maximum size"}}`,
	}
	out := BuildTurnError(err, "anthropic")
	if out.Code != "request_too_large" {
		t.Errorf("expected code=request_too_large, got %q", out.Code)
	}
	if out.Category != "provider" {
		t.Errorf("expected category=provider, got %q", out.Category)
	}
}
