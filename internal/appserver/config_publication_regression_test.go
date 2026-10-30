package appserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestOwnExtensionPolicyPublicationDoesNotReplayInConfigWatcher(t *testing.T) {
	rt := writeProviderSettingsConfig(t, newTestRuntime(t, &fakeClient{}), `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model","api_key":"synthetic"}}}`)
	out := &lockedBuffer{}
	srv := newProviderSettingsServer(t, rt, out)
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"id":"publication-check"}`), 0600); err != nil {
		t.Fatal(err)
	}
	callPluginPackageRPC(t, srv, "install-publication", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
	response := responseByID(t, parseOutput(t, out.String()), "install-publication")
	if response["error"] != nil {
		t.Fatal(response["error"])
	}
	installed := remarshal[PluginPackageInstallResult](t, response["result"])
	record := pluginPackageRecord(t, installed.ExtensionInventory, "publication-check")
	for _, action := range []ExtensionPackageAction{ExtensionPackageDisable, ExtensionPackageEnable} {
		id := "publication-" + string(action)
		callPluginPackageRPC(t, srv, id, MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: record.ID, Fingerprint: record.Fingerprint, Action: action})
		response = responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatal(response["error"])
		}
		host := rt.PluginHost
		fingerprint := srv.configFingerprint
		if fingerprint == "" {
			t.Fatal("published config was not observed")
		}
		if err := srv.refreshConfigIfChanged(); err != nil {
			t.Fatal(err)
		}
		if rt.PluginHost != host {
			t.Fatalf("watcher republished own %s transaction", action)
		}
		if srv.pluginGenerationMutation.Load() {
			t.Fatal("watcher retained mutation admission")
		}
	}
	// The publication fingerprint must not swallow a subsequent external edit.
	host := rt.PluginHost
	data, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["agent"] = map[string]any{"max_steps": 41}
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = srv.refreshConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	if rt.PluginHost == host {
		t.Fatal("publication fingerprint hid a later external edit")
	}

}

// An explicit refresh must not leave its already-published composition queued
// for a second watcher publication that rejects the user's next package action.
// Model edits remain observable whether captured by that refresh or written
// while its candidate is preparing; adopting a whole-file hash cannot hide them.
func TestCatalogRefreshPublicationDoesNotReplayOrHideModelEdits(t *testing.T) {
	for _, scenario := range []string{"composition-only", "desktop-runtime-overrides", "model-in-same-edit", "model-during-refresh", "general-in-same-edit"} {
		t.Run(scenario, func(t *testing.T) {
			type buildGate struct{ entered, release chan struct{} }
			var nextGate atomic.Pointer[buildGate]
			factory := runtime.NativeProviderFactory{ID: "fixture.config-publication", DefaultEnabled: true,
				Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
					if gate := nextGate.Swap(nil); gate != nil {
						close(gate.entered)
						<-gate.release
					}
					return &configPublicationFailureClient{}, nil
				},
			}
			body := `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model","api_key":"synthetic"}}}`
			if scenario == "desktop-runtime-overrides" {
				body = `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model","api_key":"synthetic"}},"agent":{"max_steps":41,"temperature":0.7}}`
			}
			rt := writeProviderSettingsConfig(t, newTestRuntime(t, &fakeClient{}), body, factory)
			rt.DesktopExecutionDefaults = scenario == "desktop-runtime-overrides"
			out := &lockedBuffer{}
			// Drive the real watcher path explicitly instead of relying on a file
			// debounce timer or racing a background observer during setup.
			srv := newOwnerHandlerFixture(rt, out)
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"id":"catalog-publication"}`), 0600); err != nil {
				t.Fatal(err)
			}
			callPluginPackageRPC(t, srv, "install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
			installedResponse := responseByID(t, parseOutput(t, out.String()), "install")
			if installedResponse["error"] != nil {
				t.Fatal(installedResponse["error"])
			}
			record := pluginPackageRecord(t, remarshal[PluginPackageInstallResult](t, installedResponse["result"]).ExtensionInventory, "catalog-publication")
			cfg := readProviderSettingsConfig(t, rt)
			cfg.Composition = composition.DefaultConfig("headless", "fixture.config-publication", "catalog-publication")
			writeConfig := func() {
				t.Helper()
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(rt.ConfigPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeConfig()
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			beforeModel := rt.Model
			cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{"catalog-publication": false}})
			setModel := func() {
				provider := cfg.Providers["fake-provider"]
				provider.Model = "externally-selected-model"
				cfg.Providers["fake-provider"] = provider
			}
			if scenario == "model-in-same-edit" {
				setModel()
			} else if scenario == "general-in-same-edit" {
				cfg.Agent.MaxSteps = 41
			}
			writeConfig()
			refresh := func() error {
				return srv.handleExtensionCatalogRefresh(Request{ID: json.RawMessage(`"refresh"`), Method: MethodExtensionCatalogRefresh})
			}
			if scenario == "model-during-refresh" {
				gate := &buildGate{entered: make(chan struct{}), release: make(chan struct{})}
				release := sync.OnceFunc(func() { close(gate.release) })
				defer release()
				nextGate.Store(gate)
				done := make(chan error, 1)
				go func() { done <- refresh() }()
				select {
				case <-gate.entered:
				case err := <-done:
					t.Fatalf("refresh did not prepare its graph: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("refresh preparation did not start")
				}
				setModel()
				writeConfig()
				release()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			} else if err := refresh(); err != nil {
				t.Fatal(err)
			}
			if response := responseByID(t, parseOutput(t, out.String()), "refresh"); response["error"] != nil {
				t.Fatal(response["error"])
			}
			published := rt.PluginHost
			if scenario == "composition-only" || scenario == "desktop-runtime-overrides" {
				gate := &buildGate{entered: make(chan struct{}), release: make(chan struct{})}
				release := sync.OnceFunc(func() { close(gate.release) })
				defer release()
				nextGate.Store(gate)
				done := make(chan error, 1)
				go func() { done <- srv.refreshConfigIfChanged() }()
				select {
				case <-gate.entered:
					callPluginPackageRPC(t, srv, "disable", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: record.ID, Fingerprint: record.Fingerprint, Action: ExtensionPackageDisable})
					response := responseByID(t, parseOutput(t, out.String()), "disable")
					release()
					if err := <-done; err != nil {
						t.Fatal(err)
					}
					t.Fatalf("watcher replayed completed refresh and blocked immediate disable: %+v", response)
				case err := <-done:
					nextGate.Store(nil)
					release()
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("watcher observation did not finish")
				}
				if rt.PluginHost != published || rt.Model != beforeModel {
					t.Fatal("unchanged watcher republished the completed refresh")
				}
			} else {
				if err := srv.refreshConfigIfChanged(); err != nil {
					t.Fatal(err)
				}
				if scenario == "general-in-same-edit" {
					if rt.PluginHost == published {
						t.Fatal("refresh fingerprint hid an external general-setting edit")
					}
				} else if rt.Model != "externally-selected-model" {
					t.Fatalf("refresh fingerprint hid external model selection: got %q", rt.Model)
				}
			}
			callPluginPackageRPC(t, srv, "disable", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: record.ID, Fingerprint: record.Fingerprint, Action: ExtensionPackageDisable})
			if response := responseByID(t, parseOutput(t, out.String()), "disable"); response["error"] != nil {
				t.Fatalf("completed refresh did not leave package changes ready: %+v", response)
			}
			if scenario == "desktop-runtime-overrides" {
				saved := readProviderSettingsConfig(t, rt)
				if saved.Agent.MaxSteps != 41 || saved.Agent.Temperature != 0.7 {
					t.Fatal("runtime overrides were written into saved settings")
				}
			}
		})
	}
}

// Refresh remains a catalog operation for compositions without model execution;
// avoiding a duplicate publication must not require a fabricated model choice.
func TestCatalogRefreshPublicationWithoutModel(t *testing.T) {
	rt := writeProviderSettingsConfig(t, newTestRuntime(t, &fakeClient{}), `{"composition":{"version":5,"profile":"minimal"}}`)
	out := &lockedBuffer{}
	srv := newOwnerHandlerFixture(rt, out)
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := rt.LoadEffectiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Composition.Patches = append(cfg.Composition.Patches, composition.Patch{Modules: map[string]bool{"catalog-publication-unused": false}})
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := srv.handleExtensionCatalogRefresh(Request{ID: json.RawMessage(`"refresh"`), Method: MethodExtensionCatalogRefresh}); err != nil {
		t.Fatal(err)
	}
	if response := responseByID(t, parseOutput(t, out.String()), "refresh"); response["error"] != nil {
		t.Fatal(response["error"])
	}
	published := rt.PluginHost
	if err := srv.refreshConfigIfChanged(); err != nil {
		t.Fatalf("model-free refresh left an unapplied duplicate: %v", err)
	}
	if rt.PluginHost != published || rt.Model != "" || rt.ProviderName != "" {
		t.Fatal("model-free refresh rebuilt the graph or fabricated a model")
	}
}

// A failed preparation preserves the old graph; a committed activation failure
// must publish its diagnostic graph but must not claim a successful receipt.
func TestCatalogRefreshFailureDoesNotAdoptConfig(t *testing.T) {
	for _, stage := range []string{"preparation", "committed-activation"} {
		t.Run(stage, func(t *testing.T) {
			var fail atomic.Bool
			factory := runtime.NativeProviderFactory{ID: "fixture.config-publication", DefaultEnabled: true,
				Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
					if fail.Load() && stage == "preparation" {
						return nil, errors.New("fixture preparation failed")
					}
					return &configPublicationFailureClient{fail: fail.Load()}, nil
				},
			}
			rt := writeProviderSettingsConfig(t, newTestRuntime(t, &fakeClient{}), `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model","api_key":"synthetic"}}}`, factory)
			out := &lockedBuffer{}
			srv := newOwnerHandlerFixture(rt, out)
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			beforeHost, beforeFingerprint := rt.PluginHost, srv.configFingerprint
			cfg, _, err := rt.LoadEffectiveConfig()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Composition = composition.DefaultConfig("headless", "fixture.config-publication")
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			fail.Store(true)
			if err := srv.handleExtensionCatalogRefresh(Request{ID: json.RawMessage(`"refresh"`), Method: MethodExtensionCatalogRefresh}); err != nil {
				t.Fatal(err)
			}
			messages := parseOutput(t, out.String())
			if response := responseByID(t, messages, "refresh"); response["error"] == nil {
				t.Errorf("%s failure was reported as successful", stage)
			}
			if srv.configFingerprint != beforeFingerprint {
				t.Error("failed catalog publication adopted the pending configuration")
			}
			if stage == "preparation" {
				if rt.PluginHost != beforeHost {
					t.Fatal("failed preparation replaced the active graph")
				}
			} else {
				if rt.PluginHost == beforeHost {
					t.Fatal("fixture did not reach a committed activation failure")
				}
				if len(notificationsByMethod(messages, NotificationPluginInventoryChanged)) == 0 {
					t.Fatal("committed failure did not publish its diagnostic inventory")
				}
			}
			fail.Store(false)
			if err := srv.refreshConfigIfChanged(); err != nil {
				t.Fatal(err)
			}
			if srv.configFingerprint == beforeFingerprint {
				t.Fatal("watcher could not apply the pending config after repair")
			}
		})
	}
}
