package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestServerSavingAPIKeyClearsPreviousBearerConfig(t *testing.T) {
	for _, oldConfig := range []string{`"auth_token":"old-token"`, `"auth_token_env":"GATEWAY_OLD_TOKEN"`, `"auth_token":"old-token","auth_token_env":"GATEWAY_OLD_TOKEN"`} {
		t.Run(oldConfig, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
			t.Setenv("GATEWAY_OLD_TOKEN", "old-env-token")
			rt := newTestRuntime(t, &fakeClient{})
			headers := make(chan http.Header, 1)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headers <- r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
			}))
			defer endpoint.Close()
			writeProviderSettingsConfig(t, rt, fmt.Sprintf(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"fake-key","model":"fake-model"},"gateway":{"type":"anthropic","base_url":%q,"model":"synthetic-model",%s}}}`, endpoint.URL, oldConfig))
			out := &lockedBuffer{}
			srv := New(rt, out)
			result := requestProviderSettings(t, srv, out, "save-key", `{"provider":"gateway","api_key":"new-key","keep_selection":true}`)
			if result["error"] != nil {
				t.Fatalf("settings update: %v", result["error"])
			}
			saved := readProviderSettingsConfig(t, rt).Providers["gateway"]
			if saved.AuthToken != "" || saved.AuthTokenEnv != "" || saved.APIKey != "" || saved.APIKeyEnv != "" {
				t.Errorf("config retained credential fields after saving key: %+v", saved)
			}
			client, err := providerfactory.BuildClient(saved, "gateway")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Chat(context.Background(), providers.ChatRequest{Model: "synthetic-model", Messages: []providers.ChatMessage{{Role: "user", Content: "synthetic request"}}})
			if err != nil {
				t.Fatal(err)
			}
			got := <-headers
			if got.Get("Authorization") != "" || got.Get("X-Api-Key") != "new-key" {
				t.Fatalf("reloaded credentials: Authorization=%q X-Api-Key=%q", got.Get("Authorization"), got.Get("X-Api-Key"))
			}
		})
	}
}
