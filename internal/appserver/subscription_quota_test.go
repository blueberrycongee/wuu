package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	configpkg "github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Quota refreshes must remain opt-in, keep accounts isolated, retain successful
// observations across restarts, and stop showing allowances on rejected auth.
func TestProviderQuotaAttributionAndRecovery(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	root, home := t.TempDir(), t.TempDir()
	rt := &runtime.Session{RootDir: root, WuuHome: home, ConfigPath: filepath.Join(root, "config.json")}
	openRuntime := func() *runtime.Session {
		raw, err := os.ReadFile(rt.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		var cfg configpkg.Config
		if err = json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		current, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: home, ConfigPath: rt.ConfigPath, ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg, NoTools: true, NonInteractive: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = current.Cleanup() })
		return current
	}
	var requests atomic.Int32
	var status atomic.Int32
	status.Store(http.StatusOK)
	token := func(account, email string) string {
		claims, _ := json.Marshal(map[string]any{"email": email, "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
		return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	}
	firstToken, secondToken := token("account-a", "first@example.test"), token("account-b", "second@example.test")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/backend-api/wham/usage" {
			t.Errorf("quota refresh submitted a different request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") == "Bearer "+secondToken {
			fmt.Fprint(w, `{"plan_type":"plus","rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`)
			return
		}
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":37,"limit_window_seconds":18000,"reset_at":1800000000},"secondary_window":{"used_percent":125,"limit_window_seconds":604800}}}`)
	}))
	defer upstream.Close()
	writeConfig := func(first string) {
		t.Helper()
		cfg := map[string]any{"default_provider": "fake-provider", "providers": map[string]any{
			"fake-provider": map[string]string{"type": "openai-compatible", "base_url": "https://example.test/v1", "api_key": "fixture", "model": "fake-model"},
			"first":         map[string]string{"type": "openai-codex", "base_url": upstream.URL + "/backend-api/codex", "api_key": first, "model": "gpt-5"},
			"second":        map[string]string{"type": "openai-codex", "base_url": upstream.URL + "/backend-api/codex", "api_key": secondToken, "model": "gpt-5"},
		}}
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(rt.ConfigPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if rt.PluginHost != nil {
			var selected configpkg.Config
			if err := json.Unmarshal(data, &selected); err != nil {
				t.Fatal(err)
			}
			candidate, err := rt.PreflightExtensions(selected)
			if err != nil {
				t.Fatal(err)
			}
			if err = rt.ActivatePluginGeneration(candidate, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeConfig(firstToken)
	rt = openRuntime()
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	list := func(id string, include bool) EngineListResult {
		t.Helper()
		if err := srv.handleEngineList(Request{ID: json.RawMessage(`"` + id + `"`), Params: json.RawMessage(fmt.Sprintf(`{"include_quota":%t}`, include))}); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("engine/list: %+v", response)
		}
		data, _ := json.Marshal(response["result"])
		var result EngineListResult
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	quota := func(result EngineListResult, name string) *SubscriptionQuota {
		t.Helper()
		for _, provider := range result.SubscriptionProviders {
			if provider.Name == name && provider.Quota != nil {
				return provider.Quota
			}
		}
		t.Fatalf("missing quota for %s: %+v", name, result)
		return nil
	}
	list("ordinary", false)
	if requests.Load() != 0 {
		t.Fatal("navigation requested account allowances")
	}
	state, stateErr := rt.RetainState()
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	_, quotaErr := state.ReadModelQuota(context.Background(), "first")
	_ = state.Close()
	if quotaErr != nil {
		t.Fatalf("selected quota route: %v", quotaErr)
	}
	fresh := list("fresh", true)
	first, second := quota(fresh, "first"), quota(fresh, "second")
	if first.Status != "available" || first.Account == nil || first.Account.Label != "first@example.test" || first.ObservedAt == "" || first.ExpiresAt == "" || len(first.Windows) != 2 || first.Windows[0].UsedPercent == nil || *first.Windows[0].UsedPercent != 37 || *first.Windows[1].UsedPercent != 125 {
		t.Fatalf("fresh first account: %+v", first)
	}
	if second.Account == nil || first.Account.ID == second.Account.ID || second.Account.Label != "second@example.test" || len(second.Windows) != 1 || second.Windows[0].UsedPercent != nil {
		t.Fatalf("account attribution or unknown allowance lost: %+v", second)
	}
	if quota(fresh, "fake-provider").Status != "unsupported" {
		t.Fatal("unsupported provider received fabricated quota")
	}
	status.Store(http.StatusServiceUnavailable)
	stale := quota(list("stale", true), "first")
	if stale.Status != "stale" || stale.ObservedAt != first.ObservedAt || stale.ExpiresAt != first.ExpiresAt || *stale.Windows[0].UsedPercent != 37 || stale.ErrorCode == "" {
		t.Fatalf("last-known observation was refilled or relabelled: %+v", stale)
	}
	// Restart with a new runtime after the old server's owned watchers stop.
	// Only persisted configuration and credential-scoped observations survive.
	srv.Close()
	_, _ = rt.Cleanup()
	rt = openRuntime()
	out = &lockedBuffer{}
	srv = New(rt, out)
	t.Cleanup(srv.Close)
	if restarted := quota(list("restart", true), "first"); restarted.Status != "stale" || restarted.ObservedAt != first.ObservedAt {
		t.Fatalf("restart lost observation: %+v", restarted)
	}
	writeConfig(token("account-c", "third@example.test"))
	if changed := quota(list("account-change", true), "first"); changed.Status != "unavailable" || len(changed.Windows) != 0 || changed.Account.ID == first.Account.ID {
		t.Fatalf("another account inherited quota: %+v", changed)
	}
	writeConfig(firstToken)
	status.Store(http.StatusUnauthorized)
	if rejected := quota(list("rejected", true), "first"); rejected.Status != "sign_in" || len(rejected.Windows) != 0 {
		t.Fatalf("rejected credential retained usable quota: %+v", rejected)
	}
	if strings.Contains(out.String(), firstToken) || strings.Contains(out.String(), secondToken) {
		t.Fatal("quota protocol exposed credentials")
	}
}

// Navigation must not request historical statistics. The dashboard opts in,
// and a broken history scan must still leave its service inventory usable.
func TestSubscriptionHistoryIsLoadedOnlyOnDemand(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	t.Setenv("WUU_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	if err := os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"grok","providers":{"grok":{"type":"grok-build","model":"grok-4.5"},"codex":{"type":"openai-codex","model":"gpt-5","reuse_codex_credentials":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bindConfigSourceForTest(t, rt)
	documents := &subscriptionHistoryDocuments{Documents: rt.Documents}
	rt.Documents = documents
	rt.Sessions = session.NewStore(context.Background(), documents)
	for _, source := range []string{"grok", "codex", "external"} {
		sess, err := rt.Sessions.CreateWithMetadata(source, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if source == "external" {
			if _, err := rt.Sessions.SetEngine(sess.ID, "codex"); err != nil {
				t.Fatal(err)
			}
		}
		for _, record := range []session.HistoryRecord{
			{Role: "meta", Content: "token_usage", Provider: source, InputTokens: 12},
			{Role: "meta", Content: "turn_terminal", Provider: source, StopReason: "completed", InputTokens: 12},
		} {
			if err := rt.Sessions.AppendHistoryRecord(sess.ID, record); err != nil {
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
		providers, ok := response["result"].(map[string]any)["providers"].([]any)
		if !ok || len(providers) != 2 {
			t.Fatalf("initialize inventory: %+v", response)
		}
		for _, raw := range providers {
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
	documents.fail = true
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

func TestSubscriptionQuotaGoesOnlyToEnginesAdvertisingIt(t *testing.T) {
	// A binary the host cannot start fails the allowance read, which still
	// attaches an unavailable quota without running an app-server.
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","api_key":"test-key","model":"fake-model"}},"engines":{"codex":{"binary_path":%q}}}`, binary)
	var cfg configpkg.Config
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		t.Fatal(err)
	}
	rt := newEngineAdminRuntime(t, cfg)
	srv := &Server{rt: rt}

	engines := srv.engineInventory()
	srv.attachSubscriptionQuotas(engines, nil)
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
		t.Fatalf("no engine received quota: %+v", engines)
	}
}

// Fault injection stays at the selected document boundary used by history.
type subscriptionHistoryDocuments struct {
	persistence.Documents
	fail bool
}

func (d *subscriptionHistoryDocuments) Read(ctx context.Context, query persistence.Query) (persistence.View, error) {
	if d.fail {
		return persistence.View{}, errors.New("history provider unavailable")
	}
	return d.Documents.Read(ctx, query)
}
