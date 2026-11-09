package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateHostCompatibility(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "runtime")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := Plugin{Manifest: Manifest{
		ID:                "compatible",
		Platforms:         []string{"darwin", "linux"},
		MinimumWuuVersion: "0.14.0",
		Runtime:           &RuntimeSpec{Command: executable},
	}}
	options := CompatibilityOptions{
		GOOS:       "darwin",
		WuuVersion: "v0.15.0-dev",
		LookPath: func(command string) (string, error) {
			if command == executable {
				return command, nil
			}
			return "", errors.New("missing")
		},
	}
	if err := ValidateHostCompatibility(base, options); err != nil {
		t.Fatalf("compatible plugin rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Plugin)
		want   string
	}{
		{name: "platform", mutate: func(item *Plugin) { item.Platforms = []string{"windows"} }, want: "does not support platform"},
		{name: "minimum version", mutate: func(item *Plugin) { item.MinimumWuuVersion = "0.16.0" }, want: "requires Wuu"},
		{name: "invalid minimum", mutate: func(item *Plugin) { item.MinimumWuuVersion = "next" }, want: "major.minor.patch"},
		{name: "runtime", mutate: func(item *Plugin) { item.Runtime.Command = "missing-runtime" }, want: "is not executable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := base
			manifest := base.Manifest
			runtimeSpec := *base.Runtime
			manifest.Runtime = &runtimeSpec
			item.Manifest = manifest
			test.mutate(&item)
			if err := ValidateHostCompatibility(item, options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateHostCompatibility() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverWithOptionsFiltersIncompatibleCommunityPlugins(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writePlugin(t, filepath.Join(home, "plugins", "wrong-platform"), `{"id":"wrong-platform","platforms":["windows"]}`)
	writePlugin(t, filepath.Join(root, ".wuu", "plugins", "future"), `{"id":"future","minimumWuuVersion":"0.16.0"}`)
	writePlugin(t, filepath.Join(root, ".wuu", "plugins", "ready"), `{"id":"ready","minimumWuuVersion":"0.14.0"}`)

	plugins := DiscoverWithOptions(root, home, DiscoverOptions{
		GOOS:       "darwin",
		WuuVersion: "v0.15.0-dev",
		LookPath:   func(command string) (string, error) { return command, nil },
	})
	if _, ok := findPlugin(plugins, "wrong-platform"); ok {
		t.Fatal("wrong-platform plugin was discovered")
	}
	if _, ok := findPlugin(plugins, "future"); ok {
		t.Fatal("future plugin was discovered")
	}
	if _, ok := findPlugin(plugins, "ready"); !ok {
		t.Fatalf("compatible plugin missing: %+v", plugins)
	}
}

func TestInspectPackageRejectsUnsupportedPlatform(t *testing.T) {
	source := t.TempDir()
	writeFile(t, filepath.Join(source, ManifestFilename), `{"id":"wrong-platform","platforms":["definitely-not-this-host"]}`)
	if _, err := InspectPackage(source); err == nil || !strings.Contains(err.Error(), "does not support platform") {
		t.Fatalf("InspectPackage() error = %v", err)
	}
}
