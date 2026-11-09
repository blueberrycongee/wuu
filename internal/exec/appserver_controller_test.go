package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
)

func TestLocalAppServerControllerInitializeAndResumeThread(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(t.TempDir(), "wuu-home"))
	configPath := filepath.Join(root, ".wuu.json")
	if err := os.WriteFile(configPath, []byte(`{
		"default_provider": "test",
		"providers": {
			"test": {
				"type": "openai-compatible",
				"base_url": "https://example.test/v1",
				"api_key": "sk-test",
				"model": "gpt-test"
			}
		},
		"agent": {
			"permission_mode": "standard"
		}
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controller, err := NewLocalAppServerController(ctx, Options{
		Workdir:    root,
		ConfigPath: configPath,
	})
	if err != nil {
		t.Fatalf("NewLocalAppServerController: %v", err)
	}
	defer controller.Shutdown(context.Background())

	init, err := controller.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.WorkspaceRoot != root || init.Provider != "test" || init.Model != "gpt-test" {
		t.Fatalf("unexpected initialize result: %+v", init)
	}
	if init.MaxParallel != config.DefaultAgentMaxParallel {
		t.Fatalf("initialize max_parallel = %d, want %d", init.MaxParallel, config.DefaultAgentMaxParallel)
	}
	thread, err := controller.StartThread(ctx, false)
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	if thread.ID == "" || thread.Ephemeral {
		t.Fatalf("unexpected started thread: %+v", thread)
	}
	resumed, err := controller.ResumeThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ResumeThread: %v", err)
	}
	if resumed.ID != thread.ID {
		t.Fatalf("resumed thread = %q, want %q", resumed.ID, thread.ID)
	}
}

func TestLocalAppServerControllerEffortOverrideClearsConfiguredVariant(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(t.TempDir(), "wuu-home"))
	configPath := filepath.Join(root, ".wuu.json")
	if err := os.WriteFile(configPath, []byte(`{
		"default_provider": "test",
		"providers": {
			"test": {
				"type": "openai-compatible",
				"base_url": "https://example.test/v1",
				"api_key": "sk-test",
				"model": "gpt-5.5"
			}
		},
		"agent": {
			"effort": "xhigh",
			"variant": "xhigh"
		}
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controller, err := NewLocalAppServerController(ctx, Options{
		Workdir:    root,
		ConfigPath: configPath,
		Effort:     "low",
		NoTools:    true,
	})
	if err != nil {
		t.Fatalf("NewLocalAppServerController: %v", err)
	}
	defer controller.Shutdown(context.Background())

	init, err := controller.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.Effort != "low" || init.Variant != "low" {
		t.Fatalf("effort override should select low variant, got effort=%q variant=%q", init.Effort, init.Variant)
	}
}

func TestLocalAppServerControllerIgnoreUserConfigReloadsProjectLayers(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(t.TempDir(), "unused-user-home"))
	configPath := filepath.Join(root, ".wuu.json")
	if err := os.WriteFile(configPath, []byte(`{
  "default_provider": "test",
  "providers": {
    "test": {
      "type": "openai-compatible",
      "base_url": "https://example.test/v1",
      "api_key": "sk-test",
      "model": "gpt-test"
    }
  },
  "agent": {"max_steps": 4, "effort": "low"}
}`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	settingsDir := filepath.Join(root, ".wuu")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir project settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{
  "agent": {"max_steps": 17, "effort": "high"}
}`), 0o644); err != nil {
		t.Fatalf("write project settings: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controller, err := NewLocalAppServerController(ctx, Options{
		Workdir:          root,
		IgnoreUserConfig: true,
		NoTools:          true,
	})
	if err != nil {
		t.Fatalf("NewLocalAppServerController: %v", err)
	}
	defer controller.Shutdown(context.Background())

	init, err := controller.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.AdvancedSettings.MaxSteps != 17 || init.Effort != "high" {
		t.Fatalf("initialize lost explicitly trusted project layer: advanced=%+v effort=%q", init.AdvancedSettings, init.Effort)
	}
}

func TestNewLocalAppServerControllerAppliesPermissionOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(t.TempDir(), "wuu-home"))
	configPath := filepath.Join(root, ".wuu.json")
	if err := os.WriteFile(configPath, []byte(`{
		"default_provider": "test",
		"providers": {
			"test": {
				"type": "openai-compatible",
				"base_url": "https://example.test/v1",
				"api_key": "sk-test",
				"model": "gpt-test"
			}
		},
		"agent": {
			"permission_mode": "unconfined"
		}
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	flagged, err := NewLocalAppServerController(ctx, Options{
		Workdir:        root,
		ConfigPath:     configPath,
		PermissionMode: "read_only",
		NoTools:        true,
	})
	if err != nil {
		t.Fatalf("NewLocalAppServerController: %v", err)
	}
	defer flagged.Shutdown(context.Background())
	flaggedInit, err := flagged.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize flagged controller: %v", err)
	}
	if flaggedInit.Permissions.Mode != config.PermissionModeReadOnly {
		t.Fatalf("--permission-mode should become the active override: mode=%q", flaggedInit.Permissions.Mode)
	}

	unflagged, err := NewLocalAppServerController(ctx, Options{
		Workdir:    root,
		ConfigPath: configPath,
		NoTools:    true,
	})
	if err != nil {
		t.Fatalf("NewLocalAppServerController without flag: %v", err)
	}
	defer unflagged.Shutdown(context.Background())
	unflaggedInit, err := unflagged.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize unflagged controller: %v", err)
	}
	if unflaggedInit.Permissions.Mode != config.PermissionModeUnconfined {
		t.Fatalf("config-sourced mode should remain active: mode=%q", unflaggedInit.Permissions.Mode)
	}
}

func TestApplyConfigOverridesExplicitVariantWinsOverEffort(t *testing.T) {
	cfg := config.Config{
		Agent: config.AgentConfig{
			Effort:  "medium",
			Variant: "medium",
		},
	}

	if err := applyConfigOverrides(&cfg, Options{Effort: "low", Variant: "high"}); err != nil {
		t.Fatalf("applyConfigOverrides: %v", err)
	}

	if cfg.Agent.Effort != "low" {
		t.Fatalf("Effort = %q, want low", cfg.Agent.Effort)
	}
	if cfg.Agent.Variant != "high" {
		t.Fatalf("Variant = %q, want high", cfg.Agent.Variant)
	}
}

func TestLocalControllerRetainsCleanupTransportAfterExecutionCancellation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("WUU_HOME", filepath.Join(root, "state"))
	started := make(chan struct{})
	var once sync.Once
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer endpoint.Close()
	path := filepath.Join(root, "config.json")
	if err := writeMinimalConfig(path, endpoint.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := Options{Workdir: root, ConfigPath: path, NoTools: true, Prompt: "work"}
	controller, err := NewLocalAppServerController(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Shutdown(context.Background())
	go func() {
		for range controller.Notifications() {
		}
	}()
	thread, err := controller.StartThread(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	run, err := controller.StartRun(ctx, runStartParams(opts, thread.ID, TurnInput{Prompt: "work"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not started")
	}
	cancel()
	cleanup, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCleanup()
	if _, err := controller.InterruptRun(cleanup, run.ID, "interrupted"); err != nil {
		t.Fatalf("execution cancellation destroyed cleanup transport: %v", err)
	}
	if err := controller.Shutdown(cleanup); err != nil {
		t.Fatalf("shutdown after explicit interrupt: %v", err)
	}
}

func TestLocalControllerCancelledStartupDoesNotOpenTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	controller, err := NewLocalAppServerController(ctx, Options{ConfigPath: filepath.Join(t.TempDir(), "missing.json")})
	if controller != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup: controller=%v err=%v", controller, err)
	}
}
