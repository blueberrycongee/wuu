package providers

import (
	"errors"
	"testing"
)

func TestQuotaErrorCodeIsSafeAndStable(t *testing.T) {
	err := NewQuotaError(QuotaErrorRateLimited)
	if got := QuotaErrorCode(err); got != "rate_limited" {
		t.Fatalf("code = %q", got)
	}
	if got := err.Error(); got != "quota read failed (rate_limited)" {
		t.Fatalf("error exposed unexpected detail: %q", got)
	}
	if got := QuotaErrorCode(errors.New("secret token server response")); got != QuotaErrorInvalidResponse {
		t.Fatalf("untyped error code = %q", got)
	}
}

func TestQuotaAccountIDIsOpaqueAndCredentialScoped(t *testing.T) {
	a := QuotaAccountID("source", "https://example.test", "secret-a")
	b := QuotaAccountID("source", "https://example.test", "secret-b")
	c := QuotaAccountID("source", "https://other.test", "secret-a")
	if a == b || a == c || a == "" {
		t.Fatalf("account IDs not scoped: %q %q %q", a, b, c)
	}
	if a == "secret-a" || a == "secret-b" {
		t.Fatalf("account ID contains credential: %q", a)
	}
}
