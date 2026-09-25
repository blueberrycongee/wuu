package providerfactory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBuildClient_OpenAICompatible(t *testing.T) {
	t.Setenv("TEST_WUU_KEY", "abc")

	client, err := BuildClient(config.ProviderConfig{
		Type:      "openai-compatible",
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "TEST_WUU_KEY",
		Model:     "gpt-test",
	}, "missing-test")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
}

func TestBuildClient_OpenAICodexDoesNotRequireAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	client, err := BuildClient(config.ProviderConfig{
		Type:    "openai-codex",
		Model:   "gpt-5-codex",
		WireAPI: "responses",
	}, "openai-codex")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
}

func TestBuildClient_OpenAICodexUsesCodexCredentialsWhenConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	token := providerFactoryFakeJWT(t, time.Now().Add(time.Hour), "acct_factory")
	writeProviderFactoryCodexAuth(t, codexHome, token)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("chatgpt-account-id"); got != "acct_factory" {
			t.Fatalf("chatgpt-account-id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := BuildClient(config.ProviderConfig{
		Type:                  "openai-codex",
		BaseURL:               server.URL,
		WireAPI:               "responses",
		Model:                 "gpt-5-codex",
		ReuseCodexCredentials: true,
	}, "openai-codex")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-5-codex",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q, want ok", resp.Content)
	}
}

