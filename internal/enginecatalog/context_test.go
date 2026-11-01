package enginecatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAntigravityDiscoveryContextTracksNativeSettings(t *testing.T) {
	for _, profile := range []string{"antigravity-cli", "antigravity-acp"} {
		t.Run(profile, func(t *testing.T) {
			home := withIsolatedHome(t)
			geminiHome := filepath.Join(home, "custom-gemini")
			t.Setenv("GEMINI_HOME", geminiHome)
			binary := writeExec(t, filepath.Join(home, "bin", "agy"))
			root := t.TempDir()
			before, err := DiscoveryContext("antigravity", binary, root)
			if err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(geminiHome, profile, "settings.json")
			if err := os.MkdirAll(filepath.Dir(settings), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settings, []byte(`{"model":"fixture"}`), 0600); err != nil {
				t.Fatal(err)
			}
			after, err := DiscoveryContext("antigravity", binary, root)
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Fatal("native settings changed without invalidating discovery")
			}
		})
	}
}
