package runtime

import (
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/codexengine"
)

func TestSubscriptionQuotaPreservesExhaustionAndRejectsMissingPercent(t *testing.T) {
	var response codexengine.RateLimitsResponse
	if err := json.Unmarshal([]byte(`{"rateLimits":{"primary":{"usedPercent":0,"windowDurationMins":300},"secondary":{"windowDurationMins":10080}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":105,"windowDurationMins":300,"resetsAt":1800000000},"secondary":{"usedPercent":0,"windowDurationMins":10080}}}}`), &response); err != nil {
		t.Fatal(err)
	}
	got := subscriptionQuotaWindows(response)
	if len(got) != 2 || got[0].UsedPercent == nil || *got[0].UsedPercent != 105 || got[1].UsedPercent == nil || *got[1].UsedPercent != 0 || got[0].ResetsAt == "" {
		t.Fatalf("quota windows = %+v", got)
	}
	// The map is authoritative; the legacy mirror must not be counted twice.
	response.ByLimitID = nil
	got = subscriptionQuotaWindows(response)
	if len(got) != 2 || got[0].UsedPercent == nil || *got[0].UsedPercent != 0 || got[0].WindowMinutes != 300 || got[1].UsedPercent != nil {
		t.Fatalf("legacy windows = %+v", got)
	}
	response.RateLimits.Primary.UsedPercent = nil
	if got := subscriptionQuotaWindows(response); len(got) != 2 || got[0].UsedPercent != nil || got[1].UsedPercent != nil {
		t.Fatalf("missing allowance became quota: %+v", got)
	}
}

// Subscription clients reserve allowance space from engine/list capabilities
// before the slower include_quota read, so every engine that receives quota
// must advertise it.
