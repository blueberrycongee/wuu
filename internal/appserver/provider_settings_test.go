package appserver

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Settings edit a service without choosing it: a key, endpoint, or model
// choice saved for one service must leave the workspace default where it was.

func writeProviderSettingsConfig(t *testing.T, rt *runtime.Session, body string) {
	t.Helper()
	if err := os.WriteFile(rt.ConfigPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func readProviderSettingsConfig(t *testing.T, rt *runtime.Session) config.Config {
	t.Helper()
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func providerSummaryNamed(t *testing.T, summaries []ProviderSummary, name string) ProviderSummary {
	t.Helper()
	for _, summary := range summaries {
		if summary.Name == name {
			return summary
		}
	}
	t.Fatalf("provider %q missing from %+v", name, summaries)
	return ProviderSummary{}
}

func requestProviderSettings(t *testing.T, srv *Server, out *lockedBuffer, id, params string) map[string]any {
	t.Helper()
	line := `{"id":"` + id + `","method":"config/model/update","params":` + params + `}`
	if err := srv.handleLine(context.Background(), []byte(line)); err != nil {
		t.Fatalf("config/model/update %s: %v", id, err)
	}
	return responseByID(t, parseOutput(t, out.String()), id)
}

const twoProviderSettingsConfig = `{
  "default_provider": "fake-provider",
  "agent": {"variant": "high"},
  "providers": {
    "fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "api_key": "old-key", "model": "fake-model"},
    "gateway": {"type": "openai-compatible", "base_url": "https://gateway.example.test/v1", "model": "gateway-model"}
  }
}
`

func TestServerKeepSelectionSavesAnotherProviderWithoutSelectingIt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, twoProviderSettingsConfig)
	defaultClient := rt.StreamRunner.Client
	out := &lockedBuffer{}
	srv := New(rt, out)
	idle := newThreadState("idle-thread", nil, "gateway", "gateway-model", rt.RootDir, false, time.Now().UTC())
	idle.execRuntime = &runtime.ThreadRuntime{StreamRunner: &agent.StreamRunner{Model: "gateway-model"}}
	srv.threads[idle.ID] = idle

	response := requestProviderSettings(t, srv, out, "key", `{"provider":"gateway","base_url":"https://edge.example.test/v1","api_key":"gateway-key","keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("keep_selection update: %v", response["error"])
	}
	result := remarshal[ConfigModelUpdateResult](t, response["result"])
	if result.Provider != "fake-provider" || result.Model != "fake-model" {
		t.Fatalf("workspace selection moved: %+v", result)
	}
	if rt.ProviderName != "fake-provider" || rt.Model != "fake-model" || rt.StreamRunner.Model != "fake-model" {
		t.Fatalf("runtime selection moved: provider=%q model=%q runner=%q", rt.ProviderName, rt.Model, rt.StreamRunner.Model)
	}
	if rt.StreamRunner.Client != defaultClient {
		t.Fatal("the default runtime adopted another provider's client")
	}
	gateway := providerSummaryNamed(t, result.Providers, "gateway")
	if gateway.BaseURL != "https://edge.example.test/v1" || !gateway.APIKeyConfigured || gateway.Model != "gateway-model" {
		t.Fatalf("gateway summary = %+v", gateway)
	}
	cfg := readProviderSettingsConfig(t, rt)
	if cfg.DefaultProvider != "fake-provider" || cfg.Agent.Variant != "high" {
		t.Fatalf("persisted selection moved: default=%q variant=%q", cfg.DefaultProvider, cfg.Agent.Variant)
	}
	if saved := cfg.Providers["gateway"]; saved.BaseURL != "https://edge.example.test/v1" || saved.Model != "gateway-model" || saved.APIKey != "" {
		t.Fatalf("gateway config = %+v", saved)
	}
	store, err := authstorage.ForHome(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	if credentials, err := store.Get("gateway"); err != nil || credentials.APIKey != "gateway-key" {
		t.Fatalf("gateway key not stored: %+v err=%v", credentials, err)
	}
	idle.mu.Lock()
	released := idle.execRuntime == nil
	idle.mu.Unlock()
	if !released {
		t.Fatal("a conversation pinned to the edited provider kept its stale client")
	}
}

func TestServerKeepSelectionCreatesProviderUnselected(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, twoProviderSettingsConfig)
	out := &lockedBuffer{}
	srv := New(rt, out)

	response := requestProviderSettings(t, srv, out, "create", `{"provider":"deepseek","model":"deepseek-v4-pro","base_url":"https://api.deepseek.com","api_key":"sk-deepseek","type":"openai-compatible","create_provider":true,"keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("create: %v", response["error"])
	}
	result := remarshal[ConfigModelUpdateResult](t, response["result"])
	if result.Provider != "fake-provider" || result.Model != "fake-model" || rt.ProviderName != "fake-provider" {
		t.Fatalf("creating a provider selected it: %+v runtime=%q", result, rt.ProviderName)
	}
	created := providerSummaryNamed(t, result.Providers, "deepseek")
	if created.Model != "deepseek-v4-pro" || !created.APIKeyConfigured {
		t.Fatalf("created summary = %+v", created)
	}
	// The endpoint matches the bundled catalog, which names the service and
	// supplies its model choices.
	if created.CatalogID != "deepseek" || created.CatalogName != "DeepSeek" || len(created.Models) < 2 {
		t.Fatalf("catalog identity = id:%q name:%q models:%d", created.CatalogID, created.CatalogName, len(created.Models))
	}
	if cfg := readProviderSettingsConfig(t, rt); cfg.DefaultProvider != "fake-provider" || cfg.Providers["deepseek"].Type != "openai-compatible" {
		t.Fatalf("persisted create = default:%q provider:%+v", cfg.DefaultProvider, cfg.Providers["deepseek"])
	}
	if unmatched := providerSummaryNamed(t, result.Providers, "gateway"); unmatched.CatalogID != "" || unmatched.CatalogName != "" {
		t.Fatalf("an unknown endpoint claimed a catalog identity: %+v", unmatched)
	}
}

// Local subscriptions use ordinary creation, without accepting endpoint
// overrides or accidentally selecting the new connection.
func TestServerCreateLocalCodexConnection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, twoProviderSettingsConfig)
	out := &lockedBuffer{}
	srv := New(rt, out)
	response := requestProviderSettings(t, srv, out, "override", `{"provider":"chatgpt","model":"gpt-6-astra","type":"openai-codex","base_url":"https://example.test","reuse_codex_credentials":true,"create_provider":true,"keep_selection":true}`)
	if response["error"] == nil {
		t.Fatal("a local OAuth connection accepted an endpoint override")
	}
	response = requestProviderSettings(t, srv, out, "create-local", `{"provider":"chatgpt","model":"gpt-6-astra","type":"openai-codex","reuse_codex_credentials":true,"create_provider":true,"keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("create local subscription: %v", response["error"])
	}
	cfg := readProviderSettingsConfig(t, rt)
	created := cfg.Providers["chatgpt"]
	if cfg.DefaultProvider != "fake-provider" || rt.ProviderName != "fake-provider" || !created.ReuseCodexCredentials || created.Type != "openai-codex" || created.BaseURL != config.Default().Providers["openai-codex"].BaseURL {
		t.Fatalf("local subscription create = default:%q provider:%+v", cfg.DefaultProvider, created)
	}
}

func TestServerKeepSelectionOnTheDefaultProviderRebuildsItsConnection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, twoProviderSettingsConfig)
	oldClient := rt.StreamRunner.Client
	out := &lockedBuffer{}
	srv := New(rt, out)

	response := requestProviderSettings(t, srv, out, "rotate", `{"provider":"fake-provider","api_key":"rotated-key","keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("rotate: %v", response["error"])
	}
	if rt.StreamRunner.Client == oldClient {
		t.Fatal("the default provider kept a client built with the old key")
	}
	if cfg := readProviderSettingsConfig(t, rt); cfg.DefaultProvider != "fake-provider" || cfg.Providers["fake-provider"].Model != "fake-model" || cfg.Agent.Variant != "high" {
		t.Fatalf("rotation changed the selection: %+v", cfg)
	}
	before, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	// The workspace model is the default provider's model, so keeping the
	// selection cannot also move it; each mixed request is refused whole.
	for id, params := range map[string]string{
		"move-model":  `{"provider":"fake-provider","model":"other-model","keep_selection":true}`,
		"variant":     `{"provider":"gateway","variant":"low","keep_selection":true}`,
		"permissions": `{"provider":"gateway","permission_mode":"auto","keep_selection":true}`,
		"thread":      `{"thread_id":"idle-thread","base_url":"https://edge.example.test/v1","keep_selection":true}`,
	} {
		if requestProviderSettings(t, srv, out, id, params)["error"] == nil {
			t.Fatalf("%s: keep_selection accepted a selection change", id)
		}
	}
	after, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a refused request changed the config")
	}
}

