package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestRefreshThreadGitAttributionUsesLatestPersistedSetting(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	writeConfig := func(content string) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(`{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.invalid/v1",
      "api_key": "test-key",
      "model": "test-model"
    }
  },
  "agent": {"git_attribution_enabled": false}
}`)

	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetGitAttributionEnabled(true)
	threadRuntime := &runtime.ThreadRuntime{Toolkit: kit}
	server := &Server{rt: &runtime.Session{
		ConfigPath:     configPath,
		ConfigLoadMode: runtime.ConfigLoadFile,
	}}

	if err := server.refreshThreadGitAttribution(threadRuntime); err != nil {
		t.Fatal(err)
	}
	if kit.GitAttributionEnabled() {
		t.Fatal("thread toolkit kept stale enabled attribution after persisted opt-out")
	}

	writeConfig(`{
  "default_provider": "main",
  "providers": {
    "main": {
      "type": "openai-compatible",
      "base_url": "https://example.invalid/v1",
      "api_key": "test-key",
      "model": "test-model"
    }
  },
  "agent": {}
}`)
	if err := server.refreshThreadGitAttribution(threadRuntime); err != nil {
		t.Fatal(err)
	}
	if !kit.GitAttributionEnabled() {
		t.Fatal("thread toolkit kept stale disabled attribution after persisted default-on setting")
	}
}
