package grokbuild

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestReadQuotaMapsUsageAndOnlyPositiveCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing" || r.URL.Query().Get("format") != "credits" || r.Header.Get("Authorization") != "Bearer grok-token" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"config":{"creditUsagePercent":0,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","end":"2030-01-01T00:00:00Z"},"onDemandCap":{"val":10},"onDemandUsed":{"val":12}}}`))
	}))
	defer server.Close()
	client, _ := New(ClientConfig{BaseURL: server.URL + "/v1", APIKey: "grok-token", HTTPClient: server.Client()})
	quota, err := client.ReadQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quota.Kind != "subscription" || len(quota.Windows) != 2 || quota.Windows[0].UsedPercent == nil || *quota.Windows[0].UsedPercent != 0 || quota.Windows[1].UsedPercent == nil || *quota.Windows[1].UsedPercent != 120 || quota.Windows[0].WindowMinutes != 10080 {
		t.Fatalf("unexpected windows: %+v", quota.Windows)
	}
}

func TestReadQuotaZeroCapIsNotUnlimitedAndIdentityChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"creditUsagePercent":null,"onDemandCap":{"val":0},"onDemandUsed":{"val":0}}}`))
	}))
	defer server.Close()
	one, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "credential-one", HTTPClient: server.Client()})
	two, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "credential-two", HTTPClient: server.Client()})
	q1, err := one.ReadQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	q2, err := two.ReadQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(q1.Windows) != 1 || q1.Windows[0].UsedPercent != nil || q1.Windows[0].Unlimited || q1.Account.ID == q2.Account.ID || strings.Contains(q1.Account.ID, "credential-one") {
		t.Fatalf("incorrect no-cap or identity behavior: %+v / %+v", q1, q2)
	}
}

func TestReadQuotaErrorsDoNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("grok-token leaked"))
	}))
	defer server.Close()
	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "grok-token", HTTPClient: server.Client()})
	quota, err := client.ReadQuota(context.Background())
	if err == nil || providers.QuotaErrorCode(err) != providers.QuotaErrorSignIn || quota.Account == nil || strings.Contains(err.Error(), "grok-token") {
		t.Fatalf("quota=%+v err=%v", quota, err)
	}
}
