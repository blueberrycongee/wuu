package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
)

func syntheticHome(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("synthetic fixture home is required")
	}
	path, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	marker, err := fixturePath(path, markerName)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		return "", fmt.Errorf("synthetic fixture marker required: %w", err)
	}
	if string(raw) != markerContent {
		return "", errors.New("invalid synthetic fixture marker")
	}
	return path, nil
}
func fixturePath(home, relative string) (string, error) {
	current := home
	for _, part := range strings.Split(relative, "/") {
		if part == ".." || part == "" {
			return "", errors.New("invalid fixture path")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("fixture paths cannot use symlinks: %s", current)
		}
	}
	return current, nil
}
func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".fixture-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(raw, '\n'))
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func install(home, executable string, prefix []string) (map[string]string, error) {
	home, err := syntheticHome(home)
	if err != nil {
		return nil, err
	}
	configPath, err := fixturePath(home, "config.json")
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("fixture configuration must be an object")
	}
	packageRoot, err := fixturePath(home, "plugins/"+providerID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(packageRoot, 0700); err != nil {
		return nil, err
	}
	manifestPath, err := fixturePath(home, "plugins/"+providerID+"/plugin.json")
	if err != nil {
		return nil, err
	}
	args := append(append([]string(nil), prefix...), "serve", "--home", home)
	manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: providerID, Version: "1.0.0", Runtime: &pluginpkg.RuntimeSpec{Protocol: "wuu-plugin-v1", Command: executable, Args: args}}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return nil, err
	}
	installed, err := pluginpkg.LoadManifest(manifestPath, "user")
	if err != nil {
		return nil, err
	}
	var settings extensions.Settings
	if raw := fields["extensions"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, err
		}
	}
	if err := settings.RecordGrant(extensions.Grant{SubjectID: installed.SubjectID, Fingerprint: installed.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: installed.EffectivePermissions, ApprovedAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	delete(settings.Disabled, installed.SubjectID)
	if settings.Enabled == nil {
		settings.Enabled = map[string]bool{}
	}
	settings.Enabled[installed.SubjectID] = true
	fields["extensions"], err = json.Marshal(settings)
	if err != nil {
		return nil, err
	}
	selection := composition.Config{Version: composition.Version}
	if raw := fields["composition"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &selection); err != nil {
			return nil, err
		}
	}
	resolved, err := selection.Resolve()
	if err != nil {
		return nil, err
	}
	if resolved.Modules != nil && !slices.Contains(*resolved.Modules, providerID) {
		*resolved.Modules = append(*resolved.Modules, providerID)
	}
	bindings := slices.DeleteFunc(resolved.Services, func(binding composition.Binding) bool {
		return binding.Service == persistence.ServiceName && binding.Major == 1
	})
	bindings = append(bindings, composition.Binding{Service: persistence.ServiceName, Major: 1, Provider: providerID})
	fields["composition"], err = json.Marshal(composition.Config{Version: composition.Version, Modules: resolved.Modules, Services: bindings})
	if err != nil {
		return nil, err
	}
	controlRoot, err := fixturePath(home, fixtureDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(controlRoot, 0700); err != nil {
		return nil, err
	}
	if err := writeJSON(configPath, fields); err != nil {
		return nil, err
	}
	return map[string]string{"provider_id": providerID, "faults_path": filepath.Join(controlRoot, "faults.json"), "rejections_path": filepath.Join(controlRoot, "rejections.jsonl")}, nil
}
