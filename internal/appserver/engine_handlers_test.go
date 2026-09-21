package appserver

import (
	"testing"
	"time"
)

func TestCodexEngineModelCatalogCacheUsesFreshMatchingBinary(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	entry := &codexEngineModelCatalogCacheEntry{
		binaryPath: "/usr/local/bin/codex",
		models: []EngineModelInfo{{
			ID:               "gpt-test",
			SupportedEfforts: []string{"low", "high"},
		}},
		expiresAt: now.Add(codexEngineModelCatalogTTL),
	}

	models, ok := entry.load("/usr/local/bin/codex", now.Add(time.Hour))
	if !ok || len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("fresh matching cache = (%+v, %v), want cached model", models, ok)
	}
	models[0].SupportedEfforts[0] = "changed"
	if entry.models[0].SupportedEfforts[0] != "low" {
		t.Fatal("cache returned mutable model effort storage")
	}

	if _, ok := entry.load("/opt/codex", now.Add(time.Hour)); ok {
		t.Fatal("cache matched a different binary path")
	}
	if _, ok := entry.load("/usr/local/bin/codex", entry.expiresAt); ok {
		t.Fatal("cache remained fresh at its expiration boundary")
	}
}

func TestACPEngineCatalogCacheClonesPermissionModes(t *testing.T) {
	now := time.Now()
	entry := &codexEngineModelCatalogCacheEntry{
		binaryPath: "/usr/local/bin/grok",
		models:     []EngineModelInfo{{ID: "grok-4.6"}},
		modes:      []EnginePermissionModeInfo{{Mode: "standard", ID: "ask", Label: "Ask"}},
		expiresAt:  now.Add(time.Hour),
	}
	_, modes, ok := entry.loadCatalog("/usr/local/bin/grok", now)
	if !ok || len(modes) != 1 || modes[0].Label != "Ask" {
		t.Fatalf("cached modes = (%+v, %v)", modes, ok)
	}
	modes[0].Label = "changed"
	if entry.modes[0].Label != "Ask" {
		t.Fatal("cache returned mutable permission mode storage")
	}
}

func TestACPEngineModelCatalogCacheHitsThenInvalidatesAfterLogin(t *testing.T) {
	now := time.Now()
	s := &Server{acpEngineModelCatalogCache: map[string]*codexEngineModelCatalogCacheEntry{
		"grok": {
			binaryPath: "/usr/local/bin/grok",
			models:     []EngineModelInfo{{ID: "grok-4.6", DisplayName: "Grok 4.6"}},
			expiresAt:  now.Add(time.Hour),
		},
	}}
	models, err := s.cachedACPEngineModels("grok", "/usr/local/bin/grok", nil)
	if err != nil || len(models) != 1 || models[0].ID != "grok-4.6" {
		t.Fatalf("cached grok models = (%+v, %v)", models, err)
	}
	s.invalidateACPEngineModelCatalog("grok")
	if _, ok := s.acpEngineModelCatalogCache["grok"]; ok {
		t.Fatal("login did not drop the grok model cache")
	}
}
