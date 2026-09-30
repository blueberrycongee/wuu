package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestReadQuotaOAuthScopesAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" || r.URL.Query().Get("cedar_ember") != "1" || r.URL.Query().Get("skip_spend") != "1" || r.Header.Get("Authorization") != "Bearer oauth-token" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("unexpected request %s %s headers=%v", r.Method, r.URL, r.Header)
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":0,"resets_at":"2030-01-02T00:00:00Z"},"seven_day":{"utilization":120},"seven_day_opus":{"utilization":null},"seven_day_sonnet":{"utilization":35},"limits":[{"kind":"model","percent":null,"resets_at":"2030-01-03T00:00:00Z","scope":{"model":{"display_name":"Claude Opus"}}}]}`))
	}))
	defer server.Close()
	client, err := New(ClientConfig{BaseURL: server.URL + "/v1", AuthToken: "oauth-token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	quota, err := client.ReadQuota(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quota.Kind != "subscription" || len(quota.Windows) != 5 || quota.Windows[0].UsedPercent == nil || *quota.Windows[0].UsedPercent != 0 || quota.Windows[1].UsedPercent == nil || *quota.Windows[1].UsedPercent != 120 || quota.Windows[2].UsedPercent != nil || quota.Windows[4].Model != "Claude Opus" {
		t.Fatalf("unexpected quota: %+v", quota)
	}
}

func TestReadQuotaAPIKeyUnsupportedWithoutNetwork(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "api-key", HTTPClient: server.Client()})
	quota, err := client.ReadQuota(context.Background())
	if err == nil || providers.QuotaErrorCode(err) != providers.QuotaErrorUnsupported || called || quota.Account == nil {
		t.Fatalf("quota=%+v err=%v called=%v", quota, err, called)
	}
}

func TestReadLocalQuotaReadsCredentialFileAndMissingIsSignIn(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	credentials := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "claude-secret", "subscriptionType": "max"}}
	body, _ := json.Marshal(credentials)
	if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: localQuotaRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != anthropicQuotaEndpoint || r.Header.Get("Authorization") != "Bearer claude-secret" {
			t.Errorf("incorrect local credential header")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"five_hour":{"utilization":42}}`)), Request: r}, nil
	})}
	q, err := readLocalQuota(context.Background(), home, client)
	if err != nil || q.Account == nil || q.Plan != "max" || len(q.Windows) != 1 || *q.Windows[0].UsedPercent != 42 {
		t.Fatalf("local credential quota: %+v %v", q, err)
	}
	if err := os.Remove(filepath.Join(configDir, ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	// A missing custom login must not borrow the default macOS login.
	marker := filepath.Join(home, "keychain-read")
	if err := os.WriteFile(filepath.Join(home, "security"), []byte("#!/bin/sh\nprintf called > '"+marker+"'\nprintf '%s' '{\"claudeAiOauth\":{\"accessToken\":\"wrong-account\"}}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", home)
	q, err = readLocalQuota(context.Background(), home, client)
	if err == nil || providers.QuotaErrorCode(err) != providers.QuotaErrorSignIn || q.Account == nil {
		t.Fatalf("missing credential result: %+v %v", q, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("custom credential directory accessed the default keychain")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if _, err := readLocalQuota(context.Background(), home, client); providers.QuotaErrorCode(err) != providers.QuotaErrorSignIn {
		t.Fatalf("isolated home borrowed a login: %v", err)
	}
}

type localQuotaRoundTripper func(*http.Request) (*http.Response, error)

func (f localQuotaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
