package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type quotaRoundTripper func(*http.Request) (*http.Response, error)

func (f quotaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func quotaResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: &http.Request{}}
}

func TestReadQuotaSupportedBalancesAndPlans(t *testing.T) {
	cases := []struct {
		base, body, path string
		kind             string
		windows          int
		balances         int
	}{
		{"https://api.deepseek.com", `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"12.3400"}]}`, "/user/balance", "balance", 0, 1},
		{"https://openrouter.ai/api/v1", `{"data":{"total_credits":25.25,"total_usage":5.1}}`, "/api/v1/credits", "balance", 0, 1},
		{"https://api.kimi.com/coding/v1", `{"usage":{"used":"25","limit":"100","resetTime":"2030-01-01T00:00:00Z"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"20","remaining":"10"}}]}`, "/coding/v1/usages", "plan", 2, 0},
		{"https://open.bigmodel.cn/api/paas/v4", `{"data":{"level":"pro","limits":[{"type":"TIME_LIMIT","unit":"count","number":50,"percentage":90,"nextResetTime":1900000000000}]}}`, "/api/monitor/usage/quota/limit", "plan", 1, 0},
		{"https://api.z.ai/api/paas/v4", `{"data":{"level":"pro","limits":[{"type":"TOKENS_LIMIT","unit":"count","number":100,"percentage":0,"nextResetTime":1900000000000}]}}`, "/api/monitor/usage/quota/limit", "plan", 1, 0},
		{"https://openrouter.ai/api/v1", `{"data":{"total_credits":0,"total_usage":0}}`, "/api/v1/credits", "balance", 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.base+tc.path+tc.body[:10], func(t *testing.T) {
			client, err := New(ClientConfig{BaseURL: tc.base, APIKey: "secret-key", HTTPClient: &http.Client{Transport: quotaRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != strings.TrimPrefix(strings.TrimPrefix(tc.base, "https://"), "http://") && !strings.HasPrefix(tc.base, r.URL.Scheme+"://"+r.URL.Host) {
					t.Errorf("unexpected host: %s", r.URL)
				}
				if r.URL.Path != tc.path {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.path)
				}
				if strings.Contains(tc.base, "bigmodel.cn") || strings.Contains(tc.base, "api.z.ai") {
					if r.Header.Get("Authorization") != "secret-key" {
						t.Errorf("Zhipu auth must be bare key")
					}
				}
				return quotaResponse(http.StatusOK, tc.body), nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			q, err := client.ReadQuota(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if q.Kind != tc.kind || len(q.Windows) != tc.windows || len(q.Balances) != tc.balances {
				t.Fatalf("unexpected quota: %+v", q)
			}
			if strings.Contains(tc.base, "api.kimi.") && (q.Windows[0].UsedPercent == nil || *q.Windows[0].UsedPercent != 50 || q.Windows[1].UsedPercent == nil || *q.Windows[1].UsedPercent != 25) {
				t.Fatalf("Kimi used/remaining windows incorrect: %+v", q.Windows)
			}
			if strings.Contains(tc.base, "open.bigmodel.cn") && (q.Windows[0].Scope != "tools" || q.Windows[0].UsedPercent == nil || *q.Windows[0].UsedPercent != 90) {
				t.Fatalf("TIME_LIMIT must remain tool-scoped: %+v", q.Windows[0])
			}
		})
	}
}

func TestReadQuotaUnsupportedEndpointDoesNotRequest(t *testing.T) {
	called := false
	client, _ := New(ClientConfig{BaseURL: "https://arbitrary.example/v1", APIKey: "secret", HTTPClient: &http.Client{Transport: quotaRoundTripper(func(*http.Request) (*http.Response, error) {
		called = true
		return quotaResponse(http.StatusOK, `{}`), nil
	})}})
	q, err := client.ReadQuota(context.Background())
	if err == nil || providers.QuotaErrorCode(err) != providers.QuotaErrorUnsupported || called || q.Account == nil {
		t.Fatalf("q=%+v err=%v called=%v", q, err, called)
	}
}

func TestReadQuotaOpenRouterPreciseBalanceAndAuthFailure(t *testing.T) {
	client, _ := New(ClientConfig{BaseURL: "https://openrouter.ai/api/v1", APIKey: "key", HTTPClient: &http.Client{Transport: quotaRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer key" {
			return quotaResponse(http.StatusUnauthorized, "credential secret"), nil
		}
		return quotaResponse(http.StatusOK, `{"data":{"total_credits":"0.30000000000000001","total_usage":"0.1"}}`), nil
	})}})
	q, err := client.ReadQuota(context.Background())
	if err != nil || len(q.Balances) != 1 || q.Balances[0].Amount != "0.20000000000000001" {
		t.Fatalf("precise decimal result q=%+v err=%v", q, err)
	}
}
