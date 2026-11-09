// Package testsupport configures disposable native-client integration fixtures.
package testsupport

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/blueberrycongee/wuu/internal/config"
)

// WriteConfig exposes the legacy local catalog without an inference endpoint.
func WriteConfig(path, provider string) error {
	return writeConfig(path, provider, "http://127.0.0.1:1/v1", "")
}

// WriteLoopbackConfig binds the production selected model adapter to a disposable
// HTTP fixture. A synthetic key prevents ambient credentials from participating.
func WriteLoopbackConfig(path, provider, endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || !net.ParseIP(parsed.Hostname()).IsLoopback() {
		return fmt.Errorf("native model fixture requires an HTTP loopback endpoint")
	}
	return writeConfig(path, provider, endpoint, "native-fixture-only")
}

func writeConfig(path, provider, endpoint, apiKey string) error {
	cfg := config.Default()
	cfg.DefaultProvider = provider
	cfg.Providers = map[string]config.ProviderConfig{provider: {
		Type: "openai-compatible", BaseURL: endpoint, APIKey: apiKey, Model: provider,
		Models: map[string]config.ProviderModelConfig{
			provider: {Name: "Fixture model"},
			"alternate": {Name: "Alternate fixture model", Variants: map[string]map[string]any{
				"careful": {"reasoningEffort": "high"},
			}},
		},
	}}
	if apiKey != "" {
		// Title sampling must not consume a user-turn release token.
		cfg.Agent.ModelRoles.Title = config.ModelRoleConfig{Provider: provider, Model: "native-fixture-title"}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