func TestBuildClient_AnthropicUsesConfiguredModelOutputLimit(t *testing.T) {
	t.Setenv("TEST_ANTHROPIC_KEY", "abc")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.MaxTokens != 131_072 {
			t.Fatalf("max_tokens = %d, want 131072", body.MaxTokens)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := BuildClient(config.ProviderConfig{
		Type:      "anthropic",
		BaseURL:   server.URL,
		APIKeyEnv: "TEST_ANTHROPIC_KEY",
		Model:     "k3",
		Models: map[string]config.ProviderModelConfig{
			"k3": {Limit: &config.ProviderModelLimitConfig{Output: 131_072}},
		},
	}, "kimi-for-coding")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	if _, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    "k3",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
}

func TestBuildClient_XAISubscriptionUsesStoredOAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", "")
	token := "supergrok-factory"
	if err := os.MkdirAll(filepath.Join(home, ".wuu"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("xai-subscription", authstorage.Credentials{
		Type:         "oauth",
		AccessToken:  token,
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
		AuthMode:     "xai",
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := BuildClient(config.ProviderConfig{
		Type:    "xai-subscription",
		BaseURL: server.URL,
		WireAPI: "responses",
		Model:   "grok-4.6",
	}, "xai-subscription")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    "grok-4.6",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q, want ok", resp.Content)
	}
}

func TestSupportsNativeToolDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider config.ProviderConfig
		model    string
		options  map[string]any
		want     bool
	}{
		{
			name:     "openai responses unsupported model",
			provider: config.ProviderConfig{Type: "openai-compatible", WireAPI: "responses"},
			model:    "gpt-test",
			want:     false,
		},
		{
			name:     "openai responses supported model",
			provider: config.ProviderConfig{Type: "openai-compatible", WireAPI: "responses"},
			model:    "gpt-5.4",
			want:     true,
		},
		{
			name:     "openai chat fallback",
			provider: config.ProviderConfig{Type: "openai-compatible"},
			model:    "gpt-test",
			want:     false,
		},
		{
			name:     "first party anthropic",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://api.anthropic.com"},
			model:    "claude-sonnet-4-5",
			want:     true,
		},
		{
			name:     "anthropic compatible explicit native",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://anthropic-proxy.example.com"},
			model:    "claude-sonnet-4-5",
			want:     true,
		},
		{
			name:     "anthropic compatible generic model explicit native",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://anthropic-proxy.example.com"},
			model:    "generic-coder",
			want:     true,
		},
		{
			name:     "anthropic compatible opt in",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://anthropic-proxy.example.com"},
			model:    "generic-coder",
			options:  map[string]any{"anthropicToolSearch": true},
			want:     true,
		},
		{
			name:     "explicitly disabled",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://api.anthropic.com"},
			model:    "claude-sonnet-4-5",
			options:  map[string]any{"anthropicToolSearch": false},
			want:     false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsNativeToolDiscovery(tc.provider, tc.model, tc.options); got != tc.want {
				t.Fatalf("SupportsNativeToolDiscovery = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSupportsNativeToolDiscoveryByDefault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider config.ProviderConfig
		model    string
		options  map[string]any
		want     bool
	}{
		{
			name:     "first party openai responses unsupported model",
			provider: config.ProviderConfig{Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses"},
			model:    "gpt-test",
			want:     false,
		},
		{
			name:     "first party openai responses supported model",
			provider: config.ProviderConfig{Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses"},
			model:    "gpt-5.4",
			want:     true,
		},
		{
			name:     "compatible openai responses",
			provider: config.ProviderConfig{Type: "openai-compatible", BaseURL: "https://compatible.example.com/v1", WireAPI: "responses"},
			model:    "gpt-test",
			want:     false,
		},
		{
			name:     "first party anthropic",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://api.anthropic.com"},
			model:    "claude-sonnet-4-5",
			want:     true,
		},
		{
			name:     "compatible anthropic",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://compatible.example.com/anthropic"},
			model:    "claude-sonnet-4-5",
			want:     false,
		},
		{
			name:     "compatible anthropic explicit option",
			provider: config.ProviderConfig{Type: "anthropic", BaseURL: "https://compatible.example.com/anthropic"},
			model:    "claude-sonnet-4-5",
			options:  map[string]any{"anthropicToolSearch": true},
			want:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsNativeToolDiscoveryByDefault(tc.provider, tc.model, tc.options); got != tc.want {
				t.Fatalf("SupportsNativeToolDiscoveryByDefault = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveProviderProfile_InfersKnownOpenCodeNPMProviders(t *testing.T) {
	for _, tc := range []struct {
		name string
		npm  string
		wire wireProtocol
		auth authMode
	}{
		{name: "openai compatible", npm: "@ai-sdk/openai-compatible", wire: wireOpenAIChat, auth: authAPIKey},
		{name: "openrouter", npm: "@openrouter/ai-sdk-provider", wire: wireOpenAIChat, auth: authAPIKey},
		{name: "anthropic", npm: "@ai-sdk/anthropic", wire: wireAnthropicMessages, auth: authAnthropicToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := resolveProviderProfile(config.ProviderConfig{Type: "opencode-provider-id", NPM: tc.npm})
			if err != nil {
				t.Fatalf("resolveProviderProfile returned error: %v", err)
			}
			if profile.Wire != tc.wire {
				t.Fatalf("Wire = %q, want %q", profile.Wire, tc.wire)
			}
			if profile.Auth != tc.auth {
				t.Fatalf("Auth = %q, want %q", profile.Auth, tc.auth)
			}
		})
	}
}

func TestResolveProviderProfile_RejectsUnsupportedNPMProviders(t *testing.T) {
	_, err := resolveProviderProfile(config.ProviderConfig{Type: "google", NPM: "@ai-sdk/google"})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestResolveProviderProfile_OpenAICodexRejectsChatWire(t *testing.T) {
	_, err := resolveProviderProfile(config.ProviderConfig{Type: "openai-codex", WireAPI: "chat"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveAPIKey_AuthStoreFallback(t *testing.T) {
	// Clear default env var so fallback to auth store is exercised.
	t.Setenv("OPENAI_API_KEY", "")

	home := t.TempDir()
	// Save key to auth store.
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("myapi", authstorage.Credentials{Type: "api_key", APIKey: "sk-from-auth-store"}); err != nil {
		t.Fatalf("save auth key: %v", err)
	}

	provider := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://example.com/v1",
		Model:   "test",
		// No APIKey, no APIKeyEnv set.
	}

	key, err := ResolveAPIKeyWithHome(provider, "myapi", home)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if key != "sk-from-auth-store" {
		t.Fatalf("expected sk-from-auth-store, got %q", key)
	}
}

func TestResolveAPIKey_MiniMaxMigratesStoredAuthTokenFallback(t *testing.T) {
	// The env var outranks the auth store; clear it so a key in the
	// developer's shell cannot satisfy (or leak into) this test.
	t.Setenv("ANTHROPIC_API_KEY", "")

	home := t.TempDir()
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("minimax", authstorage.Credentials{Type: "auth_token", AuthToken: "sk-minimax-from-old-ui"}); err != nil {
		t.Fatalf("save auth token: %v", err)
	}

	provider := config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://api.minimaxi.com/anthropic",
		Model:   "MiniMax-M3",
	}
	key, err := ResolveAPIKeyWithHome(provider, "minimax", home)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if key != "sk-minimax-from-old-ui" {
		t.Fatalf("expected migrated MiniMax key, got %q", key)
	}
}

func TestResolveAuthToken_NonMiniMaxStoredAuthTokenStaysBearer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	store, err := authstorage.ForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("anthropic", authstorage.Credentials{Type: "auth_token", AuthToken: "bearer-token"}); err != nil {
		t.Fatalf("save auth token: %v", err)
	}

	provider := config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://api.anthropic.com",
		Model:   "claude-3-5-sonnet-latest",
	}
	if got := resolveAuthToken(provider, "anthropic"); got != "bearer-token" {
		t.Fatalf("auth token = %q, want bearer-token", got)
	}
}

func TestBuildClient_MissingAPIKey(t *testing.T) {
	_ = os.Unsetenv("MISSING_WUU_KEY")

	_, err := BuildClient(config.ProviderConfig{
		Type:      "openai-compatible",
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "MISSING_WUU_KEY",
		Model:     "gpt-test",
	}, "missing-provider")
	if err == nil {
		t.Fatal("expected error")
	}
}

func writeProviderFactoryCodexAuth(t *testing.T, codexHome, token string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"access_token":  token,
			"refresh_token": "refresh-token",
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
}

func providerFactoryFakeJWT(t *testing.T, exp time.Time, accountID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := map[string]any{
		"exp": exp.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	data, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	return header + "." + payload + ".sig"
}

// TestNativeToolSearchOptionOverridesResponsesWire locks the wire-neutral
// per-model override: config decides native deferred-tool support, not the
// base_url alone, on the OpenAI Responses wire like on the anthropic wire.
func TestNativeToolSearchOptionOverridesResponsesWire(t *testing.T) {
	compatible := config.ProviderConfig{Type: "openai", BaseURL: "https://proxy.example/v1", WireAPI: "responses"}
	if SupportsNativeToolDiscoveryByDefault(compatible, "gpt-5.5", nil) {
		t.Fatal("compatible responses endpoint must default to no native discovery")
	}
	if !SupportsNativeToolDiscoveryByDefault(compatible, "gpt-5.5", map[string]any{"native_tool_search": true}) {
		t.Fatal("native_tool_search=true must enable native discovery on a compatible responses endpoint")
	}
	firstParty := config.ProviderConfig{Type: "openai", BaseURL: "https://api.openai.com/v1", WireAPI: "responses"}
	if SupportsNativeToolDiscoveryByDefault(firstParty, "gpt-5.5", map[string]any{"native_tool_search": false}) {
		t.Fatal("native_tool_search=false must disable native discovery even on first-party OpenAI")
	}
	if SupportsNativeToolDiscovery(firstParty, "gpt-5.5", map[string]any{"native_tool_search": false}) {
		t.Fatal("explicit mode must honor native_tool_search=false")
	}
}

func TestResolveProviderProfileGrokBuild(t *testing.T) {
	profile, err := resolveProviderProfile(config.ProviderConfig{Type: "grok-build", WireAPI: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Wire != wireOpenAIChat || profile.Auth != authGrokBuild {
		t.Fatalf("profile = %+v", profile)
	}
	if _, err := resolveProviderProfile(config.ProviderConfig{Type: "grok-build", WireAPI: "responses"}); err == nil {
		t.Fatal("expected responses wire to be rejected")
	}
}

func TestBuildClientGrokBuildUsesLocalCLICredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	grokHome := filepath.Join(home, ".grok")
	if err := os.MkdirAll(grokHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokHome, "auth.json"), []byte(`{"https://accounts.x.ai/sign-in":{"key":"factory-grok-token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer factory-grok-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
			t.Fatalf("X-XAI-Token-Auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	client, err := BuildClient(config.ProviderConfig{
		Type: "grok-build", BaseURL: server.URL, WireAPI: "chat", Model: "grok-4.5", ReuseGrokCredentials: true,
	}, "grok-build")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "grok-4.5", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil || resp.Content != "ok" {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
}
