package providerfactory

import (
	"fmt"
	"os"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
)

// BuildClient constructs a provider client from config.
func BuildClient(provider config.ProviderConfig) (providers.Client, error) {
	typeName := normalizeType(provider.Type)
	apiKey, err := resolveAPIKey(provider)
	if err != nil {
		return nil, err
	}

	switch typeName {
	case "openai", "openai-compatible", "codex":
		client, newErr := openai.New(openai.ClientConfig{
			BaseURL: provider.BaseURL,
			APIKey:  apiKey,
			Headers: provider.Headers,
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	case "anthropic", "claude", "anthropic-official":
		client, newErr := anthropic.New(anthropic.ClientConfig{
			BaseURL: provider.BaseURL,
			APIKey:  apiKey,
			Headers: provider.Headers,
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q", provider.Type)
	}
}

// BuildStreamClient constructs a streaming-capable provider client.
func BuildStreamClient(provider config.ProviderConfig) (providers.StreamClient, error) {
	typeName := normalizeType(provider.Type)
	apiKey, err := resolveAPIKey(provider)
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

func resolveAPIKey(provider config.ProviderConfig) (string, error) {
	if strings.TrimSpace(provider.APIKey) != "" {
		return strings.TrimSpace(provider.APIKey), nil
	}

	envKey := strings.TrimSpace(provider.APIKeyEnv)
	if envKey == "" {
		envKey = defaultAPIKeyEnv(normalizeType(provider.Type))
	}
	if envKey == "" {
		return "", fmt.Errorf("provider %q requires api_key or api_key_env", provider.Type)
	}

	value := strings.TrimSpace(os.Getenv(envKey))
	if value == "" {
		return "", fmt.Errorf("environment variable %s is empty", envKey)
	}
	return value, nil
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
