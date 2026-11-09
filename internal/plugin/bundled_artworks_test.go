package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledArtworksDiscovery(t *testing.T) {
	currentVersion, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	plugins := discoverBundled(t.TempDir(), DiscoverOptions{
		GOOS:       "darwin",
		WuuVersion: strings.TrimSpace(string(currentVersion)),
		LookPath:   func(command string) (string, error) { return command, nil },
		LookupEnv:  func(string) (string, bool) { return "", false },
	})
	for _, item := range plugins {
		if item.ID != "artworks" {
			continue
		}
		if len(item.Skins) != 8 {
			t.Fatalf("artworks skins = %d, want 8: %+v", len(item.Skins), item.Skins)
		}
		for _, skin := range item.Skins {
			for mode, variant := range map[string]SkinVariantSpec{"light": skin.Light, "dark": skin.Dark} {
				if variant.Image == "" {
					t.Fatalf("skin %q %s variant has no image", skin.ID, mode)
				}
				info, err := os.Stat(filepath.Join(item.Root, variant.Image))
				if err != nil || !info.Mode().IsRegular() {
					t.Fatalf("skin %q %s image %q: %v", skin.ID, mode, variant.Image, err)
				}
			}
		}
		return
	}
	t.Fatal("bundled artworks plugin was not discovered")
}
