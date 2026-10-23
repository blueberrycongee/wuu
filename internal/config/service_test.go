package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// A configuration replacement must preserve raw source bytes, reject stale
// writers, retain user-only settings across project overlays and fail closed.
func TestConfigurationServiceCASAndSourcePreservation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	source := []byte("{\n  \"default_provider\": \"example\",\n  \"providers\": {\"example\": {\"type\": \"openai-compatible\", \"model\": \"test\", \"base_url\": \"https://example.test/v1\", \"api_key\": \"private-value\"}},\n  \"agent\": {\"permission_mode\": \"read_only\"}\n}\n")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".wuu"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".wuu", "settings.local.json"), []byte(`{"agent":{"permission_mode":"unconfined"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	service := NewService("test.config", path, root)
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	if _, err := client.Read(ctx); err == nil {
		t.Fatal("prepared config provider read")
	}
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := client.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(view.Source, source) || view.Config.Agent.PermissionMode != PermissionModeReadOnly {
		t.Fatalf("source/authority lost: equal=%v mode=%s", bytes.Equal(view.Source, source), view.Config.Agent.PermissionMode)
	}
	replacement := bytes.Replace(source, []byte(`"model": "test"`), []byte(`"model": "next"`), 1)
	if _, err := client.CompareExchange(ctx, view.Revision, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CompareExchange(ctx, view.Revision, source); err == nil {
		t.Fatal("stale writer committed")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, replacement) {
		t.Fatalf("stored source changed: %v", err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx); err == nil {
		t.Fatal("closed config provider read")
	}
}
func TestConfigurationServiceDoesNotLeakInvalidSecretsOrReplay(t *testing.T) {
	ctx := context.Background()
	service := NewService("test.config", filepath.Join(t.TempDir(), "config.json"), "")
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: "compare-exchange", Params: json.RawMessage(`{"expected_revision":"old","source":"c2VjcmV0LXZhbHVl"}`)})
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("invalid source error leaked: %v", err)
	}
	calls := 0
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("outcome unknown")
	})
	if _, err := client.CompareExchange(ctx, "old", []byte(`{}`)); err == nil || calls != 1 {
		t.Fatalf("uncertain write: calls=%d err=%v", calls, err)
	}
}

func TestConfigurationSourceEditsPreserveUnrelatedValues(t *testing.T) {
	source := []byte(layerBaseConfigJSON)
	next, err := UpdateProviderSource(source, "main", ProviderChange{Model: "next-model"})
	if err != nil {
		t.Fatal(err)
	}
	var original, updated map[string]json.RawMessage
	json.Unmarshal(source, &original)
	json.Unmarshal(next, &updated)
	if !bytes.Equal(original["memory"], updated["memory"]) {
		var a, b any
		json.Unmarshal(original["memory"], &a)
		json.Unmarshal(updated["memory"], &b)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("provider edit changed unrelated memory")
		}
	}
	enabled := false
	next, err = UpdateGeneralSettingsSource(next, GeneralSettingsUpdate{GitAttributionEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeConfig(next, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["main"].Model != "next-model" || cfg.Agent.GitAttributionEnabledValue() {
		t.Fatal("source edits lost explicit settings")
	}
}

// Embedded callers own their initial values. Selection must not substitute a
// disk default, share caller maps, accept stale writes, or work after closure.
func TestMemoryConfigurationPreservesExplicitValuesAndCAS(t *testing.T) {
	ctx := context.Background()
	cfg := Config{DefaultProvider: "embedded", Providers: map[string]ProviderConfig{
		"embedded": {Type: "openai-compatible", Model: "embedded-model", BaseURL: "https://example.test/v1"},
	}}
	service := NewMemoryService("test.memory-config", cfg)
	cfg.Providers["embedded"] = ProviderConfig{Model: "mutated-after-construction"}
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: ServiceName, Method: method, Params: raw})
	})
	if _, err := client.Read(ctx); err == nil {
		t.Fatal("prepared memory provider read")
	}
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := client.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Config.DefaultProvider != "embedded" || view.Config.Providers["embedded"].Model != "embedded-model" || view.Config.Agent.MaxSteps != 0 {
		t.Fatal("explicit embedded configuration was replaced, defaulted, or aliased")
	}
	next, err := UpdateProviderModelSource(view.Source, "embedded", "replacement")
	if err != nil {
		t.Fatal(err)
	}
	committed, err := client.CompareExchange(ctx, view.Revision, next)
	if err != nil || !committed.Committed || committed.Config.Providers["embedded"].Model != "replacement" {
		t.Fatalf("replacement failed: %v", err)
	}
	if _, err := client.CompareExchange(ctx, view.Revision, view.Source); err == nil {
		t.Fatal("stale memory write committed")
	}
	current, err := client.Read(ctx)
	if err != nil || current.Revision != committed.Revision || !bytes.Equal(current.Source, next) {
		t.Fatalf("replacement not retained: %v", err)
	}
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx); err == nil {
		t.Fatal("closed memory provider read")
	}
}
