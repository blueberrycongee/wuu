package providers

import (
	"errors"
	"testing"
	"time"
)

func TestPlanRecoveryHTTPStatusSemantics(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		action RecoveryActionKind
	}{
		{name: "gateway timeout", err: &HTTPError{StatusCode: 504, Body: "gateway timeout"}, action: RecoveryReplaySame},
		{name: "temporary openai compatible 404", err: &HTTPError{ProviderFamily: "openai", StatusCode: 404, Body: `{"error":"route warming"}`}, action: RecoveryReplaySame},
		{name: "terminal openai model 404", err: &HTTPError{ProviderFamily: "openai", StatusCode: 404, Body: `{"code":"model_not_found"}`}, action: RecoveryStop},
		{name: "anthropic 404", err: &HTTPError{ProviderFamily: "anthropic", StatusCode: 404, Body: "not found"}, action: RecoveryStop},
		{name: "temporary rate limit", err: &HTTPError{StatusCode: 429, Body: "retry later", RetryAfter: 3 * time.Second}, action: RecoveryWaitThenReplay},
		{name: "terminal quota", err: &HTTPError{StatusCode: 429, Body: "insufficient_quota"}, action: RecoveryStop},
		{name: "terminal 403 quota", err: &HTTPError{StatusCode: 403, Body: "The usage limit has been reached", AuthRefreshable: true}, action: RecoveryStop},
		{name: "terminal 403 permission", err: &HTTPError{StatusCode: 403, Body: "forbidden"}, action: RecoveryStop},
		{name: "billing service 503", err: &HTTPError{StatusCode: 503, Body: "billing service temporarily unavailable"}, action: RecoveryReplaySame},
		{name: "context transform", err: &HTTPError{StatusCode: 413, Body: "context_length_exceeded", ContextOverflow: true}, action: RecoveryTransformPayload},
		{name: "body too large", err: &HTTPError{StatusCode: 413, Body: "nginx client_max_body_size"}, action: RecoveryStop},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := NormalizeFailure(test.err)
			plan := PlanRecovery(failure)
			if plan.Action != test.action {
				t.Fatalf("failure/plan = %+v / %+v, want %s", failure, plan, test.action)
			}
		})
	}
}

func TestPlanRecoveryHTTP2StreamReset(t *testing.T) {
	err := errors.New("read SSE stream: stream error: stream ID 1; NO_ERROR; received from peer")
	failure := NormalizeFailure(err)
	if failure.Origin != FailureOriginNetwork || failure.Category != FailureNetwork {
		t.Fatalf("failure = %+v, want network origin/category", failure)
	}
	if plan := PlanRecovery(failure); plan.Action != RecoveryReplaySame {
		t.Fatalf("plan = %+v, want RecoveryReplaySame", plan)
	}
	if !IsRetryable(err) {
		t.Fatalf("IsRetryable = false, want true")
	}
}

func TestPlanRecoveryStopsLocalBackpressureAndUnsafeReplay(t *testing.T) {
	backpressure := PlanRecovery(NormalizeFailure(&LocalBackpressureError{Component: "responses websocket"}))
	if backpressure.Action != RecoveryStop {
		t.Fatalf("backpressure plan = %+v", backpressure)
	}
	unsafe := PlanRecovery(NormalizeFailure(&ReplayBlockedError{Cause: errors.New("EOF"), Reason: errors.New("tool started")}))
	if unsafe.Action != RecoveryBlockUnsafe {
		t.Fatalf("unsafe replay plan = %+v", unsafe)
	}
}

func TestChatGPTPlanRecovery(t *testing.T) {
	for _, test := range []struct {
		code     string
		status   int
		category FailureCategory
		action   RecoveryActionKind
		retry    bool
	}{
		{"subscription_sharing_usage_limit_exceeded", 429, FailureQuota, RecoveryStop, false},
		{"subscription_sharing_usage_unavailable", 503, FailureServer, RecoveryReplaySame, true},
		{"subscription_sharing_user_unavailable", 503, FailureServer, RecoveryReplaySame, true},
	} {
		for _, err := range []error{NewProviderStreamError(test.code, "ChatGPT plan unavailable"), &HTTPError{StatusCode: test.status, ProviderCode: test.code}} {
			failure := NormalizeFailure(err)
			if failure.Category != test.category || PlanRecovery(failure).Action != test.action || IsRetryable(err) != test.retry {
				t.Errorf("failure=%+v plan=%+v retry=%v", failure, PlanRecovery(failure), IsRetryable(err))
			}
		}
	}
}
