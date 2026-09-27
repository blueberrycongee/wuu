package appserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
)

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
