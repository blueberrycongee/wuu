package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestPluginSettingsValidateOwnershipTypeScopeAndGeneration(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t)
	callPluginPackageRPC(t, srv, "default", MethodPluginSettingGet, PluginSettingGetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Key: "enabled"})
	defaultResponse := responseByID(t, parseOutput(t, out.String()), "default")
	if defaultResponse["error"] != nil {
		t.Fatalf("default response = %+v", defaultResponse)
	}
	defaultResult := remarshal[PluginSettingResult](t, defaultResponse["result"])
	if defaultResult.Scope != PluginValueScopeWorkspace || string(defaultResult.Value) != "true" {
		t.Fatalf("default result = %+v", defaultResult)
	}
	callPluginPackageRPC(t, srv, "qualified", MethodPluginSettingGet, PluginSettingGetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Key: item.ID + ".enabled"})
	qualifiedResponse := responseByID(t, parseOutput(t, out.String()), "qualified")
	if qualifiedResponse["error"] != nil {
		t.Fatalf("qualified response = %+v", qualifiedResponse)
	}
	qualifiedResult := remarshal[PluginSettingResult](t, qualifiedResponse["result"])
	if qualifiedResult.Key != "enabled" || string(qualifiedResult.Value) != "true" {
		t.Fatalf("qualified result = %+v", qualifiedResult)
	}

	callPluginPackageRPC(t, srv, "set", MethodPluginSettingSet, PluginSettingSetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Key: "enabled", Value: json.RawMessage(`false`)})
	setResult := remarshal[PluginSettingResult](t, responseByID(t, parseOutput(t, out.String()), "set")["result"])
	if string(setResult.Value) != "false" {
		t.Fatalf("set result = %+v", setResult)
	}

	for id, params := range map[string]PluginSettingSetParams{
		"owner": {ID: item.SubjectID, Fingerprint: item.Fingerprint, Key: "other.enabled", Value: json.RawMessage(`true`)},
		"type":  {ID: item.SubjectID, Fingerprint: item.Fingerprint, Key: "enabled", Value: json.RawMessage(`"yes"`)},
		"stale": {ID: item.SubjectID, Fingerprint: "stale", Key: "enabled", Value: json.RawMessage(`true`)},
	} {
		callPluginPackageRPC(t, srv, id, MethodPluginSettingSet, params)
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] == nil {
			t.Fatalf("%s unexpectedly succeeded: %+v", id, response)
		}
	}
}

func TestPluginDiagnosticsExposeIsolatedCapabilityFailures(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t)
	broken := &isolatedFailureClient{id: item.ID, invokeErr: errors.New("observer boom")}
	srv.rt.PluginHost = pluginhost.New(broken)
	if err := srv.notifyPluginTurnCompleted(context.Background(), pluginhost.AgentTurnCompletedInput{}); err != nil {
		t.Fatal(err)
	}
	callPluginPackageRPC(t, srv, "diagnostics", MethodPluginDiagnosticsList, PluginDiagnosticsParams{
		ID: item.SubjectID, Fingerprint: item.Fingerprint,
	})
	result := remarshal[PluginDiagnosticsResult](t, responseByID(t, parseOutput(t, out.String()), "diagnostics")["result"])
	if result.ID != item.SubjectID || len(result.Diagnostics) != 1 || result.Diagnostics[0].Contribution != pluginhost.CapabilityAgentTurnCompleted || !strings.Contains(result.Diagnostics[0].Message, "observer boom") {
		t.Fatalf("diagnostics result = %+v", result)
	}
}

type isolatedFailureClient struct {
	id        string
	invokeErr error
}

func (c *isolatedFailureClient) ID() string { return c.id }
func (c *isolatedFailureClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *isolatedFailureClient) Close(context.Context) error { return nil }
func (c *isolatedFailureClient) ProtocolVersion() int        { return pluginhost.CapabilityProtocolVersion }
func (c *isolatedFailureClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentTurnCompleted, Kind: pluginhost.SeamObserve, ErrorPolicy: pluginhost.ErrorPolicyIsolate, Version: 1}}
}
func (c *isolatedFailureClient) InvokeCapability(context.Context, pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	return pluginhost.CapabilityInvokeResult{}, c.invokeErr
}

