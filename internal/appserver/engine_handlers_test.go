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
