package runtime

import (
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestSessionPTCGlobalSwitch(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		root, home := t.TempDir(), t.TempDir()
		t.Setenv("WUU_HOME", filepath.Join(home, "state"))
		t.Setenv("TEST_WUU_KEY", "fixture")
		s, err := NewSession(Options{RootDir: root, HomeDir: home, Config: config.Config{DefaultProvider: "test", Providers: map[string]config.ProviderConfig{"test": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: enabled}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = s.Cleanup() })
		thread, err := s.NewThreadRuntime("conversation")
		if err != nil {
			t.Fatal(err)
		}
		for _, kit := range []*tools.Toolkit{s.Toolkit, thread.Toolkit} {
			foundRun, foundRead := false, false
			for _, d := range kit.Definitions() {
				foundRun = foundRun || d.Name == "run_code"
				foundRead = foundRead || d.Name == "read_file"
			}
			if foundRun != enabled || !foundRead {
				t.Fatalf("PTC=%v run=%v read=%v", enabled, foundRun, foundRead)
			}
		}
	}
}

func TestWorkerPTCSurfaceUsesGlobalSetting(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	for _, enabled := range []bool{false, true} {
		kit.ConfigurePTC(service, config.PTCConfig{Enabled: enabled})
		kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
		surface, err := workerToolSurfaceForToolkit(kit, "anthropic", "claude-sonnet-4", true)
		if err != nil {
			t.Fatal(err)
		}
		_, run := surface.Tools["run_code"]
		_, direct := surface.Tools["read_file"]
		_, nested := surface.NestedTools["read_file"]
		if run != enabled || !direct || nested != enabled {
			t.Fatalf("worker surface=%+v", surface)
		}
	}
}