func TestPluginStorageIsNamespacedAndScopeIsExplicit(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t)
	for _, scope := range []PluginValueScope{PluginValueScopeUser, PluginValueScopeWorkspace} {
		id := "set-" + string(scope)
		callPluginPackageRPC(t, srv, id, MethodPluginStorageSet, PluginStorageSetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: scope, Key: "panel.mode", Value: string(scope)})
		if response := responseByID(t, parseOutput(t, out.String()), id); response["error"] != nil {
			t.Fatalf("%s failed: %+v", id, response)
		}
	}
	for _, scope := range []PluginValueScope{PluginValueScopeUser, PluginValueScopeWorkspace} {
		id := "get-" + string(scope)
		callPluginPackageRPC(t, srv, id, MethodPluginStorageGet, PluginStorageGetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: scope, Key: "panel.mode"})
		result := remarshal[PluginStorageResult](t, responseByID(t, parseOutput(t, out.String()), id)["result"])
		if result.Value == nil || *result.Value != string(scope) {
			t.Fatalf("%s result = %+v", scope, result)
		}
	}
	callPluginPackageRPC(t, srv, "bad-key", MethodPluginStorageGet, PluginStorageGetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: PluginValueScopeUser, Key: "../escape"})
	if responseByID(t, parseOutput(t, out.String()), "bad-key")["error"] == nil {
		t.Fatal("invalid storage key unexpectedly succeeded")
	}
}

func newPluginStateTestServer(t *testing.T) (*Server, pluginpkg.Plugin, *lockedBuffer) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.RootDir = t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "plugin.json")
	writePluginPackageFile(t, manifestPath, `{
  "id":"state-demo",
  "desktop":{"entry":"desktop.js"},
  "contributes":{"settings":{"enabled":{"type":"boolean","title":"Enabled","default":true,"scope":"workspace","apply":"live"}}}
}`)
	writePluginPackageFile(t, filepath.Join(filepath.Dir(manifestPath), "desktop.js"), `export const activate = () => {};`)
	item, err := pluginpkg.LoadManifest(manifestPath, "user")
	if err != nil {
		t.Fatal(err)
	}
	rt.Plugins = []pluginpkg.Plugin{item}
	rt.ExtensionSettings = &extensions.Settings{}
	if err := rt.ExtensionSettings.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	return New(rt, out), item, out
}

func TestPluginRegistryIntrospectReturnsKernelServices(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if srv.startupErr != nil {
		t.Fatalf("server startup: %v", srv.startupErr)
	}
	if err := srv.refreshExtensions(srv.currentExtensionConfig()); err != nil {
		t.Fatalf("refresh extensions: %v", err)
	}
	callPluginPackageRPC(t, srv, "introspect", MethodPluginRegistryIntrospect, nil)
	response := responseByID(t, parseOutput(t, out.String()), "introspect")
	if response["error"] != nil {
		t.Fatalf("introspect response = %+v", response)
	}
	result := remarshal[pluginhost.ServiceRegistrySnapshot](t, response["result"])
	if len(result.Services) == 0 {
		t.Fatal("introspect returned no services")
	}
	foundStorage, foundIntrospect := false, false
	for _, entry := range result.Services {
		switch entry.Service {
		case pluginhost.KernelStorageGetService:
			foundStorage = entry.Provider == "kernel" && entry.Kernel
		case pluginhost.KernelRegistryIntrospectService:
			foundIntrospect = entry.Provider == "kernel" && entry.Kernel
		}
	}
	if !foundStorage || !foundIntrospect {
		t.Fatalf("kernel services missing from snapshot: %+v", result.Services)
	}
}

func TestPluginExecutionsListReturnsLiveTable(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if srv.startupErr != nil {
		t.Fatalf("server startup: %v", srv.startupErr)
	}
	if err := srv.refreshExtensions(srv.currentExtensionConfig()); err != nil {
		t.Fatalf("refresh extensions: %v", err)
	}
	callPluginPackageRPC(t, srv, "executions", MethodPluginExecutionsList, nil)
	response := responseByID(t, parseOutput(t, out.String()), "executions")
	if response["error"] != nil {
		t.Fatalf("executions response = %+v", response)
	}
	result := remarshal[PluginExecutionsResult](t, response["result"])
	if result.Executions == nil {
		t.Fatal("executions must be an empty list, not null")
	}
}
