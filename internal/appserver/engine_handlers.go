package appserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/externalengine"
	"github.com/blueberrycongee/wuu/internal/session"
)

const (
	engineModelCatalogTTL       = 2 * time.Minute
	engineModelDiscoveryTimeout = 30 * time.Second
)

type engineModelCatalogCacheEntry struct {
	gate       chan struct{}
	key        [32]byte
	completed  time.Time
	err        error
	status     string
	binaryPath string
	models     []EngineModelInfo
	modes      []EnginePermissionModeInfo
	expiresAt  time.Time
}

func (e *engineModelCatalogCacheEntry) load(binaryPath string, now time.Time) ([]EngineModelInfo, bool) {
	models, _, ok := e.loadCatalog(binaryPath, now)
	return models, ok
}

func (e *engineModelCatalogCacheEntry) loadCatalog(binaryPath string, now time.Time) ([]EngineModelInfo, []EnginePermissionModeInfo, bool) {
	if e == nil || e.binaryPath != binaryPath || !now.Before(e.expiresAt) {
		return nil, nil, false
	}
	return cloneEngineModels(e.models), cloneEnginePermissionModes(e.modes), true
}

// handleEngineList reports the engine inventory and the persisted engine
// settings for the settings UI.
func (s *Server) handleEngineList(req Request) error {
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
	result := EngineListResult{
		Engines:  s.engineInventoryAt(firstNonEmpty(params.CWD, s.rt.RootDir), params.RefreshModels),
		Settings: s.engineSettingsFromConfig(),
	}
	if params.IncludeQuota {
		result.SubscriptionProviders = append([]ProviderSummary(nil), s.providerSummaries()...)
		// Historical statistics belong to the opt-in dashboard request, not
		// navigation or the composer inventory. Copy providers before enriching
		// them so cached configuration summaries remain history-free.
		s.attachSubscriptionActivity(result.Engines, result.SubscriptionProviders)
		s.attachSubscriptionQuotas(result.Engines, result.SubscriptionProviders)
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
	if err := config.UpdateEnginesSettings(s.rt.ConfigPath, params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	s.applyEngineSettingsToRuntime()
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
	var out []EngineInfo
	// Built-in engine is always present.
	out = append(out, EngineInfo{
		ID:       string(agentengine.EngineWuu),
		Version:  "1",
		Enabled:  true,
		BinaryOK: true,
	})
	descriptors := make(map[agentengine.EngineID]agentengine.Descriptor)
	if s.rt.Engines() != nil {
		for _, desc := range s.rt.Engines().Descriptors() {
			descriptors[desc.ID] = desc
		}
	}
	settings := s.engineSettingsFromConfig()
	type acpProbe struct {
		index int
		entry enginecatalog.Entry
		path  string
	}
	var probes []acpProbe
	claudeIndex := -1
	for _, entry := range enginecatalog.Entries() {
		id := agentengine.EngineID(entry.ID)
		desc := descriptors[id]
		info := EngineInfo{
			ID:           string(id),
			DisplayName:  entry.Name,
			Protocol:     entry.Protocol,
			InstallURL:   entry.InstallURL,
			Version:      desc.Version,
			Capabilities: append([]string(nil), desc.Capabilities...),
			Enabled:      s.rt.EngineAvailable(id),
		}
		if id == "claude" || id == "grok" {
			info.Capabilities = append(info.Capabilities, "account-quota")
		}
		switch id {
		case "codex":
			path, err := s.codexBinaryPath()
			info.BinaryPath = path
			info.BinaryOK, info.Error = binaryStatus(path, err)
			if info.Enabled && info.BinaryOK {
				probes = append(probes, acpProbe{index: len(out), entry: entry, path: path})
			}
		case "claude":
			path, err := s.claudeBinaryPath()
			info.BinaryPath = path
			info.BinaryOK, info.Error = binaryStatus(path, err)
			if info.Enabled && info.BinaryOK {
				claudeIndex = len(out)
			}
		default:
			override := ""
			if setting := settings.Binary(entry.ID); setting != nil {
				override = setting.BinaryPath
			}
			path, err := entry.Resolve(override)
			info.BinaryPath = path
			info.BinaryOK, info.Error = binaryStatus(path, err)
			info.PermissionModes = hostPermissionModes(entry.Protocol)
			if info.Enabled && info.BinaryOK {
				probes = append(probes, acpProbe{index: len(out), entry: entry, path: path})
			}
		}
		out = append(out, info)
	}
	var wg sync.WaitGroup
	if claudeIndex >= 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info := &out[claudeIndex]
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			models, err := s.claudeEngineModelCatalog.List(ctx, info.BinaryPath, root, refreshModels)
			info.Models = claudeEngineModels(models)
			info.ModelsStatus = enginecatalog.CatalogStatus(len(models), err)
			if err != nil && len(models) > 0 {
				info.ModelsStatus = "stale"
			}
			if err != nil && !errors.Is(err, enginecatalog.ErrCatalogUnsupported) {
				info.ModelsError = err.Error()
			}
		}()
	}
	for _, probe := range probes {
		wg.Add(1)
		go func(probe acpProbe) {
			defer wg.Done()
			models, modes, status, err := s.cachedEngineCatalog(probe.entry, probe.path, root, refreshModels)
			out[probe.index].ModelsStatus = status
			if err != nil && !errors.Is(err, enginecatalog.ErrCatalogUnsupported) {
				out[probe.index].ModelsError = err.Error()
			}
			out[probe.index].Models = models
			if len(modes) > 0 {
				out[probe.index].PermissionModes = modes
			}
		}(probe)
	}
	wg.Wait()
	return out
}

