// Package testsupport configures disposable native-client integration fixtures.
package testsupport

import (
	"encoding/json"
	"os"

	"github.com/blueberrycongee/wuu/internal/config"
)

// WriteConfig exposes a local model catalog; execution still uses the fixture's injected provider.
func WriteConfig(path, provider string) error {
	cfg := config.Default()
	cfg.DefaultProvider = provider
	cfg.Providers = map[string]config.ProviderConfig{provider: {
		Type: "openai-compatible", BaseURL: "http://127.0.0.1:1/v1", Model: provider,
		Models: map[string]config.ProviderModelConfig{
			provider: {Name: "Fixture model"},
			"alternate": {Name: "Alternate fixture model", Variants: map[string]map[string]any{
				"careful": {"reasoningEffort": "high"},
			}},
		},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