func TestServerAddModelRestoresRemovedAndCustomChoices(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, `{
  "default_provider": "fake-provider",
  "providers": {"fake-provider": {"type": "openai-compatible", "base_url": "https://example.test/v1", "api_key": "k", "model": "fake-model", "models": {"old": {"name": "Old model", "disabled": true}, "fake-model": {}}}}
}
`)
	out := &lockedBuffer{}
	srv := New(rt, out)
	choices := func(summary ProviderSummary) []string {
		ids := make([]string, 0, len(summary.Models))
		for _, model := range summary.Models {
			ids = append(ids, model.ID)
		}
		sort.Strings(ids)
		return ids
	}

	initial := providerSummaryNamed(t, srv.providerSummaries(), "fake-provider")
	if got := strings.Join(initial.HiddenModels, ","); got != "old" || strings.Join(choices(initial), ",") != "fake-model" {
		t.Fatalf("initial hidden=%q choices=%v", got, choices(initial))
	}

	response := requestProviderSettings(t, srv, out, "restore", `{"provider":"fake-provider","add_model":"old","keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("restore: %v", response["error"])
	}
	restored := providerSummaryNamed(t, remarshal[ConfigModelUpdateResult](t, response["result"]).Providers, "fake-provider")
	if len(restored.HiddenModels) != 0 || strings.Join(choices(restored), ",") != "fake-model,old" {
		t.Fatalf("restored hidden=%v choices=%v", restored.HiddenModels, choices(restored))
	}
	if saved := readProviderSettingsConfig(t, rt).Providers["fake-provider"].Models["old"]; saved.Disabled || saved.Name != "Old model" {
		t.Fatalf("restored model config = %+v", saved)
	}

	response = requestProviderSettings(t, srv, out, "custom", `{"provider":"fake-provider","add_model":"team-finetune","keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("custom: %v", response["error"])
	}
	custom := providerSummaryNamed(t, remarshal[ConfigModelUpdateResult](t, response["result"]).Providers, "fake-provider")
	if strings.Join(choices(custom), ",") != "fake-model,old,team-finetune" {
		t.Fatalf("custom choices=%v", choices(custom))
	}

	response = requestProviderSettings(t, srv, out, "hide", `{"provider":"fake-provider","remove_model":"team-finetune","keep_selection":true}`)
	if response["error"] != nil {
		t.Fatalf("hide: %v", response["error"])
	}
	hidden := providerSummaryNamed(t, remarshal[ConfigModelUpdateResult](t, response["result"]).Providers, "fake-provider")
	if strings.Join(hidden.HiddenModels, ",") != "team-finetune" || strings.Join(choices(hidden), ",") != "fake-model,old" {
		t.Fatalf("hidden=%v choices=%v", hidden.HiddenModels, choices(hidden))
	}
	if rt.ProviderName != "fake-provider" || rt.Model != "fake-model" {
		t.Fatalf("curating choices moved the selection: %q/%q", rt.ProviderName, rt.Model)
	}

	before, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for id, params := range map[string]string{
		"same":   `{"provider":"fake-provider","add_model":"old","remove_model":"old","keep_selection":true}`,
		"create": `{"provider":"fresh","model":"m","base_url":"https://fresh.example.test/v1","api_key":"k","create_provider":true,"add_model":"extra"}`,
		"thread": `{"thread_id":"t","add_model":"old"}`,
	} {
		if requestProviderSettings(t, srv, out, id, params)["error"] == nil {
			t.Fatalf("%s: accepted an invalid model choice change", id)
		}
	}
	after, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a refused model choice change modified the config")
	}
}

