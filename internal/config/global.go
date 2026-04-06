package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const globalConfigRelPath = ".config/wuu/preferences.json"

type GlobalConfig struct {
	Theme                  string `json:"theme,omitempty"`
	HasCompletedOnboarding bool   `json:"has_completed_onboarding,omitempty"`
}

func LoadGlobalConfig(home string) (GlobalConfig, error) {
	path := filepath.Join(home, globalConfigRelPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GlobalConfig{}, nil
		}
		return GlobalConfig{}, err
	}
	var gc GlobalConfig
	if err := json.Unmarshal(data, &gc); err != nil {
		return GlobalConfig{}, fmt.Errorf("parse global config: %w", err)
	}
	return gc, nil
}

func SaveGlobalConfig(home string, gc GlobalConfig) error {
	path := filepath.Join(home, globalConfigRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create global config dir: %w", err)
	}
	data, err := json.MarshalIndent(gc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
