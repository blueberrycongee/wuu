package providerfactory

import (
	"github.com/blueberrycongee/wuu/internal/config"
	"strings"
	"testing"
)

func TestChatGPTDoesNotReuseAPIOrCodexCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-use")
	_, err := BuildClient(config.ProviderConfig{Type: "chatgpt", APIKey: "must-not-use", ReuseCodexCredentials: true}, "chatgpt")
	if err == nil || !IsCredentialError(err) {
		t.Fatalf("missing account error=%v", err)
	}
}
func TestChatGPTRejectsUnsupportedEndpointAndTransport(t *testing.T) {
	for _, provider := range []config.ProviderConfig{
		{Type: "chatgpt", ChatGPTAccountID: "account", BaseURL: "https://chatgpt.com/backend-api/codex"},
		{Type: "chatgpt", ChatGPTAccountID: "account", StreamTransport: "websocket"},
		{Type: "chatgpt", ChatGPTAccountID: "account", WireAPI: "chat"},
	} {
		if _, err := BuildClient(provider, "chatgpt"); err == nil || strings.Contains(err.Error(), "keyring") {
			t.Errorf("invalid ChatGPT configuration not rejected before auth: %v", err)
		}
	}
}
func TestChatGPTNeverEnablesNativeToolSearch(t *testing.T) {
	provider := config.ProviderConfig{Type: "chatgpt", BaseURL: "https://api.openai.com/v1"}
	for _, options := range []map[string]any{nil, {"native_tool_search": true}} {
		if SupportsNativeToolDiscovery(provider, "gpt-6.1-sol", options) || SupportsNativeToolDiscoveryByDefault(provider, "gpt-6.1-sol", options) {
			t.Fatal("ChatGPT enables unsupported Responses tool_search")
		}
	}
}
