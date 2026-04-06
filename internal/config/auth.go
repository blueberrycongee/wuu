package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const authRelativePath = ".config/wuu/auth.json"

type authStore struct {
	Keys map[string]string `json:"keys"`
}

func SaveAuthKey(home, providerName, apiKey string) error {
	path := filepath.Join(home, authRelativePath)
	store, _ := loadAuthStore(path)
	if store.Keys == nil {
		store.Keys = make(map[string]string)
	}
	store.Keys[providerName] = apiKey
	return writeAuthStore(path, store)
}

func LoadAuthKey(home, providerName string) (string, error) {
	path := filepath.Join(home, authRelativePath)
	store, err := loadAuthStore(path)
	if err != nil {
		return "", err
	}
	key, ok := store.Keys[providerName]
	if !ok || key == "" {
		return "", fmt.Errorf("no auth key for provider %q", providerName)
	}
	return key, nil
}

func loadAuthStore(path string) (authStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return authStore{Keys: make(map[string]string)}, err
	}
	var store authStore
	if err := json.Unmarshal(data, &store); err != nil {
		return authStore{Keys: make(map[string]string)}, err
	}
	if store.Keys == nil {
		store.Keys = make(map[string]string)
	}
	return store, nil
}

func writeAuthStore(path string, store authStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
