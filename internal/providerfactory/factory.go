package providerfactory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/chatgpt"
	"github.com/blueberrycongee/wuu/internal/providers/codex"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
	"github.com/blueberrycongee/wuu/internal/siwc"
)

// Native transport clients record every physical submission through their
// bound attempt. Runtime admission is selected at the common model receipt
// boundary, shared with external adapters; there is no process-global policy.

// Bindings captures services owned by the calling generation. The caller keeps
// them alive for every client and login operation and owns their cleanup.
type Bindings struct {
	// Context is used only during construction; refresh receives its current request context.
	Context     context.Context
	Credentials credentialstore.Store
	ChatGPT     *siwc.Manager
}

// BuildClient constructs a single-submission protocol client. Without bindings,
// only explicit config and environment credentials are available.
func BuildClient(provider config.ProviderConfig, providerName string, bindings ...Bindings) (providers.Client, error) {
	return buildClient(provider, providerName, bindings...)
}

// BuildStreamClient constructs a streaming-capable provider client using the
// provider default retry policy and the same captured services as BuildClient.
func BuildStreamClient(provider config.ProviderConfig, providerName string, bindings ...Bindings) (providers.StreamClient, error) {
	client, err := buildClient(provider, providerName, bindings...)
	if err != nil {
		return nil, err
	}
	return providers.AdaptStreamClient(client), nil
}

