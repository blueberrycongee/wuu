package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/externalengine"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/anthropic"
	"github.com/blueberrycongee/wuu/internal/providers/grokbuild"
)

type nativeEngineAdmin struct {
	disabled                 bool
	factory                  agentengine.Factory
	engineID                 agentengine.EngineID
	binary, root             string
	credentials              credentialstore.Store
	claudeEngineModelCatalog claudeengine.ModelCatalog
	engineModelCatalogMu     sync.Mutex
	engineModelCatalogCache  map[string]*engineModelCatalogCacheEntry
}

func newNativeEngineAdmin(factory agentengine.Factory, engineID agentengine.EngineID, binary, root string, credentials credentialstore.Store) *nativeEngineAdmin {
	return &nativeEngineAdmin{factory: factory, engineID: engineID, binary: binary, root: root, credentials: credentials}
}

func (s *nativeEngineAdmin) Invoke(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.disabled && method != "describe" {
		return nil, errors.New("selected engine is disabled or unavailable")
	}
	switch method {
	case "describe":
		result := pluginprotocol.EngineDescription{BinaryPath: s.binary}
		if entry, ok := enginecatalog.Lookup(string(s.engineID)); ok {
			result.DisplayName, result.Protocol, result.InstallURL, result.UpdateURL = entry.Name, entry.Protocol, entry.InstallURL, entry.UpdateURL
			result.PermissionModes = hostPermissionModes(entry.Protocol)
		}
		if s.canonicalHistory(ctx) {
			result.BinaryOK = true
		} else {
			info, err := os.Stat(s.binary)
			result.BinaryOK = err == nil && !info.IsDir() && filepath.IsAbs(s.binary)
			if !result.BinaryOK {
				result.Error = "engine binary is unavailable: " + s.binary
			}
			if result.BinaryOK && !s.disabled {
				result.CLIVersion = enginecatalog.ObserveVersion(ctx, string(s.engineID), s.binary)
			}
		}
		return json.Marshal(result)
	case "models":
		var req pluginprotocol.EngineModelsRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		root := req.CWD
		if root == "" {
			root = s.root
		}
		result := pluginprotocol.EngineModelsResult{}
		var err error
		if _, ok := s.factory.(*claudeengine.Engine); ok {
			var models []claudeengine.ModelInfo
			models, err = s.claudeEngineModelCatalog.List(ctx, s.binary, root, req.Refresh)
			result.Models = claudeEngineModels(models)
			result.Status = enginecatalog.CatalogStatus(len(models), err)
			if err != nil && len(models) > 0 {
				result.Status = "stale"
			}
		} else if entry, ok := enginecatalog.Lookup(string(s.engineID)); ok {
			result.Models, result.PermissionModes, result.Status, err = s.cachedEngineCatalog(ctx, entry, s.binary, root, req.Refresh)
		} else {
			result.Status = "unsupported"
		}
		if err != nil && !errors.Is(err, enginecatalog.ErrCatalogUnsupported) {
			result.Error = err.Error()
		}
		return json.Marshal(result)
	case "auth":
		var req pluginprotocol.EngineAuthRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		if req.Remote && req.MethodID == "local-cli" {
			return nil, errors.New("CLI sign-in must be started on the local desktop")
		}
		engine, ok := s.factory.(*externalengine.Engine)
		if !ok {
			return nil, fmt.Errorf("engine %s does not support authentication", s.engineID)
		}
		result, err := engine.Authenticate(ctx, req.MethodID)
		if err != nil {
			return nil, err
		}
		if result.Authenticated {
			s.invalidateACPEngineModelCatalog(string(s.engineID))
		}
		return json.Marshal(result)
	case "quota":
		quota, err := s.readQuota(ctx)
		quotaJSON, marshalErr := json.Marshal(quota)
		if marshalErr != nil {
			return nil, marshalErr
		}
		result := pluginprotocol.EngineQuotaResult{Quota: quotaJSON}
		if err != nil {
			result.ErrorCode = providers.QuotaErrorCode(err)
		}
		return json.Marshal(result)
	case "history_fork":
		var req pluginprotocol.EngineHistoryForkRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, err
		}
		if s.canonicalHistory(ctx) {
			return json.Marshal(pluginprotocol.EngineHistoryForkResult{})
		}
		engine, ok := s.factory.(*codexengine.Engine)
		if !ok {
			return nil, fmt.Errorf("engine %s does not expose a verified native history cutoff; original history is unchanged", s.engineID)
		}
		if req.Removed != nil && len(req.Removed) == 0 {
			return nil, errors.New("history edit requires a non-empty removed boundary")
		}
		convert := func(messages []pluginprotocol.EngineHistoryMessage) []providers.ChatMessage {
			if messages == nil {
				return nil
			}
			out := make([]providers.ChatMessage, len(messages))
			for i, message := range messages {
				out[i] = providers.ChatMessage{Role: message.Role, EngineTurnID: message.EngineTurnID, ProviderItemID: message.ProviderItemID, Steered: message.Steered}
			}
			return out
		}
		retained, removed := convert(req.Retained), convert(req.Removed)
		ref, err := engine.ForkHistory(ctx, req.Ref, req.CWD, retained, removed, req.WholeTurn)
		if err != nil {
			return nil, err
		}
		return json.Marshal(pluginprotocol.EngineHistoryForkResult{Ref: ref})
	default:
		return nil, fmt.Errorf("engine operation %q is unsupported", method)
	}
}