// attachSubscriptionActivity reads all dashboard sources together. A store
// read failure leaves the fields empty:
// the inventory itself is still usable, and the dashboard says the request is
// unknown rather than inventing a status.
func (s *Server) attachSubscriptionActivity(engines []EngineInfo, providers []ProviderSummary) {
	if s == nil || s.rt == nil || strings.TrimSpace(s.rt.SessionDir) == "" {
		return
	}
	keys := make([]session.SubscriptionActivityKey, 0, len(engines))
	for _, engine := range engines {
		if engine.ID == "" || engine.ID == string(agentengine.EngineWuu) {
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
	activity, err := session.LatestSubscriptionActivity(s.rt.SessionDir, keys)
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

// cachedEngineCatalog serializes probes for an engine and rechecks native
// configuration before publishing. Failed refreshes retain only same-context
// results, accompanied by the error; successful empty responses replace them.
func (s *Server) cachedEngineCatalog(entry enginecatalog.Entry, binary, root string, force bool) ([]EngineModelInfo, []EnginePermissionModeInfo, string, error) {
	requested := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), engineModelDiscoveryTimeout)
	defer cancel()
	s.engineModelCatalogMu.Lock()
	if s.engineModelCatalogCache == nil {
		s.engineModelCatalogCache = make(map[string]*engineModelCatalogCacheEntry)
	}
	cache := s.engineModelCatalogCache[entry.ID]
	if cache == nil {
		cache = &engineModelCatalogCacheEntry{gate: make(chan struct{}, 1)}
		s.engineModelCatalogCache[entry.ID] = cache
	}
	s.engineModelCatalogMu.Unlock()
	select {
	case cache.gate <- struct{}{}:
		defer func() { <-cache.gate }()
	case <-ctx.Done():
		return nil, nil, "error", ctx.Err()
	}
	key, err := enginecatalog.DiscoveryContext(entry.ID, binary, root)
	if err != nil {
		cache.models, cache.modes, cache.completed, cache.expiresAt = nil, nil, time.Time{}, time.Time{}
		cache.status, cache.err = "", nil
		return nil, nil, "error", err
	}
	if key != cache.key {
		cache.key, cache.binaryPath = key, binary
		cache.models, cache.modes, cache.completed, cache.err = nil, nil, time.Time{}, nil
		cache.expiresAt = time.Time{}
		cache.status = ""
	}
	if cache.completed.After(requested) || !force && time.Now().Before(cache.expiresAt) {
		models, modes, _ := cache.loadCatalog(binary, cache.completed)
		return models, modes, cache.status, cache.err
	}
	var models []EngineModelInfo
	var modes []EnginePermissionModeInfo
	var discoverErr error
	if entry.Protocol == "codex" {
		// Discovery uses a short-lived host in the session directory so a live
		// conversation cannot keep an obsolete account/configuration snapshot.
		discovered, err := codexengine.NewHost(binary, root).ListModels(ctx)
		models, discoverErr = codexEngineModels(discovered), err
	} else {
		discovered, err := externalengine.New(entry, binary, root).DiscoverCatalog(ctx)
		models, modes, discoverErr = acpEngineModels(discovered.Models), acpEnginePermissionModes(discovered.Modes), err
	}
	current, err := enginecatalog.DiscoveryContext(entry.ID, binary, root)
	if err != nil || current != key {
		cache.models, cache.modes, cache.completed, cache.expiresAt = nil, nil, time.Time{}, time.Time{}
		cache.status, cache.err = "", nil
		if err != nil {
			return nil, nil, "error", err
		}
		return nil, nil, "error", errors.New("engine configuration changed during model discovery; refresh to retry")
	}
	cache.completed, cache.err = time.Now(), discoverErr
	previousStatus := cache.status
	cache.status = enginecatalog.CatalogStatus(len(models), discoverErr)
	ttl := engineModelCatalogTTL
	if discoverErr == nil || errors.Is(discoverErr, enginecatalog.ErrCatalogUnsupported) {
		cache.models, cache.modes = models, modes
	} else {
		ttl = 5 * time.Second
		if previousStatus == "ready" || previousStatus == "empty" || previousStatus == "stale" {
			cache.status = "stale"
		} else {
			cache.models, cache.modes = models, modes
			if len(models) > 0 {
				cache.status = "partial"
			}
		}
	}
	cache.expiresAt = cache.completed.Add(ttl)
	return cloneEngineModels(cache.models), cloneEnginePermissionModes(cache.modes), cache.status, cache.err
}

func (s *Server) invalidateACPEngineModelCatalog(engineID string) {
	s.engineModelCatalogMu.Lock()
	defer s.engineModelCatalogMu.Unlock()
	delete(s.engineModelCatalogCache, engineID)
}

func hostPermissionModes(protocol string) []EnginePermissionModeInfo {
	switch protocol {
	case "acp", "opencode":
		return []EnginePermissionModeInfo{{Mode: "standard"}, {Mode: "unconfined"}}
	default:
		return nil
	}
}

func acpEnginePermissionModes(modes []externalengine.HostPermissionMode) []EnginePermissionModeInfo {
	out := make([]EnginePermissionModeInfo, 0, len(modes))
	for _, mode := range modes {
		id := strings.TrimSpace(mode.Mode)
		if id == "" {
			continue
		}
		out = append(out, EnginePermissionModeInfo{
			Mode:  id,
			ID:    strings.TrimSpace(mode.ID),
			Label: strings.TrimSpace(mode.Label),
		})
	}
	return out
}

func cloneEnginePermissionModes(modes []EnginePermissionModeInfo) []EnginePermissionModeInfo {
	if modes == nil {
		return nil
	}
	cloned := make([]EnginePermissionModeInfo, len(modes))
	copy(cloned, modes)
	return cloned
}

func acpEngineModels(models []externalengine.DiscoveredModel) []EngineModelInfo {
	out := make([]EngineModelInfo, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		out = append(out, EngineModelInfo{
			ID:               id,
			DisplayName:      strings.TrimSpace(model.DisplayName),
			DefaultEffort:    strings.TrimSpace(model.DefaultEffort),
			SupportedEfforts: append([]string(nil), model.SupportedEfforts...),
			IsDefault:        model.IsDefault,
			FastMode:         model.FastMode,
			DefaultSpeed:     model.DefaultSpeed,
			Options:          enginecatalog.CloneModelOptions(model.Options),
		})
	}
	return out
}

func cloneEngineModels(models []EngineModelInfo) []EngineModelInfo {
	if models == nil {
		return nil
	}
	cloned := make([]EngineModelInfo, len(models))
	for i, model := range models {
		cloned[i] = model
		cloned[i].Options = enginecatalog.CloneModelOptions(model.Options)
		cloned[i].SupportedEfforts = append([]string(nil), model.SupportedEfforts...)
	}
	return cloned
}

func codexEngineModels(models []codexengine.ModelListItem) []EngineModelInfo {
	out := make([]EngineModelInfo, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.Model)
		if id == "" {
			id = strings.TrimSpace(model.ID)
		}
		if id == "" {
			continue
		}
		efforts := make([]string, 0, len(model.SupportedReasoningEfforts))
		for _, effort := range model.SupportedReasoningEfforts {
			if value := strings.TrimSpace(string(effort.ReasoningEffort)); value != "" {
				efforts = append(efforts, value)
			}
		}
		fast := false
		for _, tier := range model.ServiceTiers {
			if tier.ID == "fast" || tier.ID == "priority" {
				fast = true
			}
		}
		if len(model.ServiceTiers) == 0 {
			for _, tier := range model.AdditionalSpeedTiers {
				if tier == "fast" {
					fast = true
				}
			}
		}
		defaultSpeed := "standard"
		if model.DefaultServiceTier == "fast" || model.DefaultServiceTier == "priority" {
			defaultSpeed = "fast"
		}
		out = append(out, EngineModelInfo{
			FastMode: fast, DefaultSpeed: defaultSpeed,
			ID:               id,
			DisplayName:      strings.TrimSpace(model.DisplayName),
			DefaultEffort:    strings.TrimSpace(string(model.DefaultReasoningEffort)),
			SupportedEfforts: efforts,
			IsDefault:        model.IsDefault,
		})
	}
	return out
}

