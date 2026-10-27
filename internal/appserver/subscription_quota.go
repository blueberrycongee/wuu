package appserver

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"

	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

const subscriptionQuotaFreshness = 5 * time.Minute

// Quota discovery is opt-in, so ordinary inventory refreshes do not make
// account requests. ACP v1 has no standard account-allowance query;
// usage_update describes context occupancy and must not be treated as quota.
func (s *Server) attachSubscriptionQuotas(engines []EngineInfo, configured []ProviderSummary) {
	state, err := s.rt.RetainState()
	if err != nil {
		return
	}
	defer state.Close()
	s.attachSubscriptionQuotasForState(state, engines, configured)
}

func (s *Server) attachSubscriptionQuotasForState(state *runtime.State, engines []EngineInfo, configured []ProviderSummary) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	documents := state.Documents
	services, serviceErr := state.Services()
	var cfg config.Config
	configErr := serviceErr
	if configErr == nil {
		view, err := services.Config.Read(ctx)
		cfg, configErr = view.Config, err
	}
	var wg sync.WaitGroup
	for i := range engines {
		engine := &engines[i]
		if !engine.Enabled || !engine.BinaryOK || !slices.Contains(engine.Capabilities, "account-quota") {
			continue
		}
		wg.Go(func() {
			var result pluginprotocol.EngineQuotaResult
			err := state.CallEngine(ctx, agentengine.EngineID(engine.ID), "quota", struct{}{}, &result)
			var quota providers.Quota
			if err == nil {
				err = json.Unmarshal(result.Quota, &quota)
			}
			if err == nil && result.ErrorCode != "" {
				err = providers.NewQuotaError(result.ErrorCode)
			}

			engine.Quota = s.subscriptionQuotaSnapshot(ctx, documents, quota, err)
		})
	}
	for i := range configured {
		service := &configured[i]
		wg.Go(func() {
			if configErr != nil {
				service.Quota = s.subscriptionQuotaSnapshot(ctx, documents, providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorInvalidResponse))
				return
			}
			_, exists := cfg.Providers[service.Name]
			if !exists {
				_, exists = selectedGrokBuildProvider(cfg, service.Name, services.Credentials)
			}
			if !exists {
				service.Quota = s.subscriptionQuotaSnapshot(ctx, documents, providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported))
				return
			}
			quota, err := state.ReadModelQuota(ctx, service.Name)
			service.Quota = s.subscriptionQuotaSnapshot(ctx, documents, quota, err)
		})
	}
	wg.Wait()
}

// Snapshots are stored per credential-scoped identity rather than connection
// name, so renaming a service cannot move an allowance to another account.
func (s *Server) subscriptionQuotaSnapshot(ctx context.Context, documents persistence.Documents, quota providers.Quota, readErr error) *SubscriptionQuota {
	now := time.Now().UTC()
	quota.CheckedAt = now.Format(time.RFC3339Nano)
	quota.Status = "available"
	quota.ErrorCode = ""
	key := ""
	if quota.Account != nil && quota.Account.ID != "" {
		key = providers.QuotaAccountID("snapshot", "", quota.Account.ID)
	}
	var previous providers.Quota
	if key != "" {
		err := persistence.AccessDocumentImage(ctx, documents, "subscription.quota", key, func(image *persistence.DocumentImage) error {
			// Legacy files are consulted only before a durable image exists. An empty
			// image is a tombstone, preventing old allowances returning after sign-out.
			data, err := os.ReadFile(filepath.Join(s.rt.WuuHome, "quota-snapshots", key+".json"))
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			var legacy providers.Quota
			if err = json.Unmarshal(data, &legacy); err != nil {
				return err
			}
			if legacy.Account != nil && legacy.Account.ID == quota.Account.ID {
				return image.Put("snapshot", legacy)
			}
			return nil
		}, func(image *persistence.DocumentImage) error {
			if err := image.Get("snapshot", &previous); err != nil {
				return err
			}
			if readErr != nil && providers.QuotaErrorCode(readErr) == providers.QuotaErrorSignIn {
				return image.Put("snapshot", nil)
			}
			return nil
		})
		if err != nil {
			providers.DebugLogf("quota snapshot read: %v", err)
			previous = providers.Quota{}
		}
	}
	if readErr != nil {
		quota.ErrorCode = providers.QuotaErrorCode(readErr)
		quota.Status = "unavailable"
		switch quota.ErrorCode {
		case providers.QuotaErrorSignIn:
			quota.Status = "sign_in"

		case providers.QuotaErrorUnsupported:
			quota.Status = "unsupported"
		default:
			if previous.Status == "available" && previous.Account != nil && quota.Account != nil && previous.Account.ID == quota.Account.ID {
				previous.Status, previous.ErrorCode, previous.CheckedAt = "stale", quota.ErrorCode, quota.CheckedAt
				return &previous
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
	if key != "" {
		err := persistence.AccessDocumentImage(ctx, documents, "subscription.quota", key, nil, func(image *persistence.DocumentImage) error { return image.Put("snapshot", quota) })
		if err != nil {
			providers.DebugLogf("quota snapshot persistence: %v", err)
		}
	}

	return &quota
}
