package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
