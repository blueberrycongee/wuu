package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/extensions"
)

func writeSelfReferentialSymlink(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir symlink parent: %v", err)
	}
	if err := os.Symlink(filepath.Base(path), path); err != nil {
		t.Skipf("self-referential symlinks are unavailable: %v", err)
	}
}

func TestLoadFrom_UsesUserProviderAndProjectAgentSettings(t *testing.T) {
	t.Setenv("WUU_HOME", "")
	workdir := t.TempDir()
	home := t.TempDir()

	homeConfig := filepath.Join(home, ".wuu", "config.json")
	if err := os.MkdirAll(filepath.Dir(homeConfig), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	homeJSON := `{
  "default_provider": "home",
  "providers": {
    "home": {
      "type": "openai-compatible",
      "base_url": "https://home.example/v1",
      "api_key_env": "HOME_KEY",
      "model": "home-model"
    }
  },
  "agent": {
    "max_steps": 4,
    "temperature": 0.1,
    "system_prompt": "home"
  }
}`
	if err := os.WriteFile(homeConfig, []byte(homeJSON), 0o644); err != nil {
		t.Fatalf("write home config: %v", err)
	}

	localPath := filepath.Join(workdir, ".wuu.json")
	localJSON := `{
  "default_provider": "local",
  "providers": {
    "local": {
      "type": "openai-compatible",
      "base_url": "https://local.example/v1",
      "api_key_env": "LOCAL_KEY",
      "model": "local-model"
    }
  },
  "agent": {
    "max_steps": 3,
    "temperature": 0.3,
    "system_prompt": "local"
  }
}`
	if err := os.WriteFile(localPath, []byte(localJSON), 0o644); err != nil {
		t.Fatalf("write local config: %v", err)
	}

	cfg, path, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if path != homeConfig {
		t.Fatalf("expected user path %q, got %q", homeConfig, path)
	}
	if cfg.DefaultProvider != "home" {
		t.Fatalf("expected user default provider, got %q", cfg.DefaultProvider)
	}
	if _, ok := cfg.Providers["local"]; ok {
		t.Fatalf("project provider must not be introduced: %+v", cfg.Providers)
	}
	if cfg.Agent.MaxSteps != 3 || cfg.Agent.Temperature != 0.3 {
		t.Fatalf("project agent settings were not applied: %+v", cfg.Agent)
	}
}

func TestConfigRejectsNegativeMaxParallel(t *testing.T) {
	cfg := Default()
	cfg.Agent.MaxParallel = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "agent.max_parallel") {
		t.Fatalf("Validate error = %v, want agent.max_parallel error", err)
	}
}

func TestConfig_ModelRolesRejectUnknownProvider(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {
				Type:    "openai-compatible",
				BaseURL: "https://example.com/v1",
				Model:   "gpt-5-codex",
			},
		},
		Agent: AgentConfig{
			ModelRoles: ModelRolesConfig{
				Review: ModelRoleConfig{Provider: "missing", Model: "review-model"},
			},
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `agent.model_roles.review.provider "missing" not found`) {
		t.Fatalf("expected unknown provider validation error, got %v", err)
	}
}

func TestRetiredMemoryProductFieldsAreIgnoredAtLoadBoundary(t *testing.T) {
	cfg, err := decodeConfig([]byte(`{
  "memory": {
    "nudge_interval": 3,
    "memory_char_limit": 2200,
    "user_char_limit": 1375
  }
}`), "legacy-memory.json")
	if err != nil {
		t.Fatalf("decodeConfig: %v", err)
	}
	if len(cfg.Instructions.Filenames) != 0 || len(cfg.Instructions.ProjectRootMarkers) != 0 ||
		len(cfg.Instructions.UserDirs) != 0 || cfg.Instructions.IncludeLegacyInstructions != nil {
		t.Fatalf("retired memory product fields entered runtime state: %+v", cfg.Instructions)
	}
}

func TestRetiredCoordinationModelDoesNotBlockConfigLoading(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	data := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "model": "main-model"
    }
  },
  "agent": {
    "model_roles": {
      "coordination": {"provider": "removed-provider", "model": "old-room-model"},
      "verification": {"provider": "main", "model": "verification-model"}
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatalf("load config with retired coordination model: %v", err)
	}
	if cfg.DefaultProvider != "main" || cfg.Agent.ModelRoles.Verification.Model != "verification-model" {
		t.Fatalf("active model selections were not preserved: %+v", cfg.Agent.ModelRoles)
	}
}

