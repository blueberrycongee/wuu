package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestReadQuotaMapsCodexWindowsAndSafeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" || r.Method != http.MethodGet {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer token-a" || r.Header.Get("X-Quota-Test") != "yes" {
			t.Errorf("missing auth/configured header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":120},"secondary_window":{"used_percent":125,"limit_window_seconds":604800,"reset_at":1800000000,"reset_after_seconds":120}},"additional_rate_limits":[{"limit_name":"codex_extra","metered_feature":"review","rate_limit":{"primary_window":{"used_percent":null,"limit_window_seconds":3600,"reset_at":1800100000}}}],"rate_limit_reset_credits":{"available_count":0}}`))
	}))
	defer server.Close()
	client, err := New(ClientConfig{BaseURL: server.URL + "/backend-api/codex", APIKey: "token-a", Headers: map[string]string{"X-Quota-Test": "yes"}, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	quota, err := client.ReadQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quota.Kind != "subscription" || quota.Plan != "pro" || len(quota.Windows) != 3 {
		t.Fatalf("unexpected quota: %+v", quota)
	}
	if quota.Windows[0].UsedPercent == nil || *quota.Windows[0].UsedPercent != 0 {
		t.Fatalf("genuine zero lost: %+v", quota.Windows[0])
	}
	if quota.Windows[1].UsedPercent == nil || *quota.Windows[1].UsedPercent != 125 {
		t.Fatalf("overage lost: %+v", quota.Windows[1])
	}
	if quota.Windows[0].ResetsAt == "" || quota.Windows[2].UsedPercent != nil || quota.ResetCredits == nil || *quota.ResetCredits != 0 {
		t.Fatalf("unknown/relative reset/credits not preserved: %+v", quota)
	}
	if quota.Windows[1].ResetsAt != "2027-01-15T08:00:00Z" {
		t.Fatalf("absolute reset did not take precedence: %+v", quota.Windows[1])
	}
	if quota.Account == nil || strings.Contains(quota.Account.ID, "token-a") || quota.Account.ID == "" {
		t.Fatalf("unsafe or missing account identity: %+v", quota.Account)
	}
}

func TestReadQuotaFailuresKeepOpaqueAccountAndSafeCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		http int
	}{
		{name: "unauthorized", code: providers.QuotaErrorSignIn, http: http.StatusUnauthorized},
		{name: "rate limited", code: providers.QuotaErrorRateLimited, http: http.StatusTooManyRequests},
		{name: "malformed", code: providers.QuotaErrorInvalidResponse, http: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.http)
				if tc.name == "malformed" {
					_, _ = w.Write([]byte(`{`))
				} else {
					_, _ = w.Write([]byte("private-token-response"))
				}
			}))
			defer server.Close()
			client, _ := New(ClientConfig{BaseURL: server.URL + "/codex", APIKey: "secret", HTTPClient: server.Client()})
			quota, err := client.ReadQuota(context.Background())
			if err == nil || providers.QuotaErrorCode(err) != tc.code {
				t.Fatalf("error = %v, code = %q", err, providers.QuotaErrorCode(err))
			}
			if quota.Account == nil || strings.Contains(quota.Account.ID, "secret") || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("unsafe failure result: %+v, %v", quota, err)
			}
		})
	}
}

func TestReadQuotaNetworkFailureReturnsAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	client, _ := New(ClientConfig{BaseURL: url + "/codex", APIKey: "secret"})
	quota, err := client.ReadQuota(context.Background())
	if err == nil || providers.QuotaErrorCode(err) != providers.QuotaErrorNetwork || quota.Account == nil {
		t.Fatalf("quota=%+v err=%v", quota, err)
	}
}
