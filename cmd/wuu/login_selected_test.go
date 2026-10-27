package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers/xaisub"
)

func TestLoginAddsProviderThroughSelectedConfig(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WUU_HOME", filepath.Join(home, ".wuu"))
	cfg := config.Default()
	cfg.DefaultProvider = "kept"
	cfg.Providers = map[string]config.ProviderConfig{"kept": {Type: "openai-compatible", BaseURL: "https://example.test/v1", Model: "fixture", APIKey: "explicit"}}
	source := config.NewMemoryService("test.login-config", cfg)
	if err := source.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	defer source.Close(ctx)
	client := config.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return source.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: config.ServiceName, Method: method, Params: raw})
	})
	if err := ensureXAISubscriptionProvider(ctx, client); err != nil {
		t.Fatal(err)
	}
	view, err := client.Read(ctx)
	if err != nil || view.Config.DefaultProvider != "kept" || view.Config.Providers[xaisub.AuthProviderID].Type != "xai-subscription" {
		t.Fatalf("selected configuration lost: %v", err)
	}
	if err := ensureXAISubscriptionProvider(ctx, client); err != nil {
		t.Fatal(err)
	}
	after, err := client.Read(ctx)
	if err != nil || after.Revision != view.Revision {
		t.Fatal("existing connection was overwritten")
	}
	if _, err := os.Stat(filepath.Join(home, ".wuu", "config.json")); !os.IsNotExist(err) {
		t.Fatal("selected configuration created native file")
	}
	if err := source.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ensureXAISubscriptionProvider(ctx, client); err == nil {
		t.Fatal("closed source fell back to local configuration")
	}
}
