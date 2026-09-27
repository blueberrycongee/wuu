package appserver

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/codexengine"
)

// Quota discovery is opt-in, so ordinary inventory refreshes do not make
// account requests. ACP v1 has no standard account-allowance query;
// usage_update describes context occupancy and must not be treated as quota.
func (s *Server) attachSubscriptionQuotas(engines []EngineInfo) {
	for i := range engines {
		engine := &engines[i]
		if engine.ID != "codex" || !engine.Enabled || !engine.BinaryOK || s.rt.CodexHost() == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		limits, err := s.rt.CodexHost().ReadRateLimits(ctx)
		cancel()
		quota := &SubscriptionQuota{Status: "unavailable", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
		if err == nil {
			quota.Windows = subscriptionQuotaWindows(limits)
			if len(quota.Windows) > 0 {
				quota.Status = "available"
			}
		}
		engine.Quota = quota
	}
}

func subscriptionQuotaWindows(limits codexengine.RateLimitsResponse) []SubscriptionQuotaWindow {
	var windows []SubscriptionQuotaWindow
	appendWindow := func(id, label string, window *codexengine.RateLimitWindow) {
		if window == nil || window.UsedPercent == nil || math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) || *window.UsedPercent < 0 {
			return
		}
		result := SubscriptionQuotaWindow{ID: id, Label: label, UsedPercent: *window.UsedPercent}
		if window.WindowMinutes != nil && *window.WindowMinutes > 0 {
			result.WindowMinutes = *window.WindowMinutes
		}
		if window.ResetsAt != nil && *window.ResetsAt > 0 {
			result.ResetsAt = time.Unix(*window.ResetsAt, 0).UTC().Format(time.RFC3339)
		}
		windows = append(windows, result)
	}
	appendSnapshot := func(id string, snapshot codexengine.RateLimitSnapshot) {
		appendWindow(id+":primary", snapshot.LimitName, snapshot.Primary)
		appendWindow(id+":secondary", snapshot.LimitName, snapshot.Secondary)
	}
	if len(limits.ByLimitID) == 0 {
		appendSnapshot("codex", limits.RateLimits)
	} else {
		ids := make([]string, 0, len(limits.ByLimitID))
		for id := range limits.ByLimitID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			snapshot := limits.ByLimitID[id]
			if snapshot.LimitName == "" && len(ids) > 1 {
				snapshot.LimitName = id
			}
			appendSnapshot(id, snapshot)
		}
	}
	return windows
}