func (s *nativeEngineAdmin) readQuota(ctx context.Context) (providers.Quota, error) {
	switch engine := s.factory.(type) {
	case *codexengine.Engine:
		limits, err := engine.Host().ReadRateLimits(ctx)
		quota := providers.Quota{Kind: "subscription", Plan: limits.RateLimits.PlanType, Windows: subscriptionQuotaWindows(limits)}
		if limits.AccountID != "" {
			quota.Account = &providers.QuotaAccount{ID: providers.QuotaAccountID("codex-cli", "account/rateLimits/read", limits.AccountID), Source: "codex-cli"}
		}
		if err != nil {
			return quota, providers.NewQuotaError(providers.QuotaErrorNetwork)
		}
		if len(quota.Windows) == 0 {
			return quota, providers.NewQuotaError(providers.QuotaErrorUnsupported)
		}
		return quota, nil
	case *claudeengine.Engine:
		if s.credentials == nil {
			return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
		}
		return anthropic.ReadSelectedQuota(ctx, s.credentials)
	case *externalengine.Engine:
		if s.engineID == "grok" {
			if s.credentials == nil {
				return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
			}
			client, err := grokbuild.New(grokbuild.ClientConfig{Credentials: s.credentials, ReuseGrokCredentials: true})
			if err != nil {
				return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorSignIn)
			}
			return client.ReadQuota(ctx)
		}
	}
	return providers.Quota{}, providers.NewQuotaError(providers.QuotaErrorUnsupported)
}

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
	models     []pluginprotocol.EngineModelInfo
	modes      []pluginprotocol.EnginePermissionModeInfo
	expiresAt  time.Time
}

func (e *engineModelCatalogCacheEntry) load(binaryPath string, now time.Time) ([]pluginprotocol.EngineModelInfo, bool) {
	models, _, ok := e.loadCatalog(binaryPath, now)
	return models, ok
}

func (e *engineModelCatalogCacheEntry) loadCatalog(binaryPath string, now time.Time) ([]pluginprotocol.EngineModelInfo, []pluginprotocol.EnginePermissionModeInfo, bool) {
	if e == nil || e.binaryPath != binaryPath || !now.Before(e.expiresAt) {
		return nil, nil, false
	}
	return cloneEngineModels(e.models), cloneEnginePermissionModes(e.modes), true
}

