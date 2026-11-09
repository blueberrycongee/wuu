package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAddProviderIfMissingPreservesConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{
		"default_provider":"existing",
		"agent":{"effort":"low","permission_mode":"read_only","future_option":true},
		"providers":{"existing":{"type":"openai-compatible","model":"custom","future_option":123}},
		"future_section":{"enabled":true}
	}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := ProviderConfig{Type: "openai-compatible", Model: "discovered-model"}
	// Multiple app servers can select the same discovered connection at once.
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- AddProviderIfMissing(path, "discovered", provider)
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := AddProviderIfMissing(path, "existing", provider); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal([]byte(original), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	providers := after["providers"].(map[string]any)
	added, ok := providers["discovered"].(map[string]any)
	if !ok || added["model"] != provider.Model {
		t.Fatalf("missing registered provider: %+v", added)
	}
	delete(providers, "discovered")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("registering a provider changed existing settings or unknown fields")
	}
}
