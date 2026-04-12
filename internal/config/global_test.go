package config

import "testing"

func TestGlobalConfig_RoundTrip(t *testing.T) {
	home := t.TempDir()
	gc := GlobalConfig{Theme: "dark", HasCompletedOnboarding: true}
	if err := SaveGlobalConfig(home, gc); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadGlobalConfig(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Theme != "dark" {
		t.Fatalf("theme mismatch: %q", loaded.Theme)
	}
	if !loaded.HasCompletedOnboarding {
		t.Fatal("expected onboarding completed")
	}
}

func TestGlobalConfig_DefaultsWhenMissing(t *testing.T) {
	home := t.TempDir()
	gc, err := LoadGlobalConfig(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if gc.Theme != "" {
		t.Fatalf("expected empty theme, got %q", gc.Theme)
	}
}

func TestGlobalConfig_RequiresHomeDir(t *testing.T) {
	for _, home := range []string{"", "   \t\n  "} {
		t.Run("home", func(t *testing.T) {
			_, err := LoadGlobalConfig(home)
			if err == nil || err.Error() != "home directory is required" {
				t.Fatalf("expected clear home error from LoadGlobalConfig, got %v", err)
			}

			err = SaveGlobalConfig(home, GlobalConfig{Theme: "dark"})
			if err == nil || err.Error() != "home directory is required" {
				t.Fatalf("expected clear home error from SaveGlobalConfig, got %v", err)
			}
		})
	}
}
