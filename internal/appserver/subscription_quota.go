package appserver

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/providerfactory"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
	"github.com/blueberrycongee/wuu/internal/securefs"
)

const subscriptionQuotaFreshness = 5 * time.Minute

// Quota discovery is opt-in, so ordinary inventory refreshes do not make
// account requests. ACP v1 has no standard account-allowance query;
// usage_update describes context occupancy and must not be treated as quota.
func (s *Server) attachSubscriptionQuotas(engines []EngineInfo, configured []ProviderSummary) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range engines {
		engine := &engines[i]
		if !engine.Enabled || !engine.BinaryOK || engine.ID == "wuu" {
			continue
		}
		wg.Go(func() {
			var quota providers.Quota
			var err error
			switch engine.ID {
			case "codex":
				if s.rt.CodexHost() == nil {
					err = providers.NewQuotaError(providers.QuotaErrorUnsupported)
					break
				}
				var limits codexengine.RateLimitsResponse
				limits, err = s.rt.CodexHost().ReadRateLimits(ctx)
				quota.Kind = "subscription"
				quota.Plan = limits.RateLimits.PlanType
				if limits.AccountID != "" {
					quota.Account = &providers.QuotaAccount{ID: providers.QuotaAccountID("codex-cli", "account/rateLimits/read", limits.AccountID), Source: "codex-cli"}
				}
				quota.Windows = subscriptionQuotaWindows(limits)
				if err != nil {
					err = providers.NewQuotaError(providers.QuotaErrorNetwork)
				} else if len(quota.Windows) == 0 {
					err = providers.NewQuotaError(providers.QuotaErrorUnsupported)
				}
			case "claude":
				quota, err = anthropic.ReadLocalQuota(ctx, os.Getenv("HOME"))
			case "grok":
				client, buildErr := grokbuild.New(grokbuild.ClientConfig{Home: os.Getenv("HOME"), ReuseGrokCredentials: true})
				if buildErr != nil {
					err = providers.NewQuotaError(providers.QuotaErrorSignIn)
				} else {
					quota, err = client.ReadQuota(ctx)
				}
			default:
				err = providers.NewQuotaError(providers.QuotaErrorUnsupported)
			}
			engine.Quota = s.subscriptionQuotaSnapshot(quota, err)
		})
	}
	cfg, _, configErr := s.rt.LoadEffectiveConfig()
	for i := range configured {
		service := &configured[i]
		wg.Go(func() {
			if configErr != nil {
				service.Quota = s.subscriptionQuotaSnapshot(providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorInvalidResponse))
				return
			}
			provider, exists := cfg.Providers[service.Name]
			if !exists {
				provider, exists = localGrokBuildProvider(cfg, service.Name, os.Getenv("HOME"))
			}
			if !exists {
				service.Quota = s.subscriptionQuotaSnapshot(providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported))
				return
			}
			client, err := providerfactory.BuildClient(provider, service.Name)
			if err != nil {
				service.Quota = s.subscriptionQuotaSnapshot(providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorSignIn))
				return
			}
			reader, supported := client.(providers.QuotaReader)
			if !supported {
				service.Quota = s.subscriptionQuotaSnapshot(providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported))
				return
			}
			quota, err := reader.ReadQuota(ctx)
			service.Quota = s.subscriptionQuotaSnapshot(quota, err)
		})
	}
	wg.Wait()
}

// Snapshots are stored per credential-scoped identity rather than connection
// name, so renaming a service cannot move an allowance to another account.
func (s *Server) subscriptionQuotaSnapshot(quota providers.Quota, readErr error) *SubscriptionQuota {
	now := time.Now().UTC()
	quota.CheckedAt = now.Format(time.RFC3339Nano)
	quota.Status = "available"
	quota.ErrorCode = ""
	path := ""
	if quota.Account != nil && quota.Account.ID != "" {
		path = filepath.Join(s.rt.WuuHome, "quota-snapshots", providers.QuotaAccountID("snapshot", "", quota.Account.ID)+".json")
	}
	if readErr != nil {
		quota.ErrorCode = providers.QuotaErrorCode(readErr)
		quota.Status = "unavailable"
		switch quota.ErrorCode {
		case providers.QuotaErrorSignIn:
			quota.Status = "sign_in"
			if path != "" {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					providers.DebugLogf("quota snapshot removal: %v", err)
				}
			}
		case providers.QuotaErrorUnsupported:
			quota.Status = "unsupported"
		default:
			if path != "" {
				data, err := os.ReadFile(path)
				var previous providers.Quota
				if err == nil && json.Unmarshal(data, &previous) == nil && previous.Status == "available" && previous.Account != nil && previous.Account.ID == quota.Account.ID {
					previous.Status, previous.ErrorCode, previous.CheckedAt = "stale", quota.ErrorCode, quota.CheckedAt
					return &previous
				}
			}
		}
		quota.Windows, quota.Balances = nil, nil
		return &quota
	}
	quota.ObservedAt = quota.CheckedAt
	quota.ExpiresAt = now.Add(subscriptionQuotaFreshness).Format(time.RFC3339Nano)
	for i := range quota.Windows {
		window := &quota.Windows[i]
		if window.UsedPercent != nil && (math.IsNaN(*window.UsedPercent) || math.IsInf(*window.UsedPercent, 0) || *window.UsedPercent < 0) {
			window.UsedPercent = nil
		}
	}
	if path != "" {
		data, err := json.Marshal(quota)
		if err == nil {
			err = securefs.WriteFileAtomic(path, data)
		}
		if err != nil {
			providers.DebugLogf("quota snapshot persistence: %v", err)
		}
	}
	return &quota
}

func subscriptionQuotaWindows(limits codexengine.RateLimitsResponse) []SubscriptionQuotaWindow {
	var windows []SubscriptionQuotaWindow
	appendWindow := func(id, label string, window *codexengine.RateLimitWindow) {
		if window == nil {
			return
		}
		result := SubscriptionQuotaWindow{ID: id, Label: label, UsedPercent: window.UsedPercent}
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