func TestUpdateExtensionSettingsPreservesConcurrentDecisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := writeConfigJSON(path, Default()); err != nil {
		t.Fatalf("write config: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, subjectID := range []string{"plugin:project:first", "plugin:project:second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := UpdateExtensionSettings(path, func(settings *extensions.Settings) error {
				settings.SetDisabled(subjectID, true)
				return nil
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("update extension settings: %v", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if cfg.Extensions == nil || !cfg.Extensions.IsDisabled("plugin:project:first") || !cfg.Extensions.IsDisabled("plugin:project:second") {
		t.Fatalf("concurrent extension decisions were not preserved: %+v", cfg.Extensions)
	}
}

func writeConfigJSON(path string, cfg Config) error {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

func TestDefaultConfigUsesPermissionModeAsPolicySource(t *testing.T) {
	cfg := Default()
	permissions := ResolveAgentPermissions(cfg.Agent)
	if permissions.Mode != PermissionModeStandard {
		t.Fatalf("default permissions = %+v", permissions)
	}
}

func TestNormalizePermissionModeUsesThreeStateAuthority(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: PermissionModeStandard},
		{in: PermissionModeStandard, want: PermissionModeStandard},
		{in: PermissionModeReadOnly, want: PermissionModeReadOnly},
		{in: PermissionModeUnconfined, want: PermissionModeUnconfined},
		{in: "not-a-mode", want: PermissionModeStandard},
	}
	for _, tt := range tests {
		if got := NormalizePermissionMode(tt.in); got != tt.want {
			t.Fatalf("NormalizePermissionMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLoadFrom_IgnoresRetiredSystemPromptFields(t *testing.T) {
	t.Setenv("WUU_HOME", "")
	workdir := t.TempDir()
	home := t.TempDir()
	homeConfig := filepath.Join(home, ".wuu", "config.json")
	if err := os.MkdirAll(filepath.Dir(homeConfig), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	homeJSON := `{
  "default_provider": "home",
  "providers": {
    "home": {
      "type": "openai-compatible",
      "base_url": "https://home.example/v1",
      "api_key_env": "HOME_KEY",
      "model": "home-model"
    }
  },
  "agent": {
    "system_prompt": "legacy instructions",
    "append_system_prompt": "preferred instructions"
  }
}`
	if err := os.WriteFile(homeConfig, []byte(homeJSON), 0o644); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	cfg, _, err := LoadFrom(workdir, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.DefaultProvider != "home" {
		t.Fatalf("retired prompt fields should not block load, got provider %q", cfg.DefaultProvider)
	}
}

func TestConfig_RejectsUnknownStreamTransport(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.com",
      "wire_api": "responses",
      "stream_transport": "socket-party",
      "api_key": "sk-test",
      "model": "gpt-test"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`

	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := LoadProjectConfig(workdir)
	if err == nil || !strings.Contains(err.Error(), "stream_transport") {
		t.Fatalf("expected stream_transport validation error, got %v", err)
	}
}

func TestConfig_IgnoresLegacyPermissionKeys(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.com",
      "api_key": "sk-test",
      "model": "gpt-test"
    }
  },
  "agent": {
    "system_prompt": "test",
    "permission_mode": "full_access",
    "permission_profile": "danger_full_access",
    "approval_policy": "never",
    "approvals_reviewer": "auto_review",
    "permission_rules": {
      "bash": "ask"
    },
    "tool_policy": {
      "default_action": "allow",
      "tools": {
        "run_shell": "require_approval"
      },
      "kinds": {
        "web": "allow"
      },
      "risks": {
        "medium": "auto_classify",
        "high": "deny"
      }
    }
  }
}`

	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatalf("LoadFrom returned error: %v", err)
	}
	if cfg.Agent.PermissionMode != PermissionModeStandard {
		t.Fatalf("legacy full_access should normalize to standard, got %q", cfg.Agent.PermissionMode)
	}
}

func TestConfig_CodexSubscriptionAllowsDefaultBaseURL(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-codex",
      "wire_api": "responses",
      "model": "gpt-5-codex"
    }
  },
  "agent": {
    "max_steps": 0,
    "temperature": 0.2,
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Providers["main"].Type != "openai-codex" {
		t.Fatalf("provider type = %q", cfg.Providers["main"].Type)
	}
}

func TestConfig_CodexSubscriptionDefaultsLegacyCredentialReuse(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-codex",
      "base_url": "https://chatgpt.com/backend-api/codex",
      "wire_api": "responses",
      "model": "gpt-5-codex"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if !cfg.Providers["main"].ReuseCodexCredentials {
		t.Fatal("expected legacy openai-codex config to default reuse_codex_credentials")
	}
}

func TestConfig_CodexSubscriptionPreservesExplicitCredentialReuseFalse(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-codex",
      "base_url": "https://chatgpt.com/backend-api/codex",
      "wire_api": "responses",
      "model": "gpt-5-codex",
      "reuse_codex_credentials": false
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Providers["main"].ReuseCodexCredentials {
		t.Fatal("expected explicit reuse_codex_credentials=false to be preserved")
	}
}

func TestConfig_CodexSubscriptionRejectsChatWireAPI(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-codex",
      "wire_api": "chat",
      "model": "gpt-5-codex"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`

	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := LoadProjectConfig(workdir)
	if err == nil {
		t.Fatal("expected codex wire_api validation error")
	}
	if !strings.Contains(err.Error(), "wire_api") || !strings.Contains(err.Error(), "responses") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfig_RejectsUnknownWireAPI(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.com",
      "wire_api": "legacy",
      "api_key": "sk-test",
      "model": "gpt-test"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`

	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, _, err := LoadProjectConfig(workdir)
	if err == nil {
		t.Fatal("expected unknown wire_api validation error")
	}
	if !strings.Contains(err.Error(), "wire_api") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateAdvancedRuntimePersistsAgentAndProviderSettings(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://x",
      "api_key": "k",
      "model": "test"
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatal(err)
	}
	maxSteps := 12
	maxContext := 256000
	temperature := 0.4
	compactPct := 0.5
	compactKeepRecent := 20000
	disableAutoCompact := true
	providerContext := 512000
	if err := UpdateAdvancedRuntime(configPath, "main", AdvancedRuntimeUpdate{
		MaxSteps:                &maxSteps,
		MaxContextTokens:        &maxContext,
		Temperature:             &temperature,
		CompactThresholdPct:     &compactPct,
		CompactKeepRecentTokens: &compactKeepRecent,
		DisableAutoCompact:      &disableAutoCompact,
		ProviderContextWindow:   &providerContext,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.MaxSteps != maxSteps ||
		cfg.Agent.MaxContextTokens != maxContext ||
		cfg.Agent.Temperature != temperature ||
		cfg.Agent.CompactThresholdPct != compactPct ||
		cfg.Agent.CompactKeepRecentTokens != compactKeepRecent ||
		!cfg.Agent.DisableAutoCompact {
		t.Fatalf("advanced agent settings not persisted: %+v", cfg.Agent)
	}
	if cfg.Providers["main"].ContextWindow != providerContext {
		t.Fatalf("provider context_window = %d, want %d", cfg.Providers["main"].ContextWindow, providerContext)
	}
}

func TestUpdateAdvancedRuntimeDeletesTemperatureForAuto(t *testing.T) {
	workdir := t.TempDir()
	configPath := filepath.Join(workdir, ".wuu.json")
	jsonData := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://x",
      "api_key": "k",
      "model": "test"
    }
  },
  "agent": {
    "temperature": 0.4
  }
}`
	if err := os.WriteFile(configPath, []byte(jsonData), 0o644); err != nil {
		t.Fatal(err)
	}
	auto := 0.0
	if err := UpdateAdvancedRuntime(configPath, "main", AdvancedRuntimeUpdate{Temperature: &auto}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Temperature != 0 {
		t.Fatalf("temperature = %v, want Auto/0", cfg.Agent.Temperature)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"temperature"`) {
		t.Fatalf("Auto temperature should remove config override:\n%s", string(data))
	}
}

func TestLoadFrom_NotFound(t *testing.T) {
	_, _, err := LoadFrom(t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error when config is missing")
	}
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

func TestLoadFrom_EmptyHomeDoesNotImplicitlyTrustProjectConfig(t *testing.T) {
	t.Setenv("WUU_HOME", filepath.Join(t.TempDir(), "missing-user-home"))
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, localPrimaryConfig), []byte(`{
  "default_provider": "project",
  "providers": {
    "project": {
      "type": "openai-compatible",
      "base_url": "https://project.example/v1",
      "api_key_env": "PROJECT_KEY",
      "model": "project-model"
    }
  }
}`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	if _, _, err := LoadFrom(workdir, ""); !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("LoadFrom with empty home = %v, want ErrConfigNotFound", err)
	}
	cfg, _, err := LoadProjectConfig(workdir)
	if err != nil {
		t.Fatalf("explicit LoadProjectConfig: %v", err)
	}
	if cfg.DefaultProvider != "project" {
		t.Fatalf("explicit project config was not loaded: %+v", cfg)
	}
}

func TestLoadFrom_EmptyHomeArgumentUsesWUUHome(t *testing.T) {
	wuuHome := filepath.Join(t.TempDir(), "wuu-home")
	t.Setenv("WUU_HOME", wuuHome)
	if err := os.MkdirAll(wuuHome, 0o700); err != nil {
		t.Fatalf("mkdir WUU_HOME: %v", err)
	}
	configPath := filepath.Join(wuuHome, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
  "default_provider": "user",
  "providers": {
    "user": {
      "type": "openai-compatible",
      "base_url": "https://user.example/v1",
      "api_key_env": "USER_KEY",
      "model": "user-model"
    }
  }
}`), 0o600); err != nil {
		t.Fatalf("write WUU_HOME config: %v", err)
	}

	cfg, loadedPath, err := LoadFrom(t.TempDir(), "")
	if err != nil {
		t.Fatalf("LoadFrom with WUU_HOME and empty home: %v", err)
	}
	if loadedPath != configPath || cfg.DefaultProvider != "user" {
		t.Fatalf("loaded path=%q config=%+v, want WUU_HOME config %q", loadedPath, cfg, configPath)
	}
}

// A present-but-broken config must NOT look like ErrConfigNotFound,
// otherwise callers that recover missing config could silently overwrite
// the user's existing .wuu.json.
func TestLoadFrom_BrokenConfigIsNotNotFound(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, ".wuu.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := LoadProjectConfig(workdir)
	if err == nil {
		t.Fatal("expected error for malformed config")
	}
	if errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("malformed config wrongly classified as not-found: %v", err)
	}
}

func TestLoadFrom_InvalidConfigIsNotNotFound(t *testing.T) {
	workdir := t.TempDir()
	// Valid JSON, fails Validate (no providers).
	if err := os.WriteFile(filepath.Join(workdir, ".wuu.json"), []byte(`{"default_provider":"x"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := LoadProjectConfig(workdir)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("invalid config wrongly classified as not-found: %v", err)
	}
}

func TestLoadProjectConfig_OnlyFallsBackWhenPrimaryIsMissing(t *testing.T) {
	t.Run("missing primary", func(t *testing.T) {
		workdir := t.TempDir()
		fallbackPath := filepath.Join(workdir, localFallbackConfig)
		if err := os.WriteFile(fallbackPath, []byte(migrateTestConfigJSON), 0o644); err != nil {
			t.Fatalf("write fallback config: %v", err)
		}

		_, loadedPath, err := LoadProjectConfig(workdir)
		if err != nil {
			t.Fatalf("LoadProjectConfig: %v", err)
		}
		if loadedPath != fallbackPath {
			t.Fatalf("loaded path = %q, want fallback %q", loadedPath, fallbackPath)
		}
	})

	t.Run("primary read error", func(t *testing.T) {
		workdir := t.TempDir()
		primaryPath := filepath.Join(workdir, localPrimaryConfig)
		writeSelfReferentialSymlink(t, primaryPath)
		if err := os.WriteFile(filepath.Join(workdir, localFallbackConfig), []byte(migrateTestConfigJSON), 0o644); err != nil {
			t.Fatalf("write fallback config: %v", err)
		}

		_, _, err := LoadProjectConfig(workdir)
		if err == nil {
			t.Fatal("expected the primary config read error")
		}
		if errors.Is(err, ErrConfigNotFound) {
			t.Fatalf("primary read error classified as not found: %v", err)
		}
		if !strings.Contains(err.Error(), primaryPath) {
			t.Fatalf("error %q does not identify primary config %q", err, primaryPath)
		}
	})
}

func TestUpdateProviderModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "myp",
  "providers": {
    "myp": {
      "type": "anthropic",
      "base_url": "https://example.com",
      "model": "old-model"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateProviderModel(path, "myp", "new-model"); err != nil {
		t.Fatalf("UpdateProviderModel: %v", err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	p, _, _ := cfg.ResolveProvider("myp")
	if p.Model != "new-model" {
		t.Fatalf("expected new-model, got %s", p.Model)
	}
}

func TestLoadFromAcceptsOpenCodeModelMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	data := `{
  "default_provider": "google",
  "providers": {
    "google": {
      "type": "openai-compatible",
      "base_url": "https://generativelanguage.googleapis.com/v1beta",
      "npm": "@ai-sdk/google",
      "model": "gemini-3-flash",
      "models": {
        "gemini-3-flash": {
          "id": "gemini-3-flash",
          "name": "Gemini 3 Flash",
          "release_date": "2026-01-01",
          "reasoning": true,
          "provider": {
            "npm": "@ai-sdk/google"
          },
          "limit": {
            "context": 1048576,
            "output": 65536
          }
        }
      }
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	model := cfg.Providers["google"].Models["gemini-3-flash"]
	if model.Provider == nil || model.Provider.NPM != "@ai-sdk/google" {
		t.Fatalf("provider metadata = %+v", model.Provider)
	}
	if model.Limit == nil || model.Limit.Output != 65536 {
		t.Fatalf("limit metadata = %+v", model.Limit)
	}
	if model.Reasoning == nil || !*model.Reasoning {
		t.Fatalf("reasoning metadata = %+v", model.Reasoning)
	}
}

func TestUpdateProviderModel_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	os.WriteFile(path, []byte(`{
  "default_provider": "a",
  "providers": {"a": {"type": "x", "base_url": "http://x", "model": "m"}},
  "agent": {"system_prompt": "t"}
}`), 0o644)

	if err := UpdateProviderModel(path, "nonexistent", "m"); err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestUpdateProviderSelection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "old",
  "providers": {
    "old": {
      "type": "openai-compatible",
      "base_url": "https://old.example.com",
      "model": "old-model"
    },
    "next": {
      "type": "openai-compatible",
      "base_url": "https://next.example.com",
      "model": "next-model"
    }
  },
  "agent": {
    "system_prompt": "test"
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateProviderSelection(path, "next", "chosen-model"); err != nil {
		t.Fatalf("UpdateProviderSelection: %v", err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultProvider != "next" {
		t.Fatalf("expected default provider next, got %q", cfg.DefaultProvider)
	}
	p, _, _ := cfg.ResolveProvider("next")
	if p.Model != "chosen-model" {
		t.Fatalf("expected chosen-model, got %s", p.Model)
	}
	old, _, _ := cfg.ResolveProvider("old")
	if old.Model != "old-model" {
		t.Fatalf("old provider model changed: %s", old.Model)
	}
}

func TestUpdateProviderRuntimePersistsConnectionFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "old",
  "providers": {
    "old": {
      "type": "openai-compatible",
      "base_url": "https://old.example.com",
      "api_key_env": "OLD_KEY",
      "model": "old-model"
    },
    "next": {
      "type": "openai-compatible",
      "base_url": "https://next.example.com",
      "model": "next-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	baseURL := "https://custom.example.com/v1"
	apiKey := "sk-custom"
	if err := UpdateProviderRuntime(path, "next", "custom-model", &baseURL, &apiKey, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateProviderRuntime: %v", err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultProvider != "next" {
		t.Fatalf("expected default provider next, got %q", cfg.DefaultProvider)
	}
	next, _, _ := cfg.ResolveProvider("next")
	if next.Model != "custom-model" || next.BaseURL != baseURL || next.APIKey != apiKey {
		t.Fatalf("provider runtime fields not persisted: %+v", next)
	}
	if next.APIKeyEnv != "" {
		t.Fatalf("expected explicit api_key to clear api_key_env, got %q", next.APIKeyEnv)
	}
	old, _, _ := cfg.ResolveProvider("old")
	if old.Model != "old-model" || old.BaseURL != "https://old.example.com" {
		t.Fatalf("old provider changed: %+v", old)
	}
}

func TestUpdateProviderRuntimePersistsPermissionMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "agent": {
    "tool_policy": {
      "tools": {
        "run_shell": "allow"
      }
    }
  },
  "default_provider": "old",
  "providers": {
    "old": {
      "type": "openai-compatible",
      "base_url": "https://old.example.com",
      "model": "old-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	mode := PermissionModeUnconfined
	if err := UpdateProviderRuntime(path, "old", "old-model", nil, nil, nil, nil, nil, &mode, nil); err != nil {
		t.Fatalf("UpdateProviderRuntime: %v", err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	permissions := ResolveAgentPermissions(cfg.Agent)
	if permissions.Mode != PermissionModeUnconfined {
		t.Fatalf("permission mode not persisted: %+v", permissions)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, legacyKey := range []string{"tool_policy", "permission_profile", "approval_policy", "approvals_reviewer", "permission_rules"} {
		if strings.Contains(string(raw), legacyKey) {
			t.Fatalf("legacy permission key %q should be removed from config:\n%s", legacyKey, raw)
		}
	}
}

func TestCreateProviderRuntimePersistsNewProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "old",
  "providers": {
    "old": {
      "type": "openai-compatible",
      "base_url": "https://old.example.com",
      "api_key": "old-key",
      "model": "old-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	baseURL := "https://custom.example.com/v1"
	apiKey := "sk-custom"
	if err := CreateProviderRuntime(path, "custom-1", nil, "custom-model", &baseURL, &apiKey, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("CreateProviderRuntime: %v", err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultProvider != "custom-1" {
		t.Fatalf("expected default provider custom-1, got %q", cfg.DefaultProvider)
	}
	custom, _, _ := cfg.ResolveProvider("custom-1")
	if custom.Type != "openai-compatible" || custom.Model != "custom-model" || custom.BaseURL != baseURL || custom.APIKey != apiKey {
		t.Fatalf("new provider not persisted: %+v", custom)
	}
	old, _, _ := cfg.ResolveProvider("old")
	if old.Model != "old-model" || old.BaseURL != "https://old.example.com" {
		t.Fatalf("old provider changed: %+v", old)
	}
}

func TestGrokBuildDefaultsAndRuntimeCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "old",
  "providers": {
    "old": {"type": "openai-compatible", "base_url": "https://old.example.com", "model": "old"}
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	providerType := "grok-build"
	if err := CreateProviderRuntime(path, "grok-build", &providerType, "grok-4.5", nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("CreateProviderRuntime: %v", err)
	}
	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	created := cfg.Providers["grok-build"]
	if created.BaseURL != "https://cli-chat-proxy.grok.com/v1" || created.WireAPI != "chat" || !created.ReuseGrokCredentials || len(created.Models) != 3 {
		t.Fatalf("created provider = %+v", created)
	}
}

func TestUpdateProviderRuntimePersistsExplicitCodexCredentialReuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "openai-codex",
  "providers": {
    "openai-codex": {
      "type": "openai-codex",
      "base_url": "https://chatgpt.com/backend-api/codex",
      "model": "gpt-6-astra",
      "reuse_codex_credentials": false
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err := UpdateProviderRuntime(path, "openai-codex", "gpt-6-astra", nil, nil, nil, nil, nil, nil, &enabled); err != nil {
		t.Fatalf("UpdateProviderRuntime: %v", err)
	}
	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Providers["openai-codex"].ReuseCodexCredentials {
		t.Fatal("expected reuse_codex_credentials to persist as true")
	}
}

func TestGrokBuildConfigAllowsDefaultBaseURLAndRejectsResponses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	valid := `{
  "default_provider": "grok",
  "providers": {
    "grok": {"type": "grok-build", "wire_api": "chat", "model": "grok-4.5", "reuse_grok_credentials": true}
  },
  "agent": {}
}`
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("load grok-build: %v", err)
	}
	if !cfg.Providers["grok"].ReuseGrokCredentials {
		t.Fatal("reuse_grok_credentials was not parsed")
	}
	invalid := strings.Replace(valid, `"wire_api": "chat"`, `"wire_api": "responses"`, 1)
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadProjectConfig(dir); err == nil || !strings.Contains(err.Error(), `wire_api must be "chat"`) {
		t.Fatalf("responses wire error = %v", err)
	}
}

func TestRemoveProviderInactiveKeepsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "keep",
  "providers": {
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.com/v1",
      "model": "keep-model"
    },
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.com/v1",
      "model": "drop-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	newDefault, err := RemoveProvider(path, "drop", "", "")
	if err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if newDefault != "" {
		t.Fatalf("expected empty newDefault when inactive provider is removed, got %q", newDefault)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultProvider != "keep" {
		t.Fatalf("default provider changed: %q", cfg.DefaultProvider)
	}
	if _, _, err := cfg.ResolveProvider("drop"); err == nil {
		t.Fatal("expected drop provider to be removed")
	}
	if _, _, err := cfg.ResolveProvider("keep"); err != nil {
		t.Fatalf("keep provider unexpectedly removed: %v", err)
	}
}

func TestRemoveProviderActiveSwapsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "drop",
  "providers": {
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.com/v1",
      "model": "drop-model"
    },
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.com/v1",
      "model": "keep-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	newDefault, err := RemoveProvider(path, "drop", "keep", "")
	if err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if newDefault != "keep" {
		t.Fatalf("expected newDefault=keep, got %q", newDefault)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultProvider != "keep" {
		t.Fatalf("default provider not swapped: %q", cfg.DefaultProvider)
	}
	keep, _, _ := cfg.ResolveProvider("keep")
	if keep.Model != "keep-model" {
		t.Fatalf("keep provider model unexpectedly changed: %q", keep.Model)
	}
}

func TestRemoveProviderActiveAppliesFallbackModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "drop",
  "providers": {
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.com/v1",
      "model": "drop-model"
    },
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.com/v1",
      "model": "keep-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveProvider(path, "drop", "keep", "fallback-model"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	keep, _, _ := cfg.ResolveProvider("keep")
	if keep.Model != "fallback-model" {
		t.Fatalf("expected fallback model applied, got %q", keep.Model)
	}
}

func TestRemoveProviderRejectsLastProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "only",
  "providers": {
    "only": {
      "type": "openai-compatible",
      "base_url": "https://only.example.com/v1",
      "model": "only-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveProvider(path, "only", "", ""); err == nil {
		t.Fatal("expected error when removing the only provider without fallback")
	}

	cfg, _, err := LoadProjectConfig(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.DefaultProvider != "only" {
		t.Fatalf("default provider changed on failed removal: %q", cfg.DefaultProvider)
	}
	if _, _, err := cfg.ResolveProvider("only"); err != nil {
		t.Fatalf("only provider should still exist: %v", err)
	}
}

func TestRemoveProviderRejectsUnknownProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "real",
  "providers": {
    "real": {
      "type": "openai-compatible",
      "base_url": "https://real.example.com/v1",
      "model": "real-model"
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveProvider(path, "ghost", "", ""); err == nil {
		t.Fatal("expected error when removing a non-existent provider")
	}
}

func TestRemoveProviderClearsModelRoleReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "keep",
  "providers": {
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.com/v1",
      "model": "keep-model"
    },
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.com/v1",
      "model": "drop-model"
    }
  },
  "agent": {
    "model_roles": {
      "review": { "provider": "drop", "model": "drop-model" },
      "compact": { "provider": "keep", "model": "keep-model" }
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveProvider(path, "drop", "", ""); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"provider": "drop"`) {
		t.Fatalf("review role provider was not cleared: %s", data)
	}
	if !strings.Contains(string(data), `"provider": "keep"`) {
		t.Fatalf("compact role provider was unexpectedly removed: %s", data)
	}
}

func TestConfig_ModelAliasesColonContainingModelID(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {
				Type:    "openai-compatible",
				BaseURL: "https://example.com/v1",
				Model:   "gpt-5-codex",
			},
		},
		Agent: AgentConfig{
			ModelAliases: map[string]ModelRoleConfig{
				"local": {Provider: "main", Model: "llama3.2:latest"},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("colon-containing API model ID should be valid: %v", err)
	}
}

func TestConfig_ModelAliasesRejectInvalidName(t *testing.T) {
	cases := []string{"", "Frontend", "1cheap", "cheap!", "cheap alias"}
	for _, name := range cases {
		cfg := Config{
			DefaultProvider: "main",
			Providers: map[string]ProviderConfig{
				"main": {Type: "openai-compatible", BaseURL: "https://example.com/v1", Model: "gpt-5-codex"},
			},
			Agent: AgentConfig{
				ModelAliases: map[string]ModelRoleConfig{
					name: {Provider: "main", Model: "gpt-5-mini"},
				},
			},
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected invalid alias name %q to be rejected", name)
		}
	}
}

func TestConfig_ModelAliasesRejectDuplicateNormalizedName(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {Type: "openai-compatible", BaseURL: "https://example.com/v1", Model: "gpt-5-codex"},
		},
		Agent: AgentConfig{
			ModelAliases: map[string]ModelRoleConfig{
				"cheap":  {Provider: "main", Model: "gpt-5-mini"},
				" cheap": {Provider: "main", Model: "gpt-5-nano"},
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "normalize") {
		t.Fatalf("expected duplicate normalized alias error, got %v", err)
	}
}

func TestConfig_ModelAliasesRejectEmptyProvider(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {Type: "openai-compatible", BaseURL: "https://example.com/v1", Model: "gpt-5-codex"},
		},
		Agent: AgentConfig{
			ModelAliases: map[string]ModelRoleConfig{
				"cheap": {Provider: "", Model: "gpt-5-mini"},
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("expected empty provider error, got %v", err)
	}
}

func TestConfig_ModelAliasesRejectEmptyModel(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {Type: "openai-compatible", BaseURL: "https://example.com/v1", Model: "gpt-5-codex"},
		},
		Agent: AgentConfig{
			ModelAliases: map[string]ModelRoleConfig{
				"cheap": {Provider: "main", Model: ""},
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected empty model error, got %v", err)
	}
}

func TestConfig_ModelAliasesRejectUnknownProvider(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {Type: "openai-compatible", BaseURL: "https://example.com/v1", Model: "gpt-5-codex"},
		},
		Agent: AgentConfig{
			ModelAliases: map[string]ModelRoleConfig{
				"cheap": {Provider: "missing", Model: "gpt-5-mini"},
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `provider "missing" not found`) {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

func TestConfig_ModelAliasesRejectInvalidConfiguredVariant(t *testing.T) {
	cfg := Config{
		DefaultProvider: "main",
		Providers: map[string]ProviderConfig{
			"main": {
				Type:    "openai-compatible",
				BaseURL: "https://example.com/v1",
				Model:   "gpt-5-codex",
				Models: map[string]ProviderModelConfig{
					"custom-model": {
						Variants: map[string]map[string]any{
							"low":  {"reasoningEffort": "low"},
							"high": {"reasoningEffort": "high"},
						},
					},
				},
			},
		},
		Agent: AgentConfig{
			ModelAliases: map[string]ModelRoleConfig{
				"bad": {Provider: "main", Model: "custom-model", Effort: "medium"},
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected invalid effort error, got %v", err)
	}
}

func TestRemoveProviderDeletesModelAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "keep",
  "providers": {
    "keep": {
      "type": "openai-compatible",
      "base_url": "https://keep.example.com/v1",
      "model": "keep-model"
    },
    "drop": {
      "type": "openai-compatible",
      "base_url": "https://drop.example.com/v1",
      "model": "drop-model"
    }
  },
  "agent": {
    "model_aliases": {
      "drop-alias": { "provider": "drop", "model": "drop-model" },
      "keep-alias": { "provider": "keep", "model": "keep-model" }
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := RemoveProvider(path, "drop", "", ""); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"drop-alias"`) {
		t.Fatalf("drop alias was not deleted: %s", data)
	}
	if !strings.Contains(string(data), `"keep-alias"`) {
		t.Fatalf("keep alias was unexpectedly deleted: %s", data)
	}
}

func TestUpdateAdvancedRuntimeModelAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.com/v1",
      "model": "gpt-5-codex"
    }
  },
  "agent": {
    "model_aliases": {
      "old": { "provider": "main", "model": "old-model" }
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	cheap := ModelRoleConfig{Provider: "main", Model: "gpt-5-mini"}
	frontend := ModelRoleConfig{Provider: "main", Model: "gpt-5-frontend", Effort: "high"}
	if err := UpdateAdvancedRuntime(path, "main", AdvancedRuntimeUpdate{
		ModelAliases: map[string]*ModelRoleConfig{
			"cheap":    &cheap,
			"frontend": &frontend,
			"old":      nil,
		},
	}); err != nil {
		t.Fatalf("UpdateAdvancedRuntime: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"old"`) {
		t.Fatalf("old alias was not removed: %s", data)
	}
	if !strings.Contains(string(data), `"cheap"`) || !strings.Contains(string(data), `"frontend"`) {
		t.Fatalf("new aliases were not written: %s", data)
	}
	if !strings.Contains(string(data), `"effort": "high"`) {
		t.Fatalf("frontend effort was not written: %s", data)
	}
}

func TestUpdateAdvancedRuntimeModelAliasesEmptyMapClearsAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wuu.json")
	orig := `{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.com/v1",
      "model": "gpt-5-codex"
    }
  },
  "agent": {
    "model_aliases": {
      "old": { "provider": "main", "model": "old-model" }
    }
  }
}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := UpdateAdvancedRuntime(path, "main", AdvancedRuntimeUpdate{ModelAliases: map[string]*ModelRoleConfig{}}); err != nil {
		t.Fatalf("UpdateAdvancedRuntime: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "model_aliases") {
		t.Fatalf("model_aliases was not cleared: %s", data)
	}
}
