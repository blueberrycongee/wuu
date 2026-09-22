package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/insight"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestAutoDecisionPersistsAndRestoresWithoutReclassification(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
			Tools []any  `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model == "gpt-4o" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Executed by selected model\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		calls.Add(1)
		if request.Model != "gpt-4o-mini" || len(request.Tools) > 0 {
			t.Errorf("unexpected classifier request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"classifier","choices":[{"message":{"role":"assistant","content":"{\"tier\":\"complex\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5}}`)
	}))
	defer upstream.Close()
	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Default()
	cfg.DefaultProvider = "route"
	cfg.Providers = map[string]config.ProviderConfig{"route": {Type: "openai-compatible", WireAPI: "chat", BaseURL: upstream.URL, APIKey: "test", Model: "gpt-4o-mini"}, "execution": {Type: "openai-compatible", WireAPI: "chat", BaseURL: upstream.URL, APIKey: "test", Model: "gpt-4o"}}
	cfg.Agent.AutoModel = &config.AutoModelConfig{Enabled: true, Classifier: config.ModelRoleConfig{Provider: "route", Model: "gpt-4o-mini"}, Simple: config.ModelRoleConfig{Provider: "route", Model: "gpt-4o-mini"}, Medium: config.ModelRoleConfig{Provider: "execution", Model: "gpt-4o"}, Complex: config.ModelRoleConfig{Provider: "execution", Model: "gpt-4o", Effort: "high"}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(rt.SessionDir, "auto-thread"); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	th := newThreadState("auto-thread", nil, "route", config.AutoModelID, rt.RootDir, true, time.Now())
	th.Turns = []Turn{{ID: "turn-one", Kind: TurnKindUser, Status: TurnStatusInProgress}}
	execution := &runtime.ThreadRuntime{StreamRunner: &agent.StreamRunner{}, Selection: runtime.ThreadModelSelection{Provider: "route", Model: config.AutoModelID}}
	history := []providers.ChatMessage{{Role: "user", Content: "Refactor the storage protocol"}}
	first, err := srv.prepareAutoModel(context.Background(), th, execution, "turn-one", history, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Tier != "complex" || execution.StreamRunner.ProviderName != "execution" || execution.StreamRunner.Model != "gpt-4o" {
		t.Fatalf("wrong execution: %+v", first)
	}
	if th.Model != config.AutoModelID || th.Turns[0].Model != "gpt-4o" {
		t.Fatal("Auto intent or actual turn model lost")
	}
	result, err := execution.StreamRunner.RunWithCallback(context.Background(), history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "Executed by selected model" {
		t.Fatalf("selected model did not execute: %+v", result)
	}
	// A fresh runtime after restart reuses the durable decision, even if a
	// classifier would now return a different result.
	th.Turns[0].AutoModel = nil
	restored := &runtime.ThreadRuntime{StreamRunner: &agent.StreamRunner{}}
	second, err := srv.prepareAutoModel(context.Background(), th, restored, "turn-one", history, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || second.Selection != first.Selection {
		t.Fatalf("reclassified after restore: %d", calls.Load())
	}
	rows, err := insight.CollectTokenUsageRows(rt.SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].InputTokens != 20 || rows[0].Provider != "route" {
		t.Fatalf("classifier accounting: %+v", rows)
	}
}

func TestAutoSettingsCanChangeWhileTaskRuns(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	cfg := config.Default()
	cfg.DefaultProvider = "fake-provider"
	cfg.Providers = map[string]config.ProviderConfig{"fake-provider": {Type: "openai-compatible", BaseURL: "https://example.invalid", Model: "fake-model"}}
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
	policy := config.AutoModelConfig{Enabled: true, Default: true, Classifier: selection, Simple: selection, Medium: selection, Complex: selection}
	params, _ := json.Marshal(ConfigAdvancedUpdateParams{AutoModel: &policy})
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
	if srv.currentSessionRuntimeSelection().Model != config.AutoModelID {
		t.Fatal("default Auto not applied to new sessions")
	}
}
