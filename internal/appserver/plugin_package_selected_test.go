package appserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestPluginPackageSelectedConfigPromotionAndRemoval(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, ".wuu"))
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture-key", Model: "fixture-model"}}
	cfg.DefaultProvider = "fixture"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	selectedPath := filepath.Join(root, "selected.json")
	if err := os.WriteFile(selectedPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewSession(runtime.Options{RootDir: root, HomeDir: home, ConfigPath: selectedPath, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	call := func(id, method string, params any) map[string]any {
		t.Helper()
		callPluginPackageRPC(t, srv, id, method, params)
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("%s failed: %v", id, response["error"])
		}
		return response
	}
	first := writeManagedPluginPackage(t, "selected-config-package", "1.0.0", "first")
	installed := remarshal[PluginPackageInstallResult](t, call("install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: first})["result"])
	record := pluginPackageRecord(t, installed.ExtensionInventory, "selected-config-package")
	if record.ApprovalState != ExtensionApprovalGranted {
		t.Fatalf("installation did not activate: %+v", record)
	}
	// Same-source bytes change is trusted, and an explicit disabled choice survives.
	call("disable", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: record.ID, Fingerprint: record.Fingerprint, Action: ExtensionPackageDisable})
	writePluginPackageFile(t, filepath.Join(first, "plugin.json"), `{"id":"selected-config-package","version":"1.1.0","skills":["skills"]}`)
	updated := remarshal[PluginPackageInstallResult](t, call("same-source", MethodPluginPackageInstall, PluginPackageInstallParams{Path: first})["result"])
	updatedRecord := pluginPackageRecord(t, updated.ExtensionInventory, "selected-config-package")
	if updated.Pending || !updated.Replaced || updatedRecord.ApprovalState != ExtensionApprovalGranted || updatedRecord.Enabled == nil || *updatedRecord.Enabled {
		t.Fatalf("same-source update changed trust/enable choice: %+v %+v", updated, updatedRecord)
	}
	call("enable", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: updatedRecord.ID, Fingerprint: updatedRecord.Fingerprint, Action: ExtensionPackageEnable})
	second := writeManagedPluginPackage(t, "selected-config-package", "2.0.0", "second")
	staged := remarshal[PluginPackageInstallResult](t, call("stage", MethodPluginPackageInstall, PluginPackageInstallParams{Path: second})["result"])
	call("promote", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: record.ID, Fingerprint: staged.Package.Fingerprint, Action: ExtensionPackagePromoteUpdate})
	source, err := os.ReadFile(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	var promoted config.Config
	if err := json.Unmarshal(source, &promoted); err != nil {
		t.Fatal(err)
	}
	if promoted.Extensions == nil {
		t.Fatal("selected configuration lost policy")
	}
	if _, ok := promoted.Extensions.FindGrant(record.ID, staged.Package.Fingerprint); !ok {
		t.Fatal("promotion bypassed selected configuration")
	}
	call("remove", MethodPluginPackageRemove, PluginPackageRemoveParams{ID: "selected-config-package"})
	source, err = os.ReadFile(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	var removed config.Config
	if err := json.Unmarshal(source, &removed); err != nil {
		t.Fatal(err)
	}
	if removed.Extensions != nil {
		if _, ok := removed.Extensions.Grants[record.ID]; ok {
			t.Fatal("removal left selected configuration grant")
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".wuu", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("package operation accessed unselected canonical config: %v", err)
	}
}

func TestPluginPackageInstallPublishesCommittedConfigActivationFailure(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	preparePluginRuntimePathsForTest(t, rt)
	provider := &observedConfigSource{Service: config.NewService(config.Module, rt.ConfigPath, ""), outcome: "committed"}
	rt = newConfigRefreshRuntime(t, rt, runtime.NativeProviderFactory{
		ID: config.Module, DefaultEnabled: true,
		Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) { return provider, nil },
	})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	source := writeManagedPluginPackage(t, "committed-install", "1.0.0", "installed")
	callPluginPackageRPC(t, srv, "install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: source})
	messages := parseOutput(t, out.String())
	response := responseByID(t, messages, "install")
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "configuration was saved") {
		t.Fatalf("committed activation failure hidden: %+v", response)
	}
	found := false
	for _, message := range messages {
		if message["method"] != NotificationPluginInventoryChanged {
			continue
		}
		publication := remarshal[PluginInventoryChangedNotification](t, message["params"])
		record := pluginPackageRecord(t, publication.ExtensionInventory, "committed-install")
		if record.ApprovalState != ExtensionApprovalGranted {
			t.Fatalf("committed trust not published: %+v", record)
		}
		found = true
	}
	if !found {
		t.Fatalf("committed inventory was not published: %s", out.String())
	}
}
