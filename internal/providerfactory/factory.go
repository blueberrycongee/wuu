package providerfactory

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
)

// sharedProviderCoordinator coordinates every inference client built by this
// process, regardless of whether the caller is a foreground turn, sub-agent,
// title generator, or compaction job.
var sharedProviderCoordinator = providers.NewProviderCoordinator(providers.DefaultProviderCoordinatorConfig())

// BuildClient constructs a single-submission protocol client. providerName is
// the key under which this provider lives
// in the config map; it's needed so resolveAPIKey can fall back to the global
// auth store.
func BuildClient(provider config.ProviderConfig, providerName string) (providers.Client, error) {
	return buildClient(provider, providerName)
}

// BuildStreamClient constructs a streaming-capable provider client using the
// provider default retry policy. providerName is the config map key; see
// BuildClient for why this matters for the global auth-store fallback.
func BuildStreamClient(provider config.ProviderConfig, providerName string) (providers.StreamClient, error) {
	client, err := buildClient(provider, providerName)
	if err != nil {
		return nil, err
	}
	return providers.AdaptStreamClient(client), nil
}

func BuildRuntimeStreamClient(provider config.ProviderConfig, providerName string) (providers.StreamClient, error) {
	client, err := BuildStreamClient(provider, providerName)
	if err == nil {
		return client, nil
	}
	if IsCredentialError(err) {
		return &providers.UnavailableClient{Reason: err.Error()}, err
	}
	return nil, err
}

func IsCredentialError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no api key found") ||
		strings.Contains(message, "no codex oauth credentials") ||
		strings.Contains(message, "no wuu codex oauth credentials") ||
		strings.Contains(message, "no grok build credential") ||
		strings.Contains(message, "grok cli credentials") ||
		strings.Contains(message, "no wuu xai supergrok oauth credentials") ||
		strings.Contains(message, "no xai supergrok oauth credentials")
}

// SupportsNativeToolDiscoveryByDefault reports whether auto mode should use
// provider-native deferred tool loading. Only first-party provider paths are
// enabled by default; compatible endpoints must opt in explicitly.
func SupportsNativeToolDiscoveryByDefault(provider config.ProviderConfig, model string, providerOptions map[string]any) bool {
	profile, err := resolveProviderProfile(provider)
	if err != nil {
		return false
	}
	switch profile.Wire {
	case wireOpenAIChat:
		if !openai.SupportsDynamicToolLoading(model) {
			return false
		}
		if enabled, ok := nativeToolSearchOption(providerOptions); ok {
			return enabled
		}
		endpoint := strings.TrimRight(strings.ToLower(strings.TrimSpace(provider.BaseURL)), "/")
		return endpoint == "https://api.moonshot.ai/v1" || endpoint == "https://api.moonshot.cn/v1"
	case wireOpenAIResponses:
		// Per-model config wins over the first-party base_url default, the
		// same override contract the anthropic wire honors: vendor protocol
		// support is channel data, not something to hardcode by URL.
		if enabled, ok := nativeToolSearchOption(providerOptions); ok {
			return enabled && openAIModelSupportsNativeToolSearch(model)
		}
		return openAIModelSupportsNativeToolSearch(model) &&
			(isFirstPartyOpenAIResponsesBaseURL(provider.BaseURL) || profile.Auth == authCodexOAuth)
	case wireAnthropicMessages:
		return anthropic.SupportsNativeToolSearchByDefault(provider.BaseURL, model, providerOptions)
	default:
		return false
	}
}

// SupportsNativeToolDiscovery reports whether this provider path can receive
// provider-native deferred tool declarations when the user explicitly opts in.
func SupportsNativeToolDiscovery(provider config.ProviderConfig, model string, providerOptions map[string]any) bool {
	profile, err := resolveProviderProfile(provider)
	if err != nil {
		return false
	}
	switch profile.Wire {
	case wireOpenAIChat:
		if enabled, ok := nativeToolSearchOption(providerOptions); ok && !enabled {
			return false
		}
		return openai.SupportsDynamicToolLoading(model)
	case wireOpenAIResponses:
		if enabled, ok := nativeToolSearchOption(providerOptions); ok && !enabled {
			return false
		}
		return openAIModelSupportsNativeToolSearch(model)
	case wireAnthropicMessages:
		return anthropic.SupportsNativeToolSearchWhenExplicitlyEnabled(model, providerOptions)
	default:
		return false
	}
}

