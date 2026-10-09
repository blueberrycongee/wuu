package enginecatalog

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DiscoveryContext fingerprints the launch environment and native configuration
// metadata. It never reads credential contents or exposes environment values.
// A fresh probe still owns model membership; this key only permits cache reuse.
func DiscoveryContext(id, binary, root string) ([32]byte, error) {
	var key [32]byte
	root, err := filepath.Abs(root)
	if err != nil {
		return key, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return key, err
	}
	location := func(variable, fallback string) string {
		value := strings.TrimSpace(os.Getenv(variable))
		if value == "" {
			value = fallback
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(root, value)
		}
		return value
	}
	configHome := location("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataHome := location("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	paths := []string{binary}
	add := func(dir string, names ...string) {
		for _, name := range names {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	var projectFiles []string
	switch id {
	case "codex":
		add(location("CODEX_HOME", filepath.Join(home, ".codex")), "config.toml", "auth.json")
		projectFiles = []string{".codex/config.toml"}
	case "cursor":
		add(filepath.Join(home, ".cursor"), "cli-config.json", "sdk/auth.json")
		projectFiles = []string{".cursor/cli.json"}
	case "devin":
		for _, dir := range []string{filepath.Join(dataHome, "devin"), filepath.Join(configHome, "devin"), filepath.Join(home, "Library", "Application Support", "devin")} {
			add(dir, "credentials.toml", "config.toml")
		}
	case "grok":
		add(location("GROK_HOME", filepath.Join(home, ".grok")), "auth.json", "config.toml")
		projectFiles = []string{".grok/config.toml"}
	case "hermes":
		add(location("HERMES_HOME", filepath.Join(home, ".hermes")), "config.yaml", "auth.json", ".env")
	case "pi":
		add(location("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent")), "auth.json", "models.json", "settings.json")
		projectFiles = []string{".pi/settings.json"}
	case "opencode":
		add(filepath.Join(dataHome, "opencode"), "auth.json")
		add(filepath.Join(configHome, "opencode"), "opencode.json", "opencode.jsonc", "config.json")
		if os.Getenv("OPENCODE_CONFIG") != "" {
			paths = append(paths, location("OPENCODE_CONFIG", ""))
		}
		if os.Getenv("OPENCODE_CONFIG_DIR") != "" {
			add(location("OPENCODE_CONFIG_DIR", ""), "opencode.json", "opencode.jsonc")
		}
		projectFiles = []string{"opencode.json", "opencode.jsonc", ".opencode/opencode.json", ".opencode/opencode.jsonc"}
	case "antigravity":
		add(location("GEMINI_HOME", filepath.Join(home, ".gemini")), "settings.json", "oauth_creds.json")
		projectFiles = []string{".gemini/settings.json"}
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		add(dir, projectFiles...)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode([]string{id, binary, root})
	env := os.Environ()
	slices.Sort(env)
	_ = encoder.Encode(env)
	for _, path := range paths {
		_ = encoder.Encode(path)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			_ = encoder.Encode(nil)
			continue
		}
		if err != nil {
			return key, fmt.Errorf("%s discovery context: %w", id, err)
		}
		_ = encoder.Encode([]int64{info.Size(), info.ModTime().UnixNano(), int64(info.Mode())})
	}
	copy(key[:], hash.Sum(nil))
	return key, nil
}