func claudeEngineModels(models []claudeengine.ModelInfo) []EngineModelInfo {
	out := make([]EngineModelInfo, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.DisplayName)
		if name == "" || name == model.ResolvedModel {
			name = model.Value
		}
		if model.ResolvedModel != "" && !strings.Contains(name, model.ResolvedModel) {
			name += " · " + model.ResolvedModel
		}
		out = append(out, EngineModelInfo{
			ID: model.Value, DisplayName: name, ResolvedModel: model.ResolvedModel,
			SupportedEfforts: append([]string(nil), model.SupportedEffortLevels...),
			FastMode:         model.SupportsFastMode,
		})
	}
	return out
}

// claudeBinaryPath resolves the claude binary: a settings override, otherwise
// the shared lookup (env override, PATH, and desktop install locations).
func (s *Server) claudeBinaryPath() (string, error) {
	if cfg := s.engineSettingsFromConfig(); cfg != nil && cfg.Claude != nil {
		if path := strings.TrimSpace(cfg.Claude.BinaryPath); path != "" {
			return path, nil
		}
	}
	return claudeengine.ResolveBinary()
}

// engineSettingsFromConfig returns the persisted engine settings section.
func (s *Server) engineSettingsFromConfig() *config.EnginesConfig {
	if s.rt == nil {
		return nil
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil || cfg.Engines == nil {
		return nil
	}
	return cfg.Engines
}

// applyEngineSettingsToRuntime pushes persisted settings into the live
// runtime: rebuilds the codex engine around the configured binary path and
// updates the default engine for new threads.
func (s *Server) applyEngineSettingsToRuntime() {
	if s.rt == nil {
		return
	}
	cfg, _, err := s.rt.LoadEffectiveConfig()
	if err != nil {
		return
	}
	codexBinary, codexResolveErr := codexengine.ResolveBinary()
	codexEnabled := codexResolveErr == nil
	claudeBinary, claudeResolveErr := claudeengine.ResolveBinary()
	claudeEnabled := claudeResolveErr == nil
	defaultEngine := agentengine.EngineWuu
	if engineCfg := cfg.Engines; engineCfg != nil {
		if trimmed := strings.TrimSpace(engineCfg.DefaultEngine); trimmed != "" {
			defaultEngine = agentengine.NormalizeEngineID(trimmed)
		}
		if codexCfg := engineCfg.Codex; codexCfg != nil {
			enabled, explicit := codexCfg.EngineEnabled()
			if explicit {
				codexEnabled = enabled
			}
			if path := strings.TrimSpace(codexCfg.BinaryPath); path != "" {
				codexBinary = path
				if !explicit {
					codexEnabled = true
				}
			}
		}
		if claudeCfg := engineCfg.Claude; claudeCfg != nil {
			enabled, explicit := claudeCfg.EngineEnabled()
			if explicit {
				claudeEnabled = enabled
			}
			if path := strings.TrimSpace(claudeCfg.BinaryPath); path != "" {
				claudeBinary = path
				if !explicit {
					claudeEnabled = true
				}
			}
		}
	}
	s.rt.RebuildCodexEngine(codexEnabled, codexBinary)
	s.rt.RebuildClaudeEngine(claudeEnabled, claudeBinary)
	s.rt.RebuildProtocolEngines(cfg.Engines)
	if !s.rt.EngineAvailable(defaultEngine) {
		defaultEngine = agentengine.EngineWuu
	}
	s.rt.DefaultEngine = defaultEngine
}

// codexBinaryPath resolves the codex binary: a settings override, otherwise
// the shared lookup (env override, PATH, and desktop install locations).
func (s *Server) codexBinaryPath() (string, error) {
	if cfg := s.engineSettingsFromConfig(); cfg != nil && cfg.Codex != nil {
		if path := strings.TrimSpace(cfg.Codex.BinaryPath); path != "" {
			return path, nil
		}
	}
	return codexengine.ResolveBinary()
}

// binaryStatus reports whether a resolved binary path exists and is
// executable, or whether the resolution failed.
func binaryStatus(path string, resolveErr error) (bool, string) {
	if resolveErr != nil {
		return false, resolveErr.Error()
	}
	if strings.TrimSpace(path) == "" {
		return false, "no binary path resolved"
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return false, "binary not found at " + path
	}
	if !filepath.IsAbs(path) {
		return false, "binary path is not absolute: " + path
	}
	return true, ""
}
