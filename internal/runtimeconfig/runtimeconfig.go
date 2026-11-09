package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// LoadOptions describes how an embedded runtime resolves its configuration.
type LoadOptions struct {
	RootDir               string
	HomeDir               string
	ConfigPath            string
	IgnoreUserConfig      bool
	CreateConfigIfMissing bool
}

// Overrides contains process-scoped settings shared with the runtime.
type Overrides = runtime.ConfigOverrides

// Load resolves a runtime config and records the source model that must be used
// for later reloads.
func Load(opts LoadOptions) (config.Config, string, runtime.ConfigLoadMode, error) {
	path := strings.TrimSpace(opts.ConfigPath)
	if path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(opts.RootDir, path)
		}
		cfg, configPath, err := config.LoadPath(path)
		return cfg, configPath, runtime.ConfigLoadFile, err
	}
	if opts.IgnoreUserConfig {
		cfg, configPath, err := config.LoadProjectConfig(opts.RootDir)
		return cfg, configPath, runtime.ConfigLoadProject, err
	}

	cfg, configPath, err := config.LoadFrom(opts.RootDir, opts.HomeDir)
	if err == nil || !errors.Is(err, config.ErrConfigNotFound) || !opts.CreateConfigIfMissing {
		return cfg, configPath, runtime.ConfigLoadNormal, err
	}
	cfg, configPath, err = createStarterConfig(opts.RootDir, opts.HomeDir)
	return cfg, configPath, runtime.ConfigLoadNormal, err
}

// ApplyOverrides applies process-scoped settings to a loaded config.
func ApplyOverrides(cfg *config.Config, overrides Overrides) error {
	return overrides.Apply(cfg)
}

// ResolveHost validates immutable process identity and the inputs required by
// managed runtimes.
func ResolveHost(kind, instanceID, workspaceID, configPath string) (runtime.Host, error) {
	host, err := runtime.ResolveHost(runtime.Host{Kind: runtime.HostKind(kind), InstanceID: instanceID})
	if err != nil {
		return runtime.Host{}, err
	}
	if host.Kind != runtime.HostCloud {
		return host, nil
	}
	if strings.TrimSpace(workspaceID) == "" {
		return runtime.Host{}, errors.New("cloud host requires a workspace id")
	}
	if strings.TrimSpace(configPath) == "" {
		return runtime.Host{}, errors.New("cloud host requires an explicit config; managed runtimes do not create starter config")
	}
	return host, nil
}

// StarterConfig returns the local first-run configuration used by the default
// host. It intentionally keeps only the subscription-backed provider so a new
// shell enters the same setup flow as the official clients. Local Codex CLI
// credential reuse stays off until first-run setup confirms it.
func StarterConfig() config.Config {
	cfg := config.Default()
	if provider, ok := cfg.Providers["openai-codex"]; ok {
		provider.ReuseCodexCredentials = false
		cfg.DefaultProvider = "openai-codex"
		cfg.Providers = map[string]config.ProviderConfig{
			"openai-codex": provider,
		}
	}
	return cfg
}

func createStarterConfig(rootDir, homeDir string) (config.Config, string, error) {
	configPath, err := statepath.ConfigPath(homeDir)
	if err != nil {
		return config.Config{}, "", fmt.Errorf("resolve user config: %w", err)
	}
	data, err := marshalStarterConfig(StarterConfig())
	if err != nil {
		return config.Config{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return config.Config{}, "", fmt.Errorf("create config directory: %w", err)
	}
	if err := securefs.WriteFileAtomic(configPath, append(data, '\n')); err != nil {
		return config.Config{}, "", fmt.Errorf("write starter config: %w", err)
	}
	return config.LoadFrom(rootDir, homeDir)
}

func marshalStarterConfig(cfg config.Config) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	providers, _ := raw["providers"].(map[string]any)
	provider, _ := providers["openai-codex"].(map[string]any)
	if provider != nil {
		// Persist an explicit false so legacy credential-reuse defaults do not
		// turn first-run setup into a silent Codex login import.
		provider["reuse_codex_credentials"] = false
	}
	return json.MarshalIndent(raw, "", "  ")
}
