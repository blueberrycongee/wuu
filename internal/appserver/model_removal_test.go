package appserver

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
)

func TestServerConfigModelRemovalPersistsAndKeepsFallback(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	if err := os.WriteFile(rt.ConfigPath, []byte(`{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "model": "old", "models": {"old": {"name": "Old model"}, "next": {}}}},
  "agent": {"model_aliases": {"existing": {"provider": "fake-provider", "model": "old"}}}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"remove","method":"config/model/update","params":{"model":"next","remove_model":"old"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "remove")
	if response["error"] != nil {
		t.Fatalf("remove: %v", response["error"])
	}
	result := remarshal[ConfigModelUpdateResult](t, response["result"])
	if result.Model != "next" || len(result.Providers) != 1 || len(result.Providers[0].Models) != 1 || result.Providers[0].Models[0].ID != "next" {
		t.Fatalf("unexpected choices: %+v", result)
	}
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	provider := cfg.Providers["fake-provider"]
	if !provider.Models["old"].Disabled || provider.Model != "next" {
		t.Fatalf("removal not persisted: %+v", provider)
	}
	alias, err := modelroles.ResolveAlias(cfg, "existing")
	if err != nil || alias.Model != "old" || provider.Models["old"].Name != "Old model" {
		t.Fatalf("model-choice removal changed existing routing or metadata: alias=%+v err=%v", alias, err)
	}
	// Rediscovery supplies live metadata but must not undo a saved removal.
	provider.Type = "openai-codex"
	srv.codexModelCache = map[string]map[string]config.ProviderModelConfig{"fake-provider": {"old": {Name: "Rediscovered"}}}
	if !srv.withCachedCodexModels("fake-provider", provider).Models["old"].Disabled {
		t.Fatal("discovery resurrected removed model")
	}
	if err := srv.handleLine(context.Background(), []byte(`{"id":"last","method":"config/model/update","params":{"model":"next","remove_model":"next"}}`)); err != nil {
		t.Fatal(err)
	}
	if responseByID(t, parseOutput(t, out.String()), "last")["error"] == nil {
		t.Fatal("removed selected model without a fallback")
	}
	after, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatal("rejected removal changed config")
	}
}