func TestServerModelCatalogProvidersListsConnectableServices(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeProviderSettingsConfig(t, rt, twoProviderSettingsConfig)
	out := &lockedBuffer{}
	srv := New(rt, out)
	request := func(id, params string) map[string]any {
		t.Helper()
		line := `{"id":"` + id + `","method":"config/model-catalog/providers","params":` + params + `}`
		if err := srv.handleLine(context.Background(), []byte(line)); err != nil {
			t.Fatalf("config/model-catalog/providers %s: %v", id, err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}

	response := request("all", `{}`)
	if response["error"] != nil {
		t.Fatalf("list: %v", response["error"])
	}
	list := remarshal[ConfigModelCatalogProvidersResult](t, response["result"])
	byID := map[string]CatalogProviderSummary{}
	for index, provider := range list.Providers {
		byID[provider.ID] = provider
		if strings.Contains(provider.BaseURL, "${") || !(strings.HasPrefix(provider.BaseURL, "https://") || strings.HasPrefix(provider.BaseURL, "http://")) {
			t.Fatalf("%s offers an endpoint Wuu cannot connect to: %q", provider.ID, provider.BaseURL)
		}
		if provider.ModelCount == 0 || provider.DefaultModel == "" || len(provider.Models) != 0 {
			t.Fatalf("%s summary = count:%d default:%q models:%d", provider.ID, provider.ModelCount, provider.DefaultModel, len(provider.Models))
		}
		if index > 0 && strings.ToLower(list.Providers[index-1].Name) > strings.ToLower(provider.Name) {
			t.Fatalf("catalog order breaks at %q after %q", provider.Name, list.Providers[index-1].Name)
		}
	}
	for id, want := range map[string]struct{ kind, baseURL string }{
		"deepseek":  {"openai-compatible", "https://api.deepseek.com"},
		"openai":    {"openai", "https://api.openai.com/v1"},
		"anthropic": {"anthropic", "https://api.anthropic.com"},
		"xai":       {"openai-compatible", "https://api.x.ai/v1"},
	} {
		if got := byID[id]; got.Type != want.kind || got.BaseURL != want.baseURL {
			t.Fatalf("%s = type:%q base:%q", id, got.Type, got.BaseURL)
		}
	}
	// Services that need a cloud account template or an SDK Wuu does not speak
	// stay out of the list instead of failing after the key is entered.
	for _, id := range []string{"amazon-bedrock", "azure", "google-vertex", "cloudflare-workers-ai"} {
		if _, listed := byID[id]; listed {
			t.Fatalf("%s is listed but cannot be connected", id)
		}
	}

	response = request("one", `{"provider":"deepseek"}`)
	if response["error"] != nil {
		t.Fatalf("one: %v", response["error"])
	}
	one := remarshal[ConfigModelCatalogProvidersResult](t, response["result"])
	if len(one.Providers) != 1 || one.Providers[0].ID != "deepseek" || len(one.Providers[0].Models) != one.Providers[0].ModelCount {
		t.Fatalf("single provider = %+v", one.Providers)
	}
	suggested := false
	for _, model := range one.Providers[0].Models {
		if model.ID == "" {
			t.Fatalf("model without id: %+v", one.Providers[0].Models)
		}
		suggested = suggested || model.ID == one.Providers[0].DefaultModel
	}
	if !suggested {
		t.Fatalf("suggested %q is not one of the provider's models", one.Providers[0].DefaultModel)
	}
	if byID["openai"].DefaultModel != "gpt-6-astra" {
		t.Fatalf("openai suggestion = %q; want the base model of the newest release, not a Pro or Fast variant", byID["openai"].DefaultModel)
	}
	for id, params := range map[string]string{
		"unconnectable": `{"provider":"amazon-bedrock"}`,
		"unknown":       `{"provider":"not-a-provider"}`,
	} {
		if request(id, params)["error"] == nil {
			t.Fatalf("%s: listed a provider that cannot be connected", id)
		}
	}
}
