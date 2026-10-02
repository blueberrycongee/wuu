package providerfactory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBuildClientAnthropicCredentialIsolation(t *testing.T) {
	for _, tc := range []struct {
		name               string
		provider           config.ProviderConfig
		stored             authstorage.Credentials
		wantKey, wantToken string
	}{
		{name: "saved key", stored: authstorage.Credentials{Type: "api_key", APIKey: "saved-key", Source: authstorage.SourceSaved}, wantKey: "saved-key"},
		{name: "saved bearer", stored: authstorage.Credentials{Type: "auth_token", AuthToken: "saved-token", Source: authstorage.SourceSaved}, wantToken: "saved-token"},
		{name: "configured key", provider: config.ProviderConfig{APIKey: "config-key"}, wantKey: "config-key"},
		{name: "configured bearer", provider: config.ProviderConfig{AuthToken: "config-token"}, wantToken: "config-token"},
		{name: "explicit key environment", provider: config.ProviderConfig{APIKeyEnv: "GATEWAY_KEY"}, wantKey: "env-key"},
		{name: "explicit bearer environment", provider: config.ProviderConfig{AuthTokenEnv: "GATEWAY_TOKEN"}, wantToken: "env-token"},
		{name: "explicit dual credentials", provider: config.ProviderConfig{APIKey: "config-key", AuthToken: "config-token"}, wantKey: "config-key", wantToken: "config-token"},
		{name: "explicit key replaces saved bearer", provider: config.ProviderConfig{APIKey: "config-key"}, stored: authstorage.Credentials{Type: "auth_token", AuthToken: "saved-token", Source: authstorage.SourceSaved}, wantKey: "config-key"},
		{name: "explicit bearer replaces saved key", provider: config.ProviderConfig{AuthTokenEnv: "GATEWAY_TOKEN"}, stored: authstorage.Credentials{Type: "api_key", APIKey: "saved-key", Source: authstorage.SourceSaved}, wantToken: "env-token"},
		{name: "unset explicit env falls back to saved key", provider: config.ProviderConfig{APIKeyEnv: "UNSET_GATEWAY_KEY"}, stored: authstorage.Credentials{Type: "api_key", APIKey: "saved-key", Source: authstorage.SourceSaved}, wantKey: "saved-key"},
		{name: "legacy ambient credentials", wantKey: "ambient-key", wantToken: "ambient-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("WUU_HOME", "")
			t.Setenv("ANTHROPIC_API_KEY", "ambient-key")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-token")
			t.Setenv("GATEWAY_KEY", "env-key")
			t.Setenv("GATEWAY_TOKEN", "env-token")
			t.Setenv("UNSET_GATEWAY_KEY", "")
			if tc.stored.Type != "" {
				store, err := authstorage.ForHome(os.Getenv("HOME"))
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Set("gateway", tc.stored); err != nil {
					t.Fatal(err)
				}
			}
			headers := make(chan http.Header, 1)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
			}))
			defer endpoint.Close()
			provider := tc.provider
			provider.Type, provider.BaseURL, provider.Model = "anthropic", endpoint.URL, "synthetic-model"
			client, err := BuildClient(provider, "gateway")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Chat(context.Background(), providers.ChatRequest{Model: "synthetic-model", Messages: []providers.ChatMessage{{Role: "user", Content: "synthetic request"}}})
			if err != nil {
				t.Fatal(err)
			}
			got := <-headers
			if key := got.Get("X-Api-Key"); key != tc.wantKey {
				t.Errorf("X-Api-Key = %q, want %q", key, tc.wantKey)
			}
			wantAuth := ""
			if tc.wantToken != "" {
				wantAuth = "Bearer " + tc.wantToken
			}
			if auth := got.Get("Authorization"); auth != wantAuth {
				t.Errorf("Authorization = %q, want %q", auth, wantAuth)
			}
		})
	}
}
