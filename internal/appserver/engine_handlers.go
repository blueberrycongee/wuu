package appserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
	"github.com/blueberrycongee/wuu/internal/codexengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
)

const codexEngineModelCatalogTTL = 6 * time.Hour

type codexEngineModelCatalogCacheEntry struct {
	binaryPath string
	models     []EngineModelInfo
	expiresAt  time.Time
}

func (e *codexEngineModelCatalogCacheEntry) load(binaryPath string, now time.Time) ([]EngineModelInfo, bool) {
	if e == nil || e.binaryPath != binaryPath || !now.Before(e.expiresAt) {
		return nil, false
	}
	return cloneEngineModels(e.models), true
}

// handleEngineList reports the engine inventory and the persisted engine
// settings for the settings UI.
func (s *Server) handleEngineList(req Request) error {
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	result := EngineListResult{
		Engines:  s.engineInventory(),
		Settings: s.engineSettingsFromConfig(),
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
	if s.rt == nil {
		return nil
	}
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
		switch id {
		case "codex":
			path, err := s.codexBinaryPath()
			info.BinaryPath = path
			info.BinaryOK, info.Error = binaryStatus(path, err)
			if info.Enabled && info.BinaryOK && s.rt.CodexHost() != nil {
				models, modelErr := s.cachedCodexEngineModels(path, s.rt.CodexHost())
				if modelErr != nil {
					info.ModelsError = modelErr.Error()
				} else {
					info.Models = models
				}
			}
		case "claude":
			path, err := s.claudeBinaryPath()
			info.BinaryPath = path
			info.BinaryOK, info.Error = binaryStatus(path, err)
			if info.Enabled && info.BinaryOK {
				info.Models = claudeEngineModels()
			}
		default:
			override := ""
			if setting := settings.Binary(entry.ID); setting != nil {
				override = setting.BinaryPath
			}
			path, err := entry.Resolve(override)
			info.BinaryPath = path
			info.BinaryOK, info.Error = binaryStatus(path, err)
		}
		out = append(out, info)
	}
	return out
}

// cachedCodexEngineModels keeps the expensive app-server model catalog
// independent from lightweight binary detection. Engine settings can be
// revisited or re-detected without restarting Codex; a binary-path change
// naturally misses the cache and resolves a fresh catalog.
func (s *Server) cachedCodexEngineModels(binaryPath string, host *codexengine.Host) ([]EngineModelInfo, error) {
	s.engineModelCatalogMu.Lock()
	defer s.engineModelCatalogMu.Unlock()

	now := time.Now()
	if models, ok := s.codexEngineModelCatalogCache.load(binaryPath, now); ok {
		return models, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := host.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	converted := codexEngineModels(models)
	s.codexEngineModelCatalogCache = &codexEngineModelCatalogCacheEntry{
		binaryPath: binaryPath,
		models:     cloneEngineModels(converted),
		expiresAt:  now.Add(codexEngineModelCatalogTTL),
	}
	return converted, nil
}

func cloneEngineModels(models []EngineModelInfo) []EngineModelInfo {
	if models == nil {
		return nil
	}
	cloned := make([]EngineModelInfo, len(models))
	for i, model := range models {
		cloned[i] = model
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
		out = append(out, EngineModelInfo{
			ID:               id,
			DisplayName:      strings.TrimSpace(model.DisplayName),
			DefaultEffort:    strings.TrimSpace(string(model.DefaultReasoningEffort)),
			SupportedEfforts: efforts,
			IsDefault:        model.IsDefault,
		})
	}
	return out
}

// Claude Code exposes stable model aliases through --model. The CLI resolves
// each alias to the account's current eligible model, so this list does not
// hard-code dated model versions.
func claudeEngineModels() []EngineModelInfo {
	efforts := []string{"low", "medium", "high", "xhigh", "max"}
	return []EngineModelInfo{
		{ID: "sonnet", DisplayName: "Sonnet", DefaultEffort: "high", SupportedEfforts: efforts, IsDefault: true},
		{ID: "opus", DisplayName: "Opus", DefaultEffort: "high", SupportedEfforts: efforts},
		{ID: "haiku", DisplayName: "Haiku", DefaultEffort: "high", SupportedEfforts: efforts},
	}
}

// claudeBinaryPath resolves the claude binary: settings override, then env
// override, then PATH lookup.
func (s *Server) claudeBinaryPath() (string, error) {
	path := ""
	if cfg := s.engineSettingsFromConfig(); cfg != nil && cfg.Claude != nil {
		path = strings.TrimSpace(cfg.Claude.BinaryPath)
	}
	if path == "" {
		path = strings.TrimSpace(os.Getenv("WUU_CLAUDE_BINARY"))
	}
	if path != "" {
		return path, nil
	}
	return exec.LookPath("claude")
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

// codexBinaryPath resolves the codex binary: settings override, then env
// override, then PATH lookup.
func (s *Server) codexBinaryPath() (string, error) {
	path := ""
	if cfg := s.engineSettingsFromConfig(); cfg != nil && cfg.Codex != nil {
		path = strings.TrimSpace(cfg.Codex.BinaryPath)
	}
	if path == "" {
		path = strings.TrimSpace(os.Getenv("WUU_CODEX_BINARY"))
	}
	if path != "" {
		return path, nil
	}
	return exec.LookPath("codex")
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
