package appserver

import (
	"context"
	"errors"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"strings"
	"sync"
	"time"
)

// handleEngineList reports the engine inventory and the persisted engine
// settings for the settings UI.
func (s *Server) handleEngineList(req Request) error {
	binding, stateErr := s.rt.RetainState()
	if stateErr != nil {
		return s.writeResponse(req.ID, nil, stateErr)
	}
	defer binding.Close()

	var params struct {
		IncludeQuota  bool   `json:"include_quota"`
		RefreshModels bool   `json:"refresh_models"`
		CWD           string `json:"cwd"`
	}
	if len(req.Params) > 0 {
		if err := decodeParams(req.Params, &params); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	if params.RefreshModels {
		enginecatalog.RefreshUserPath()
		// Discovery alone can find a newly installed binary while the runtime
		// still has no factory for it. Reapply persisted enable/disable choices
		// before reporting availability; existing turns retain their leases.
		if err := s.rt.RefreshEngineSettings(); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		s.releaseEngineScopes()
	}
	result := EngineListResult{
		Engines:  s.engineInventoryForState(binding, firstNonEmpty(params.CWD, s.rt.RootDir), params.RefreshModels),
		Settings: s.engineSettingsFromConfig(),
	}
	if params.IncludeQuota {
		result.SubscriptionProviders = append([]ProviderSummary(nil), s.providerSummaries()...)
		// Historical statistics belong to the opt-in dashboard request, not
		// navigation or the composer inventory. Copy providers before enriching
		// them so cached configuration summaries remain history-free.
		s.attachSubscriptionActivity(binding, result.Engines, result.SubscriptionProviders)
		s.attachSubscriptionQuotasForState(binding, result.Engines, result.SubscriptionProviders)
	}
	return s.writeResponse(req.ID, result, nil)
}

// handleEngineUpdate persists engine settings and applies them to the live
// runtime (enable/disable engines, binary path overrides, default engine).
func (s *Server) handleEngineUpdate(req Request) error {
	var params EngineUpdateParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	if err := s.rt.UpdateEngineSettings(params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.releaseEngineScopes()
	return s.writeResponse(req.ID, EngineListResult{
		Engines:  s.engineInventory(),
		Settings: s.engineSettingsFromConfig(),
	}, nil)
}

// engineInventory lists every supported engine, including disabled or missing
// external binaries, so settings never confuse absence with a loading state.
func (s *Server) engineInventory() []EngineInfo {
	return s.engineInventoryWithRefresh(false)
}

func (s *Server) engineInventoryWithRefresh(refreshModels bool) []EngineInfo {
	if s.rt == nil {
		return nil
	}
	return s.engineInventoryAt(s.rt.RootDir, refreshModels)
}

func (s *Server) engineInventoryAt(root string, refreshModels bool) []EngineInfo {
	if s.rt == nil {
		return nil
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return nil
	}
	defer binding.Close()
	return s.engineInventoryForState(binding, root, refreshModels)
}

func (s *Server) engineInventoryForState(binding *runtime.State, root string, refreshModels bool) []EngineInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	descriptors := binding.EngineDescriptors(ctx)
	out := make([]EngineInfo, len(descriptors))
	var wg sync.WaitGroup
	for i, descriptor := range descriptors {
		wg.Go(func() {
			info := EngineInfo{ID: string(descriptor.ID), Version: descriptor.Version, Capabilities: append([]string(nil), descriptor.Capabilities...), RequiresHostModel: descriptor.RequiresHostModel, Enabled: true}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			var description pluginprotocol.EngineDescription
			if err := binding.CallEngine(ctx, descriptor.ID, "describe", struct{}{}, &description); err != nil {
				info.Error = err.Error()
			} else {
				info.DisplayName, info.Protocol, info.InstallURL, info.UpdateURL = description.DisplayName, description.Protocol, description.InstallURL, description.UpdateURL
				info.CLIVersion, info.BinaryPath, info.BinaryOK, info.Error = description.CLIVersion, description.BinaryPath, description.BinaryOK, description.Error
				info.PermissionModes = description.PermissionModes
				if description.Enabled != nil {
					info.Enabled = *description.Enabled
				}
			}
			if info.Enabled && info.BinaryOK {
				var models pluginprotocol.EngineModelsResult
				if err := binding.CallEngine(ctx, descriptor.ID, "models", pluginprotocol.EngineModelsRequest{CWD: root, Refresh: refreshModels}, &models); err != nil {
					info.ModelsStatus, info.ModelsError = "error", err.Error()
				} else {
					info.Models, info.ModelsStatus, info.ModelsError = models.Models, models.Status, models.Error
					if len(models.PermissionModes) > 0 {
						info.PermissionModes = models.PermissionModes
					}
				}
			}
			out[i] = info
		})
	}
	wg.Wait()
	return out
}

// attachSubscriptionActivity reads all dashboard sources together. A store
// read failure leaves the fields empty:
// the inventory itself is still usable, and the dashboard says the request is
// unknown rather than inventing a status.
func (s *Server) attachSubscriptionActivity(binding *runtime.State, engines []EngineInfo, providers []ProviderSummary) {
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return
	}
	keys := make([]session.SubscriptionActivityKey, 0, len(engines))
	for _, engine := range engines {
		if engine.ID == "" {
			continue
		}
		keys = append(keys, session.SubscriptionActivityKey{EngineID: engine.ID})
	}
	for _, provider := range providers {
		keys = append(keys, session.SubscriptionActivityKey{Provider: provider.Name})
	}
	if len(keys) == 0 {
		return
	}
	activity, err := binding.Sessions.LatestSubscriptionActivity(keys)
	if err != nil {
		return
	}
	for i := range engines {
		record, ok := activity[session.SubscriptionActivityKey{EngineID: engines[i].ID}]
		if !ok {
			continue
		}
		engines[i].LatestRequest = engineLatestRequest(record)
		engines[i].LocalUsage = &record.LocalUsage
	}
	for i := range providers {
		record, ok := activity[session.SubscriptionActivityKey{Provider: providers[i].Name}]
		if !ok {
			continue
		}
		providers[i].LatestRequest = engineLatestRequest(record)
		providers[i].LocalUsage = &record.LocalUsage
	}
}

func engineLatestRequest(record session.SubscriptionActivity) *EngineLatestRequest {
	latest := &EngineLatestRequest{
		Status:        strings.TrimSpace(record.Status),
		Error:         strings.TrimSpace(record.Error),
		Model:         strings.TrimSpace(record.Model),
		UsageReported: record.UsageReported,
	}
	if !record.At.IsZero() {
		latest.At = record.At.UTC().Format(time.RFC3339)
	}
	if record.UsageReported {
		latest.InputTokens = record.InputTokens
		latest.OutputTokens = record.OutputTokens
		latest.CacheCreationTokens = record.CacheCreationTokens
		latest.CacheReadTokens = record.CacheReadTokens
	}
	return latest
}

// engineSettingsFromConfig returns the persisted engine settings section.
func (s *Server) engineSettingsFromConfig() *config.EnginesConfig {
	if s.rt == nil {
		return nil
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return nil
	}
	return cfg.Engines
}
