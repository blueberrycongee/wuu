package providerfactory

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

// BuildClient constructs a provider client from config using the
// default HTTP retry policy (3 attempts). providerName is the key
// under which this provider lives in the config map; it's needed so
// resolveAPIKey can fall back to the global auth store (where the
// onboarding flow stashes keys via SaveAuthKey).
func BuildClient(provider config.ProviderConfig, providerName string) (providers.Client, error) {
	return BuildClientWithRetry(provider, providerName, nil)
}

// BuildClientWithRetry is like BuildClient but lets the caller pin a
// specific HTTP retry policy. Use this for long-running consumers
// (e.g. sub-agents that may run for many minutes and should be more
// tolerant of transient 429 / 5xx than the interactive main agent).
// Pass nil to use the provider client's built-in default.
func BuildClientWithRetry(provider config.ProviderConfig, providerName string, retry *providers.RetryConfig) (providers.Client, error) {
	typeName := normalizeType(provider.Type)
	apiKey, err := resolveAPIKey(provider, providerName)
	if err != nil {
		return nil, err
	}

	switch typeName {
	case "openai", "openai-compatible", "codex":
		client, newErr := openai.New(openai.ClientConfig{
			BaseURL:     provider.BaseURL,
			APIKey:      apiKey,
			Headers:     provider.Headers,
			RetryConfig: retry,
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	case "anthropic", "claude", "anthropic-official":
		client, newErr := anthropic.New(anthropic.ClientConfig{
			BaseURL:     provider.BaseURL,
			APIKey:      apiKey,
			Headers:     provider.Headers,
			RetryConfig: retry,
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q", provider.Type)
	}
}

// SubAgentRetryConfig returns the more aggressive HTTP retry policy
// recommended for long-running sub-agent runs. Compared to the
// default (3 attempts, 1s→30s backoff) this gives the worker
// substantially more headroom to ride out transient rate limits and
// upstream blips: 6 attempts, 2s→60s backoff. Total worst-case wait
// before giving up is ~2 minutes instead of ~7 seconds.
func SubAgentRetryConfig() providers.RetryConfig {
	return providers.RetryConfig{
		MaxRetries:   6,
		InitialDelay: 2 * time.Second,
		MaxDelay:     60 * time.Second,
	}
}

// BuildStreamClient constructs a streaming-capable provider client.
// providerName is the config map key — see BuildClient for why this
// matters for the global auth-store fallback.
func BuildStreamClient(provider config.ProviderConfig, providerName string) (providers.StreamClient, error) {
	typeName := normalizeType(provider.Type)
	apiKey, err := resolveAPIKey(provider, providerName)
	if err != nil {
		return nil, err
	}

	switch typeName {
	case "openai", "openai-compatible", "codex":
		return openai.New(openai.ClientConfig{
			BaseURL: provider.BaseURL,
			APIKey:  apiKey,
			Headers: provider.Headers,
		})
	case "anthropic", "claude", "anthropic-official":
		return anthropic.New(anthropic.ClientConfig{
			BaseURL: provider.BaseURL,
			APIKey:  apiKey,
			Headers: provider.Headers,
		})
	default:
		return nil, fmt.Errorf("unsupported provider type %q", provider.Type)
	}
}

func normalizeType(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

// ResolveAPIKeyWithHome resolves API key with explicit home directory.
func ResolveAPIKeyWithHome(provider config.ProviderConfig, providerName, home string) (string, error) {
	// 1. Explicit api_key in config.
	if strings.TrimSpace(provider.APIKey) != "" {
		return strings.TrimSpace(provider.APIKey), nil
	}

	// 2. Environment variable.
	envKey := strings.TrimSpace(provider.APIKeyEnv)
	if envKey == "" {
		envKey = defaultAPIKeyEnv(normalizeType(provider.Type))
	}
	if envKey != "" {
		value := strings.TrimSpace(os.Getenv(envKey))
		if value != "" {
			return value, nil
		}
	}

	// 3. Global auth store.
	if home != "" && providerName != "" {
		key, err := config.LoadAuthKey(home, providerName)
		if err == nil && key != "" {
			return key, nil
		}
	}

	hint := "set api_key or run wuu init"
	if envKey != "" {
		hint = fmt.Sprintf("set api_key, %s env var, or run wuu init", envKey)
	}
	return "", fmt.Errorf("no API key found for provider %q (%s)", provider.Type, hint)
}

func resolveAPIKey(provider config.ProviderConfig, providerName string) (string, error) {
	return ResolveAPIKeyWithHome(provider, providerName, os.Getenv("HOME"))
}

func defaultAPIKeyEnv(providerType string) string {
	switch providerType {
	case "openai", "openai-compatible", "codex":
		return "OPENAI_API_KEY"
	case "anthropic", "claude", "anthropic-official":
		return "ANTHROPIC_API_KEY"
	default:
		return ""
	}
}
