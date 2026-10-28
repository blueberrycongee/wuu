package providerfactory

import (
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
)

type wireProtocol string

const (
	wireOpenAIChat        wireProtocol = "openai_chat"
	wireOpenAIResponses   wireProtocol = "openai_responses"
	wireAnthropicMessages wireProtocol = "anthropic_messages"
)

type authMode string

const (
	authAPIKey          authMode = "api_key"
	authAnthropicToken  authMode = "anthropic_token"
	authCodexOAuth      authMode = "codex_oauth"
	authChatGPT         authMode = "chatgpt"
	authXAISubscription authMode = "xai_subscription"
	authGrokBuild       authMode = "grok_build"
)

type providerProfile struct {
	TypeName string
	Wire     wireProtocol
	Auth     authMode
}

func resolveProviderProfile(provider config.ProviderConfig) (providerProfile, error) {
	typeName := normalizeType(provider.Type)
	switch typeName {
	case "openai", "openai-compatible", "codex":
		wire, err := resolveOpenAIWire(provider.WireAPI)
		if err != nil {
			return providerProfile{}, err
		}
		return providerProfile{
			TypeName: typeName,
			Wire:     wire,
			Auth:     authAPIKey,
		}, nil
	case "chatgpt":
		if wire := strings.ToLower(strings.TrimSpace(provider.WireAPI)); wire != "" && wire != "responses" {
			return providerProfile{}, fmt.Errorf("provider type %q supports only responses wire_api", provider.Type)
		}
		return providerProfile{TypeName: typeName, Wire: wireOpenAIResponses, Auth: authChatGPT}, nil
	case "openai-codex", "codex-subscription", "chatgpt-codex":
		if wire := strings.ToLower(strings.TrimSpace(provider.WireAPI)); wire != "" && wire != "responses" {
			return providerProfile{}, fmt.Errorf("provider type %q supports only responses wire_api", provider.Type)
		}
		return providerProfile{
			TypeName: typeName,
			Wire:     wireOpenAIResponses,
			Auth:     authCodexOAuth,
		}, nil
	case "xai-subscription", "xai-oauth", "grok-subscription", "supergrok":
		if wire := strings.ToLower(strings.TrimSpace(provider.WireAPI)); wire != "" && wire != "responses" {
			return providerProfile{}, fmt.Errorf("provider type %q supports only responses wire_api", provider.Type)
		}
		return providerProfile{
			TypeName: typeName,
			Wire:     wireOpenAIResponses,
			Auth:     authXAISubscription,
		}, nil
	case "grok-build", "xai-grok-build", "grok-cli":
		if wire := strings.ToLower(strings.TrimSpace(provider.WireAPI)); wire != "" && wire != "chat" {
			return providerProfile{}, fmt.Errorf("provider type %q supports only chat wire_api", provider.Type)
		}
		return providerProfile{
			TypeName: typeName,
			Wire:     wireOpenAIChat,
			Auth:     authGrokBuild,
		}, nil
	case "anthropic", "claude", "anthropic-official":
		return providerProfile{
			TypeName: typeName,
			Wire:     wireAnthropicMessages,
			Auth:     authAnthropicToken,
		}, nil
	default:
		if inferred := nativeProviderTypeForNPM(provider.NPM); inferred != "" {
			next := provider
			next.Type = inferred
			return resolveProviderProfile(next)
		}
		return providerProfile{}, fmt.Errorf("unsupported provider type %q", provider.Type)
	}
}

func nativeProviderTypeForNPM(npm string) string {
	switch normalizeNPM(npm) {
	case "@ai-sdk/anthropic":
		return "anthropic"
	case "@ai-sdk/openai":
		return "openai"
	case "@ai-sdk/openai-compatible", "@openrouter/ai-sdk-provider", "@ai-sdk/xai":
		return "openai-compatible"
	default:
		return ""
	}
}

func normalizeNPM(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "npm:")
	return value
}

func resolveOpenAIWire(value string) (wireProtocol, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "chat":
		return wireOpenAIChat, nil
	case "responses":
		return wireOpenAIResponses, nil
	default:
		return "", fmt.Errorf("unsupported OpenAI wire API %q", value)
	}
}

func openAIWireConfig(wire wireProtocol) string {
	switch wire {
	case wireOpenAIResponses:
		return "responses"
	default:
		return "chat"
	}
}
