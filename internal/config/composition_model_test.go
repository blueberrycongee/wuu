package config

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
)

// A model-free composition must load from the same config entry point as the
// default product, without a fabricated connection or weakening real choices.
func TestModelFreeCompositionConfiguration(t *testing.T) {
	for _, profile := range []string{"desktop", "headless", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			cfg := Config{Composition: composition.DefaultConfig(profile)}
			err := cfg.Validate()
			if (err == nil) != (profile == "minimal") {
				t.Fatalf("model-free %s: %v", profile, err)
			}
		})
	}
	cfg := Config{Composition: &composition.Config{Version: composition.Version, Modules: &[]string{}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("explicit empty composition requires a model: %v", err)
	}
	cfg.DefaultProvider = "removed"
	if err := cfg.Validate(); err == nil {
		t.Fatal("explicit dangling model choice accepted")
	}
	cfg.DefaultProvider = ""
	cfg.Providers = map[string]ProviderConfig{"invalid": {}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid explicit model configuration accepted")
	}
	cfg.Providers = nil
	cfg.Composition = nil
	if err := cfg.Validate(); err == nil {
		t.Fatal("implicit default product accepted missing model configuration")
	}
}
