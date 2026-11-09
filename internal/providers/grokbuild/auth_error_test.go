package grokbuild

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestCredentialErrorReplaysGenericProxyAuthRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "http 401",
			err: &providers.HTTPError{
				ProviderFamily: "openai",
				StatusCode:     http.StatusUnauthorized,
				Body:           `401 Unauthorized: {"error":"Authentication required"}`,
			},
		},
		{
			name: "stream 401",
			err: &providers.StreamError{
				ProviderFamily: "openai",
				Code:           "401",
				Message:        `{"error":"Authentication required"}`,
				Auth:           true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := credentialError(test.err)
			var transient *transientProxyAuthError
			if !errors.As(got, &transient) {
				t.Fatalf("error = %v, want transientProxyAuthError", got)
			}
			if strings.Contains(got.Error(), "grok login") {
				t.Fatalf("error = %q, must not ask to grok login", got)
			}
			plan := providers.PlanRecovery(providers.NormalizeFailure(got))
			if plan.Action != providers.RecoveryReplaySame {
				t.Fatalf("plan = %+v, want replay_same_payload", plan)
			}
			if !providers.IsRetryable(got) {
				t.Fatal("IsRetryable = false, want true")
			}
			if !errors.Is(got, test.err) {
				t.Fatalf("wrapped error lost original: %v", got)
			}
		})
	}
}

func TestCredentialErrorStopsDiagnosticAuthRejection(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "expired credentials",
			err: &providers.HTTPError{
				StatusCode: http.StatusUnauthorized,
				Body:       `401 Unauthorized: {"error":"Invalid or expired credentials (auth_kind=bearer, x_xai_token_auth=xai-grok-cli, upstream=PermissionDenied, reason=no auth context)"}`,
			},
		},
		{
			name: "incorrect api key",
			err: &providers.HTTPError{
				StatusCode: http.StatusUnauthorized,
				Body:       `401 Unauthorized: {"error":{"message":"Incorrect API key provided."}}`,
			},
		},
		{
			name: "generic 403",
			err: &providers.HTTPError{
				StatusCode: http.StatusForbidden,
				Body:       `403 Forbidden: {"error":"Authentication required"}`,
			},
		},
		{
			name: "unrecognized 401 body",
			err: &providers.HTTPError{
				StatusCode: http.StatusUnauthorized,
				Body:       "401 Unauthorized: expired",
			},
		},
		{
			name: "stream invalid api key",
			err: &providers.StreamError{
				Code:    "401",
				Message: "invalid api key",
				Auth:    true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := credentialError(test.err)
			if !strings.Contains(got.Error(), "grok login") {
				t.Fatalf("error = %q, want grok login", got)
			}
			var transient *transientProxyAuthError
			if errors.As(got, &transient) {
				t.Fatalf("error = %v, must not be transient", got)
			}
			plan := providers.PlanRecovery(providers.NormalizeFailure(got))
			if plan.Action != providers.RecoveryStop {
				t.Fatalf("plan = %+v, want stop", plan)
			}
			if providers.IsRetryable(got) {
				t.Fatal("IsRetryable = true, want false")
			}
		})
	}
}

func TestCredentialErrorLeavesNonAuthErrorsUnchanged(t *testing.T) {
	orig := &providers.HTTPError{StatusCode: http.StatusGatewayTimeout, Body: "gateway timeout"}
	got := credentialError(orig)
	if got != orig {
		t.Fatalf("error = %v, want original", got)
	}
	plan := providers.PlanRecovery(providers.NormalizeFailure(got))
	if plan.Action != providers.RecoveryReplaySame {
		t.Fatalf("plan = %+v, want replay_same_payload", plan)
	}
}