// nativeToolSearchOption reads the wire-neutral per-model override for
// provider-native deferred tool loading from model options
// (providers.<name>.models.<model>.options.native_tool_search).
func nativeToolSearchOption(options map[string]any) (bool, bool) {
	if len(options) == 0 {
		return false, false
	}
	for _, key := range []string{"native_tool_search", "nativeToolSearch"} {
		value, exists := options[key]
		if !exists {
			continue
		}
		switch v := value.(type) {
		case bool:
			return v, true
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "1", "yes", "on":
				return true, true
			case "false", "0", "no", "off":
				return false, true
			}
		}
	}
	return false, false
}

func openAIModelSupportsNativeToolSearch(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(model, "gpt-") {
		return false
	}
	// Nano shares the 5.4 version but does not implement tool search.
	// https://developers.openai.com/api/docs/models/gpt-5.4-nano
	if model == "gpt-5.4-nano" || strings.HasPrefix(model, "gpt-5.4-nano-") {
		return false
	}
	version := strings.TrimPrefix(model, "gpt-")
	version = strings.TrimLeft(version, "-_")
	versionToken := ""
	for _, r := range version {
		if (r >= '0' && r <= '9') || r == '.' {
			versionToken += string(r)
			continue
		}
		break
	}
	if versionToken == "" {
		return false
	}
	majorText, minorText, hasMinor := strings.Cut(versionToken, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return false
	}
	minor := 0
	if hasMinor {
		minorValue, err := strconv.Atoi(minorText)
		if err != nil {
			return false
		}
		minor = minorValue
	}
	return major > 5 || (major == 5 && minor >= 4)
}

func isFirstPartyOpenAIResponsesBaseURL(raw string) bool {
	u := strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
	return u == "https://api.openai.com" ||
		u == "https://api.openai.com/v1" ||
		u == "https://chatgpt.com/backend-api/codex"
}

