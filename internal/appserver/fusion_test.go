package appserver

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestFusionPairPersistsAcrossTurnsAndRestart(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	cfg := config.Default()
	cfg.DefaultProvider = "lead"
	cfg.Providers = map[string]config.ProviderConfig{
		"lead":     {Type: "openai-compatible", BaseURL: "https://example.invalid", APIKey: "test", Model: "gpt-4o"},
		"sidekick": {Type: "openai-compatible", BaseURL: "https://example.invalid", APIKey: "test", Model: "gpt-4o-mini"},
	}
	pair := config.FusionSelection{Lead: config.ModelRoleConfig{Provider: "lead", Model: "gpt-4o"}, Sidekick: config.ModelRoleConfig{Provider: "sidekick", Model: "gpt-4o-mini"}}
	cfg.Agent.Fusion = &config.FusionConfig{Enabled: true, Lead: pair.Lead, Sidekick: pair.Sidekick}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(rt.SessionDir, "fusion-thread"); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	th := newThreadState("fusion-thread", nil, "lead", config.FusionID, rt.RootDir, true, time.Now())
	th.Turns = []Turn{{ID: "turn-one", Kind: TurnKindUser, Status: TurnStatusInProgress}}
	execution, err := rt.NewThreadRuntime(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.AgentControl.Close()
	first, err := srv.prepareFusion(context.Background(), th, execution, "turn-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	if *first != pair || execution.StreamRunner.ProviderName != "lead" || execution.StreamRunner.Model != "gpt-4o" {
		t.Fatalf("wrong binding: %+v", first)
	}
	if th.Model != config.FusionID || th.Turns[0].Model != "gpt-4o" || !execution.Toolkit.SupportsTool("fusion_delegate") {
		t.Fatal("Fusion selection or tools lost")
	}
	// Disabling Fusion and changing defaults must not retarget an existing pair.
	cfg.Agent.Fusion.Enabled = false
	cfg.Agent.Fusion.Lead, cfg.Agent.Fusion.Sidekick = pair.Sidekick, pair.Lead
	th.execRuntime = execution
	srv.threads[th.ID] = th
	srv.updateIdleThreadAdvancedRuntime(cfg)
	worker := execution.AgentControl.Manager().DefaultWorkerRuntime()
	if worker.Provider != pair.Sidekick.Provider || worker.Model != pair.Sidekick.Model {
		t.Fatalf("global update retargeted Sidekick: %+v", worker)
	}
	data, _ = json.Marshal(cfg)
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	th.Turns = []Turn{{ID: "turn-two", Kind: TurnKindUser, Status: TurnStatusInProgress}}
	restored, err := rt.NewThreadRuntime(th.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.AgentControl.Close()
	second, err := srv.prepareFusion(context.Background(), th, restored, "turn-two", nil)
	if err != nil {
		t.Fatal(err)
	}
	if *second != pair || restored.StreamRunner.Model != pair.Lead.Model {
		t.Fatalf("pair changed after restore: %+v", second)
	}
}

func TestFusionSettingsCanChangeWhileTaskRuns(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Default()
	cfg.DefaultProvider = "fake-provider"
	cfg.Providers = map[string]config.ProviderConfig{"fake-provider": {Type: "openai-compatible", BaseURL: "https://example.invalid", APIKey: "test", Model: "fake-model"}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	th := newThreadState("running", nil, "fake-provider", "fake-model", rt.RootDir, false, time.Now())
	th.running = true
	srv.threads[th.ID] = th
	selection := config.ModelRoleConfig{Provider: "fake-provider", Model: "fake-model"}
	policy := config.FusionConfig{Enabled: true, Default: true, Lead: selection, Sidekick: selection}
	params, _ := json.Marshal(ConfigAdvancedUpdateParams{Fusion: &policy})
	if err := srv.handleConfigAdvancedUpdate(Request{ID: json.RawMessage(`"save"`), Params: params}); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "save")
	if response["error"] != nil {
		t.Fatalf("save: %+v", response)
	}
	if th.Model != "fake-model" || rt.StreamRunner.Model != "fake-model" {
		t.Fatal("live selection changed")
	}
	if srv.currentSessionRuntimeSelection().Model != config.FusionID {
		t.Fatal("default Fusion not applied to new sessions")
	}
}