func BuildRuntimeStreamClient(provider config.ProviderConfig, providerName string, bindings ...Bindings) (providers.StreamClient, error) {
	client, err := BuildStreamClient(provider, providerName, bindings...)
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
	if errors.Is(err, authstorage.ErrUnavailable) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no chatgpt account credentials") ||
		strings.Contains(message, "no api key found") ||
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
	if err != nil || profile.Auth == authChatGPT {
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
	if err != nil || profile.Auth == authChatGPT {
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

func buildClient(provider config.ProviderConfig, providerName string, bindings ...Bindings) (providers.Client, error) {
	if len(bindings) > 1 {
		return nil, errors.New("one provider service binding is required")
	}
	var bound Bindings
	var store *authstorage.Store
	if len(bindings) == 1 {
		bound = bindings[0]
		buildContext := bound.Context
		if buildContext == nil {
			buildContext = context.Background()
		}
		store = authstorage.NewBound(buildContext, bound.Credentials)
	}
	profile, err := resolveProviderProfile(provider)
	if err != nil {
		return nil, err
	}

	switch profile.Wire {
	case wireOpenAIChat, wireOpenAIResponses:
		if profile.Auth == authChatGPT {
			if endpoint := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/"); endpoint != "" && endpoint != chatgpt.DefaultBaseURL {
				return nil, fmt.Errorf("ChatGPT requires the public %s endpoint", chatgpt.DefaultBaseURL)
			}
			if mode := strings.ToLower(strings.TrimSpace(provider.StreamTransport)); mode != "" && mode != "sse" {
				return nil, fmt.Errorf("ChatGPT supports only sse stream_transport")
			}
			if strings.TrimSpace(provider.ChatGPTAccountID) == "" {
				return nil, fmt.Errorf("no ChatGPT account credentials; sign in with ChatGPT")
			}
			if bound.ChatGPT == nil {
				return nil, fmt.Errorf("no ChatGPT account credentials: selected credential manager is unavailable")
			}
			manager := bound.ChatGPT
			account, err := manager.Account(constructionContext(bound.Context), provider.ChatGPTAccountID)
			if err != nil {
				return nil, fmt.Errorf("no ChatGPT account credentials: %w", err)
			}
			if !account.Connected || !account.PlanEnabled {
				return nil, fmt.Errorf("no ChatGPT account credentials with plan access; sign in with ChatGPT")
			}
			return chatgpt.New(chatgpt.ClientConfig{
				AccountID:    provider.ChatGPTAccountID,
				AccessToken:  manager.AccessToken,
				BindRequest:  manager.BindRequest,
				StreamConfig: providerStreamTransportConfig(provider),
			})
		}
		if profile.Auth == authGrokBuild {
			client, newErr := grokbuild.New(grokbuild.ClientConfig{
				Credentials:          bound.Credentials,
				BaseURL:              provider.BaseURL,
				APIKey:               resolveExplicitAPIKey(provider),
				Headers:              provider.Headers,
				StreamConfig:         providerStreamTransportConfig(provider),
				ReuseGrokCredentials: provider.ReuseGrokCredentials,
			})
			if newErr != nil {
				return nil, newErr
			}
			return client, nil
		}
		if profile.Auth == authCodexOAuth {
			client, newErr := codex.New(codex.ClientConfig{
				Credentials:           bound.Credentials,
				BaseURL:               provider.BaseURL,
				APIKey:                resolveExplicitAPIKey(provider),
				Headers:               provider.Headers,
				StreamConfig:          providerStreamTransportConfig(provider),
				StreamTransport:       providerStreamTransportMode(provider),
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
				Credentials:     bound.Credentials,
				BaseURL:         provider.BaseURL,
				Headers:         provider.Headers,
				StreamConfig:    providerStreamTransportConfig(provider),
				StreamTransport: providerStreamTransportMode(provider),
			})
			if newErr != nil {
				return nil, newErr
			}
			return client, nil
		}
		apiKey, apiKeyErr := resolveAPIKey(provider, providerName, store)
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
		})
		if newErr != nil {
			return nil, newErr
		}
		return client, nil
	case wireAnthropicMessages:
		apiKey, authToken, apiKeyErr := resolveAnthropicCredentials(provider, providerName, store)
		if apiKeyErr != nil {
			return nil, apiKeyErr
		}
		client, newErr := anthropic.New(anthropic.ClientConfig{
			BaseURL:                         provider.BaseURL,
			APIKey:                          apiKey,
			AuthToken:                       authToken,
			Headers:                         provider.Headers,
			MaxTokens:                       providerModelOutputLimit(provider),
			StreamConfig:                    providerStreamTransportConfig(provider),
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

// ResolveAPIKey resolves credentials from the supplied service. A service error
// is never replaced with an ambient environment or local-file credential.
func ResolveAPIKey(provider config.ProviderConfig, providerName string, selected credentialstore.Store) (string, error) {
	return resolveAPIKey(provider, providerName, authstorage.NewBound(context.Background(), selected))
}

// ResolveAPIKeyWithHome is an explicit legacy-file adapter for migration tools.
func ResolveAPIKeyWithHome(provider config.ProviderConfig, providerName, home string) (string, error) {
	store, err := authstorage.ForHome(home)
	if err != nil {
		return "", err
	}
	return resolveAPIKey(provider, providerName, store)
}

func loadStoredCredentials(store *authstorage.Store, providerName string) (authstorage.Credentials, error) {
	if store == nil || providerName == "" {
		return authstorage.Credentials{}, nil
	}
	credentials, err := store.Get(providerName)
	if errors.Is(err, authstorage.ErrNotFound) {
		return authstorage.Credentials{}, nil
	}
	return credentials, err
}

func resolveAPIKey(provider config.ProviderConfig, providerName string, store *authstorage.Store) (string, error) {
	if explicit := resolveExplicitAPIKey(provider); explicit != "" {
		return explicit, nil
	}
	stored, err := loadStoredCredentials(store, providerName)
	if err != nil {
		return "", err
	}
	return resolveStoredAPIKey(provider, providerName, stored)
}

func resolveStoredAPIKey(provider config.ProviderConfig, providerName string, stored authstorage.Credentials) (string, error) {
	storedKey := storedAPIKey(stored, provider, providerName)
	if storedKey != "" && stored.Source == authstorage.SourceSaved {
		return storedKey, nil
	}
	envKey := strings.TrimSpace(provider.APIKeyEnv)
	if envKey == "" {
		envKey = defaultAPIKeyEnv(normalizeType(provider.Type))
		if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
			return value, nil
		}
	}
	if storedKey != "" {
		return storedKey, nil
	}
	hint := "set api_key or run wuu init"
	if envKey != "" {
		hint = fmt.Sprintf("set api_key, %s env var, or run wuu init", envKey)
	}
	return "", fmt.Errorf("no API key found for provider %q (%s)", provider.Type, hint)
}

func storedAPIKey(credentials authstorage.Credentials, provider config.ProviderConfig, providerName string) string {
	if key := strings.TrimSpace(credentials.APIKey); key != "" {
		return key
	}
	if shouldTreatStoredAuthTokenAsAPIKey(provider, providerName) {
		return strings.TrimSpace(credentials.AuthToken)
	}
	return ""
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

// Resolve the connection's credentials as a pair. Independently falling back
// for each header can send an unrelated ambient credential to a custom endpoint.
func resolveAnthropicCredentials(provider config.ProviderConfig, providerName string, store *authstorage.Store) (string, string, error) {
	apiKey := resolveExplicitAPIKey(provider)
	authToken := strings.TrimSpace(provider.AuthToken)
	if authToken == "" {
		authToken = strings.TrimSpace(os.Getenv(strings.TrimSpace(provider.AuthTokenEnv)))
	}
	// Explicit dual-header configurations remain supported, but neither header
	// may acquire another credential source when one was explicitly selected.
	if apiKey != "" || authToken != "" {
		return apiKey, authToken, nil
	}
	stored, err := loadStoredCredentials(store, providerName)
	if err != nil {
		return "", "", err
	}
	if stored.Source == authstorage.SourceSaved {
		if key := storedAPIKey(stored, provider, providerName); key != "" {
			return key, "", nil
		}
		if token := strings.TrimSpace(stored.AuthToken); token != "" {
			return "", token, nil
		}
	}
	// Preserve the environment/default behavior for connections without a
	// selected credential, including older imported auth-store entries.
	apiKey, err = resolveStoredAPIKey(provider, providerName, stored)
	authToken = resolveAuthToken(provider, providerName, stored)
	if authToken != "" {
		err = nil
	}
	return apiKey, authToken, err
}

// resolveAuthToken resolves a Bearer auth token from config or environment.
// This mirrors the Anthropic SDK's ANTHROPIC_AUTH_TOKEN support.
func resolveAuthToken(provider config.ProviderConfig, providerName string, credentials authstorage.Credentials) string {
	if t := strings.TrimSpace(provider.AuthToken); t != "" {
		return t
	}
	// A stored token is a bearer token unless the provider treats it as its key.
	storedToken := ""
	saved := false
	if providerName != "" && !shouldTreatStoredAuthTokenAsAPIKey(provider, providerName) {
		storedToken = strings.TrimSpace(credentials.AuthToken)
		saved = credentials.Source == authstorage.SourceSaved
	}
	envKey := strings.TrimSpace(provider.AuthTokenEnv)
	if envKey != "" {
		if token := strings.TrimSpace(os.Getenv(envKey)); token != "" {
			return token
		}
	}
	// A saved token was chosen for this provider; the implicit default env var
	// is ambient and must not replace it.
	if saved && storedToken != "" {
		return storedToken
	}
	if envKey == "" {
		if token := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); token != "" {
			return token
		}
	}
	return storedToken
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

func constructionContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// NativeCompactionSupported reports configured protocol support without opening
// credentials, connections or submitting a request.
func NativeCompactionSupported(provider config.ProviderConfig) bool {
	profile, err := resolveProviderProfile(provider)
	return err == nil && profile.Auth == authCodexOAuth && (provider.NativeCompaction == nil || *provider.NativeCompaction)
}
