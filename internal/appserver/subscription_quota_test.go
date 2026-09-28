package appserver

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Navigation must not request historical statistics. The dashboard opts in,
// and a broken history scan must still leave its service inventory usable.
func TestSubscriptionHistoryIsLoadedOnlyOnDemand(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	if err := os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"grok","providers":{"grok":{"type":"grok-build","model":"grok-4.5"},"codex":{"type":"openai-codex","model":"gpt-5","reuse_codex_credentials":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"grok", "codex", "external"} {
		sess, err := session.CreateWithMetadata(rt.SessionDir, source, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if source == "external" {
			if _, err := session.SetEngine(rt.SessionDir, sess.ID, "codex"); err != nil {
				t.Fatal(err)
			}
		}
		for _, record := range []session.HistoryRecord{
			{Role: "meta", Content: "token_usage", Provider: source, InputTokens: 12},
			{Role: "meta", Content: "turn_terminal", Provider: source, StopReason: "completed", InputTokens: 12},
		} {
			if err := session.AppendHistoryRecord(rt.SessionDir, sess.ID, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	initialize := func(id string) {
		t.Helper()
		if err := srv.handleInitialize(Request{ID: json.RawMessage(`"` + id + `"`), Params: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("initialize: %+v", response)
		}
		for _, raw := range response["result"].(map[string]any)["providers"].([]any) {
			provider := raw.(map[string]any)
			if provider["latest_request"] != nil || provider["local_usage"] != nil {
				t.Fatalf("navigation loaded historical statistics: %+v", provider)
			}
		}
	}
	list := func(id string, include bool) EngineListResult {
		t.Helper()
		if err := srv.handleEngineList(Request{ID: json.RawMessage(`"` + id + `"`), Params: json.RawMessage(fmt.Sprintf(`{"include_quota":%t}`, include))}); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("engine/list: %+v", response)
		}
		data, err := json.Marshal(response["result"])
		if err != nil {
			t.Fatal(err)
		}
		var result EngineListResult
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	initialize("before")
	for _, engine := range list("inventory", false).Engines {
		if engine.LatestRequest != nil || engine.LocalUsage != nil {
			t.Fatalf("ordinary inventory loaded historical statistics: %+v", engine)
		}
	}
	dashboard := list("dashboard", true)
	if len(dashboard.SubscriptionProviders) != 2 {
		t.Fatalf("subscription providers: %+v", dashboard.SubscriptionProviders)
	}
	for _, provider := range dashboard.SubscriptionProviders {
		if provider.LatestRequest == nil || provider.LatestRequest.Status != "completed" || provider.LocalUsage == nil || provider.LocalUsage.InputTokens != 12 {
			t.Fatalf("missing provider history: %+v", provider)
		}
	}
	for _, engine := range dashboard.Engines {
		if engine.ID == "codex" && (engine.LatestRequest == nil || engine.LocalUsage == nil || engine.LocalUsage.InputTokens != 12) {
			t.Fatalf("missing external engine history: %+v", engine)
		}
	}
	// A dashboard snapshot must not contaminate the cached config summaries.
	initialize("after")
	db, err := sql.Open("sqlite", filepath.Join(rt.SessionDir, "sessions.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE session_messages SET input_tokens = 'invalid'`); err != nil {
		t.Fatal(err)
	}
	failed := list("failed-history", true)
	if len(failed.SubscriptionProviders) != 2 {
		t.Fatal("failed history scan removed service inventory")
	}
	for _, provider := range failed.SubscriptionProviders {
		if provider.LatestRequest != nil || provider.LocalUsage != nil {
			t.Fatalf("failed history invented statistics: %+v", provider)
		}
	}
	initialize("failed-history-navigation")
}

func TestSubscriptionQuotaPreservesExhaustionAndRejectsMissingPercent(t *testing.T) {
	var response codexengine.RateLimitsResponse
	if err := json.Unmarshal([]byte(`{"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":300},"secondary":{"windowDurationMins":10080}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":105,"windowDurationMins":300,"resetsAt":1800000000},"secondary":{"usedPercent":0,"windowDurationMins":10080}}}}`), &response); err != nil {
		t.Fatal(err)
	}
	got := subscriptionQuotaWindows(response)
	if len(got) != 2 || got[0].UsedPercent != 105 || got[1].UsedPercent != 0 || got[0].ResetsAt == "" {
		t.Fatalf("quota windows = %+v", got)
	}
	// The map is authoritative; the legacy mirror must not be counted twice.
	response.ByLimitID = nil
	got = subscriptionQuotaWindows(response)
	if len(got) != 1 || got[0].UsedPercent != 0 || got[0].WindowMinutes != 300 {
		t.Fatalf("legacy windows = %+v", got)
	}
	response.RateLimits.Primary.UsedPercent = nil
	if got := subscriptionQuotaWindows(response); len(got) != 0 {
		t.Fatalf("missing allowance became quota: %+v", got)
	}
}

// Subscription clients reserve allowance space from engine/list capabilities
// before the slower include_quota read, so every engine that receives quota
// must advertise it.
func TestSubscriptionQuotaGoesOnlyToEnginesAdvertisingIt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	// A binary the host cannot start fails the allowance read, which still
	// attaches an unavailable quota without running an app-server.
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"test-key","model":"fake-model"}},"engines":{"codex":{"binary_path":%q}}}`, binary)
	if err := os.WriteFile(rt.ConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(agentengine.NewRegistry())
	rt.RebuildCodexEngine(true, binary)
	srv := &Server{rt: rt}

	engines := srv.engineInventory()
	srv.attachSubscriptionQuotas(engines)
	quoted := 0
	for _, engine := range engines {
		if engine.Quota == nil {
			continue
		}
		quoted++
		if !slices.Contains(engine.Capabilities, "account-quota") {
			t.Fatalf("%s received quota without advertising account-quota: %v", engine.ID, engine.Capabilities)
		}
	}
	if quoted == 0 {
		t.Fatal("no engine received quota")
	}
}
