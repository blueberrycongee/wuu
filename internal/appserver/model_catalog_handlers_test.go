package appserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestConfigModelCatalogRefreshRPC(t *testing.T) {
	t.Cleanup(func() { _ = modelcatalog.UseEmbedded() })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
          "openai": {
            "id": "openai",
            "name": "OpenAI",
            "api": "https://api.openai.com/v1",
            "models": {"fresh-model": {"id": "fresh-model", "name": "Fresh Model"}}
          }
        }`))
	}))
	t.Cleanup(upstream.Close)

	var out bytes.Buffer
	wuuHome := t.TempDir()
	configPath := filepath.Join(wuuHome, "config.json")
	if err := os.WriteFile(configPath, []byte(`{
      "default_provider": "openai",
      "providers": {"openai": {
        "type": "openai",
        "base_url": "https://api.openai.com/v1",
        "model": "fresh-model"
      }}
    }`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := New(&runtime.Session{
		WuuHome: wuuHome, ConfigPath: configPath, ConfigLoadMode: runtime.ConfigLoadFile,
	}, &out)
	defer server.Close()
	server.modelCatalogURL = upstream.URL
	server.modelCatalogHTTPClient = upstream.Client()

	if err := server.handleConfigModelCatalogRefresh(context.Background(), Request{ID: []byte(`"refresh"`)}); err != nil {
		t.Fatalf("config/model-catalog/refresh error = %v", err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "refresh")
	result := remarshal[ConfigModelCatalogRefreshResult](t, response["result"])
	if result.ProviderCount != 1 || result.ModelCount < 1 {
		t.Fatalf("refresh counts = providers:%d models:%d", result.ProviderCount, result.ModelCount)
	}
	foundFresh := false
	for _, provider := range result.Providers {
		if provider.Name != "openai" {
			continue
		}
		for _, model := range provider.Models {
			if model.ID == "fresh-model" {
				foundFresh = true
				break
			}
		}
	}
	if !foundFresh {
		t.Fatalf("provider summaries = %#v", result.Providers)
	}
	server.Close()
	if err := modelcatalog.UseEmbedded(); err != nil {
		t.Fatal(err)
	}
	var restartedOut bytes.Buffer
	restarted := New(&runtime.Session{
		WuuHome: wuuHome, ConfigPath: configPath, ConfigLoadMode: runtime.ConfigLoadFile,
	}, &restartedOut)
	defer restarted.Close()
	cached, ok := modelcatalog.ProviderByID("openai")
	if !ok {
		t.Fatalf("startup cached provider missing, ok=%v", ok)
	}
	foundCachedFresh := false
	for _, model := range cached.Models {
		if model.ID == "fresh-model" {
			foundCachedFresh = true
			break
		}
	}
	if !foundCachedFresh {
		t.Fatalf("startup cached provider = %+v", cached)
	}
}