func buildClient(provider config.ProviderConfig, providerName string) (providers.Client, error) {
	profile, err := resolveProviderProfile(provider)
	if err != nil {
		return nil, err
	}

	// Resolve auth token for anthropic providers (Bearer auth, aligned with
	// the Anthropic SDK's ANTHROPIC_AUTH_TOKEN support).
	authToken := resolveAuthToken(provider, providerName)

	switch profile.Wire {
	case wireOpenAIChat, wireOpenAIResponses:
		if profile.Auth == authGrokBuild {
			client, newErr := grokbuild.New(grokbuild.ClientConfig{
				BaseURL:              provider.BaseURL,
				APIKey:               resolveExplicitAPIKey(provider),
				Headers:              provider.Headers,
				StreamConfig:         providerStreamTransportConfig(provider),
				Coordinator:          sharedProviderCoordinator,
				ReuseGrokCredentials: provider.ReuseGrokCredentials,
			})
			if newErr != nil {
				return nil, newErr
			}
			return client, nil
		}
		if profile.Auth == authCodexOAuth {
			client, newErr := codex.New(codex.ClientConfig{
				BaseURL:               provider.BaseURL,
				APIKey:                resolveExplicitAPIKey(provider),
				Headers:               provider.Headers,
				StreamConfig:          providerStreamTransportConfig(provider),
				StreamTransport:       providerStreamTransportMode(provider),
				Coordinator:           sharedProviderCoordinator,
				ReuseCodexCredentials: provider.ReuseCodexCredentials,
				NativeCompaction:      provider.NativeCompaction,
			})
			if newErr != nil {
				return nil, newErr
			}
			return client, nil
		}
		if profile.Auth == authXAISubscription {
			client, newErr := xaisub.New(xaisub.ClientConfig{
				BaseURL:         provider.BaseURL,
				Headers:         provider.Headers,
				StreamConfig:    providerStreamTransportConfig(provider),
				StreamTransport: providerStreamTransportMode(provider),
				Coordinator:     sharedProviderCoordinator,
			})
			if newErr != nil {
				return nil, newErr
			}
			return client, nil
		}
		apiKey, apiKeyErr := resolveAPIKey(provider, providerName)
		if apiKeyErr != nil {
			return nil, apiKeyErr
		}
		client, newErr := openai.New(openai.ClientConfig{
			BaseURL:            provider.BaseURL,
			WireAPI:            openAIWireConfig(profile.Wire),
			APIKey:             apiKey,
			Headers:            provider.Headers,
			StreamConfig:       providerStreamTransportConfig(provider),
			ResponsesTransport: providerStreamTransportMode(provider),
			Coordinator:        sharedProviderCoordinator,
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	case wireAnthropicMessages:
		// API key is optional when auth token is available.
		apiKey, apiKeyErr := resolveAPIKey(provider, providerName)
		if apiKeyErr != nil && authToken == "" {
			return nil, apiKeyErr
		}
		client, newErr := anthropic.New(anthropic.ClientConfig{
			BaseURL:                         provider.BaseURL,
			APIKey:                          apiKey,
			AuthToken:                       authToken,
			Headers:                         provider.Headers,
			MaxTokens:                       providerModelOutputLimit(provider),
			StreamConfig:                    providerStreamTransportConfig(provider),
			Coordinator:                     sharedProviderCoordinator,
			CacheCreationInputTokensOmitted: provider.CacheCreationInputTokensOmitted,
			InputTokensIncludeCacheRead:     provider.InputTokensIncludeCacheRead,
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported provider wire protocol %q", profile.Wire)
	}
}

func providerModelOutputLimit(provider config.ProviderConfig) int {
	model := strings.TrimSpace(provider.Model)
	if model == "" {
		return 0
	}
	if cfg, ok := provider.Models[model]; ok && cfg.Limit != nil {
		return cfg.Limit.Output
	}
	for _, cfg := range provider.Models {
		if strings.TrimSpace(cfg.ID) == model && cfg.Limit != nil {
			return cfg.Limit.Output
		}
	}
	return 0
}

func providerStreamTransportConfig(provider config.ProviderConfig) *providers.StreamTransportConfig {
	cfg := providers.StreamTransportConfig{}
	if provider.StreamConnectTimeoutMS > 0 {
		cfg.ConnectTimeout = time.Duration(provider.StreamConnectTimeoutMS) * time.Millisecond
	}
	if provider.StreamHeaderTimeoutMS > 0 {
		cfg.HeaderTimeout = time.Duration(provider.StreamHeaderTimeoutMS) * time.Millisecond
	}
	if provider.StreamIdleTimeoutMS > 0 {
		cfg.IdleTimeout = time.Duration(provider.StreamIdleTimeoutMS) * time.Millisecond
	}
	if cfg == (providers.StreamTransportConfig{}) {
		return nil
	}
	return &cfg
}

func providerStreamTransportMode(provider config.ProviderConfig) providers.StreamTransportMode {
	mode, _ := providers.NormalizeStreamTransportMode(provider.StreamTransport)
	return mode
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
		if store, err := authstorage.ForHome(home); err == nil {
			if credentials, err := store.Get(providerName); err == nil && strings.TrimSpace(credentials.APIKey) != "" {
				return strings.TrimSpace(credentials.APIKey), nil
			} else if err == nil && shouldTreatStoredAuthTokenAsAPIKey(provider, providerName) && strings.TrimSpace(credentials.AuthToken) != "" {
				return strings.TrimSpace(credentials.AuthToken), nil
			}
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

func resolveExplicitAPIKey(provider config.ProviderConfig) string {
	if strings.TrimSpace(provider.APIKey) != "" {
		return strings.TrimSpace(provider.APIKey)
	}
	if envKey := strings.TrimSpace(provider.APIKeyEnv); envKey != "" {
		return strings.TrimSpace(os.Getenv(envKey))
	}
	return ""
}

// resolveAuthToken resolves a Bearer auth token from config or environment.
// This mirrors the Anthropic SDK's ANTHROPIC_AUTH_TOKEN support.
func resolveAuthToken(provider config.ProviderConfig, providerName string) string {
	if t := strings.TrimSpace(provider.AuthToken); t != "" {
		return t
	}
	envKey := strings.TrimSpace(provider.AuthTokenEnv)
	if envKey == "" {
		envKey = "ANTHROPIC_AUTH_TOKEN"
	}
	if token := strings.TrimSpace(os.Getenv(envKey)); token != "" {
		return token
	}
	if providerName != "" {
		if store, err := authstorage.ForHome(os.Getenv("HOME")); err == nil {
			if credentials, err := store.Get(providerName); err == nil && strings.TrimSpace(credentials.AuthToken) != "" {
				if shouldTreatStoredAuthTokenAsAPIKey(provider, providerName) {
					return ""
				}
				return strings.TrimSpace(credentials.AuthToken)
			}
		}
	}
	return ""
}

func shouldTreatStoredAuthTokenAsAPIKey(provider config.ProviderConfig, providerName string) bool {
	name := strings.ToLower(strings.TrimSpace(providerName))
	baseURL := strings.ToLower(strings.TrimSpace(provider.BaseURL))
	return strings.Contains(name, "minimax") ||
		strings.Contains(baseURL, "minimax.io") ||
		strings.Contains(baseURL, "minimaxi.com")
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