// cachedEngineCatalog serializes probes for an engine and rechecks native
// configuration before publishing. Failed refreshes retain only same-context
// results, accompanied by the error; successful empty responses replace them.
func (s *nativeEngineAdmin) cachedEngineCatalog(parent context.Context, entry enginecatalog.Entry, binary, root string, force bool) ([]pluginprotocol.EngineModelInfo, []pluginprotocol.EnginePermissionModeInfo, string, error) {
	requested := time.Now()
	ctx, cancel := context.WithTimeout(parent, engineModelDiscoveryTimeout)
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
	var models []pluginprotocol.EngineModelInfo
	var modes []pluginprotocol.EnginePermissionModeInfo
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

func (s *nativeEngineAdmin) invalidateACPEngineModelCatalog(engineID string) {
	s.engineModelCatalogMu.Lock()
	defer s.engineModelCatalogMu.Unlock()
	delete(s.engineModelCatalogCache, engineID)
}

func hostPermissionModes(protocol string) []pluginprotocol.EnginePermissionModeInfo {
	switch protocol {
	case "acp", "opencode":
		return []pluginprotocol.EnginePermissionModeInfo{{Mode: "standard"}, {Mode: "unconfined"}}
	default:
		return nil
	}
}

func acpEnginePermissionModes(modes []externalengine.HostPermissionMode) []pluginprotocol.EnginePermissionModeInfo {
	out := make([]pluginprotocol.EnginePermissionModeInfo, 0, len(modes))
	for _, mode := range modes {
		id := strings.TrimSpace(mode.Mode)
		if id == "" {
			continue
		}
		out = append(out, pluginprotocol.EnginePermissionModeInfo{
			Mode:  id,
			ID:    strings.TrimSpace(mode.ID),
			Label: strings.TrimSpace(mode.Label),
		})
	}
	return out
}

func cloneEnginePermissionModes(modes []pluginprotocol.EnginePermissionModeInfo) []pluginprotocol.EnginePermissionModeInfo {
	if modes == nil {
		return nil
	}
	cloned := make([]pluginprotocol.EnginePermissionModeInfo, len(modes))
	copy(cloned, modes)
	return cloned
}

func acpEngineModels(models []externalengine.DiscoveredModel) []pluginprotocol.EngineModelInfo {
	out := make([]pluginprotocol.EngineModelInfo, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		out = append(out, pluginprotocol.EngineModelInfo{
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

func cloneEngineModels(models []pluginprotocol.EngineModelInfo) []pluginprotocol.EngineModelInfo {
	if models == nil {
		return nil
	}
	cloned := make([]pluginprotocol.EngineModelInfo, len(models))
	for i, model := range models {
		cloned[i] = model
		cloned[i].Options = enginecatalog.CloneModelOptions(model.Options)
		cloned[i].SupportedEfforts = append([]string(nil), model.SupportedEfforts...)
	}
	return cloned
}

func codexEngineModels(models []codexengine.ModelListItem) []pluginprotocol.EngineModelInfo {
	out := make([]pluginprotocol.EngineModelInfo, 0, len(models))
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
		out = append(out, pluginprotocol.EngineModelInfo{
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

func claudeEngineModels(models []claudeengine.ModelInfo) []pluginprotocol.EngineModelInfo {
	out := make([]pluginprotocol.EngineModelInfo, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.DisplayName)
		if name == "" || name == model.ResolvedModel {
			name = model.Value
		}
		if model.ResolvedModel != "" && !strings.Contains(name, model.ResolvedModel) {
			name += " · " + model.ResolvedModel
		}
		out = append(out, pluginprotocol.EngineModelInfo{
			ID: model.Value, DisplayName: name, ResolvedModel: model.ResolvedModel,
			SupportedEfforts: append([]string(nil), model.SupportedEffortLevels...),
			FastMode:         model.SupportsFastMode,
		})
	}
	return out
}

func subscriptionQuotaWindows(limits codexengine.RateLimitsResponse) []providers.QuotaWindow {
	var windows []providers.QuotaWindow
	appendWindow := func(id, label string, window *codexengine.RateLimitWindow) {
		if window == nil {
			return
		}
		result := providers.QuotaWindow{ID: id, Label: label, UsedPercent: window.UsedPercent}
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

// Canonical history is an explicit provider capability, not an engine-ID grant.
func (s *nativeEngineAdmin) canonicalHistory(ctx context.Context) bool {
	if s.factory == nil {
		return false
	}
	descriptor, err := s.factory.Descriptor(ctx)
	return err == nil && slices.Contains(descriptor.Capabilities, "canonical-history")
}
