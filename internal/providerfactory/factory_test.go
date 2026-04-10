package providerfactory

import (
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestBuildClient_OpenAICompatible(t *testing.T) {
	t.Setenv("TEST_WUU_KEY", "abc")

	client, err := BuildClient(config.ProviderConfig{
		Type:      "openai-compatible",
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "TEST_WUU_KEY",
		Model:     "gpt-test",
	}, "test")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
}

func TestBuildClient_Anthropic(t *testing.T) {
	t.Setenv("TEST_ANTHROPIC_KEY", "abc")

	client, err := BuildClient(config.ProviderConfig{
		Type:      "anthropic",
		BaseURL:   "https://api.anthropic.com",
		APIKeyEnv: "TEST_ANTHROPIC_KEY",
		Model:     "claude-test",
	}, "test")
	if err != nil {
		t.Fatalf("BuildClient returned error: %v", err)
	}
	if client == nil {
		t.Fatal("expected client")
	}
}

func TestResolveAPIKey_AuthStoreFallback(t *testing.T) {
	// Clear default env var so fallback to auth store is exercised.
	t.Setenv("OPENAI_API_KEY", "")

	home := t.TempDir()
	// Save key to auth store.
	if err := config.SaveAuthKey(home, "myapi", "sk-from-auth-store"); err != nil {
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

func TestBuildClientWithRetry_AppliesCustomConfig(t *testing.T) {
	t.Setenv("TEST_WUU_KEY", "abc")

	rc := SubAgentRetryConfig()
	if rc.MaxRetries != 6 {
		t.Fatalf("SubAgentRetryConfig MaxRetries = %d, want 6", rc.MaxRetries)
	}
	if rc.MaxDelay < rc.InitialDelay {
		t.Fatalf("SubAgentRetryConfig MaxDelay (%v) < InitialDelay (%v)", rc.MaxDelay, rc.InitialDelay)
	}

	// We can't introspect the underlying client's RetryConfig from the
	// public providers.Client interface, but we can at least verify
	// the constructor accepts the override and returns successfully.
	// Smoke test for both supported provider families.
	for _, ptype := range []string{"openai-compatible", "anthropic"} {
		client, err := BuildClientWithRetry(config.ProviderConfig{
			Type:      ptype,
			BaseURL:   "https://example.com/v1",
			APIKeyEnv: "TEST_WUU_KEY",
			Model:     "test",
		}, "test", &rc)
		if err != nil {
			t.Fatalf("BuildClientWithRetry(%s) returned error: %v", ptype, err)
		}
		if client == nil {
			t.Fatalf("BuildClientWithRetry(%s) returned nil client", ptype)
		}
	}
}

func TestBuildClient_MissingAPIKey(t *testing.T) {
	_ = os.Unsetenv("MISSING_WUU_KEY")

	_, err := BuildClient(config.ProviderConfig{
		Type:      "openai-compatible",
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "MISSING_WUU_KEY",
		Model:     "gpt-test",
	}, "test")
	if err == nil {
		t.Fatal("expected error")
	}
}
