package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/runtime"
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
	srv, item, out := newPluginStateTestServer(t, func(id string) pluginhost.Client {
		return &isolatedFailureClient{id: id, invokeErr: errors.New("observer boom")}
	})
	if err := srv.notifyPluginTurnCompleted(testRuntimeState(t, srv.rt), context.Background(), pluginhost.AgentTurnCompletedInput{}); err != nil {
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

func TestPluginDiagnosticsAdmitInventoryRenderersAndKeepFailures(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	preparePluginRuntimePathsForTest(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	cfg := srv.currentExtensionConfig()
	cfg.Composition = composition.DefaultConfig("desktop")
	if err := srv.refreshExtensions(cfg); err != nil {
		t.Fatal(err)
	}
	modules := rt.NativeRendererModules()
	if len(modules) == 0 {
		t.Fatal("no admitted renderer modules")
	}
	for _, module := range modules {
		t.Run(module.ID, func(t *testing.T) {
			params := PluginDiagnosticsParams{ID: module.SubjectID, Fingerprint: module.Fingerprint}
			callPluginPackageRPC(t, srv, module.ID, MethodPluginDiagnosticsList, params)
			response := responseByID(t, parseOutput(t, out.String()), module.ID)
			if response["error"] != nil {
				t.Fatalf("admitted renderer diagnostics failed: %+v", response)
			}
			result := remarshal[PluginDiagnosticsResult](t, response["result"])
			if result.ID != module.SubjectID || result.Diagnostics == nil || len(result.Diagnostics) != 0 {
				t.Fatalf("healthy renderer diagnostics: %+v", result)
			}
			params.Fingerprint = "stale"
			callPluginPackageRPC(t, srv, module.ID+"-stale", MethodPluginDiagnosticsList, params)
			if responseByID(t, parseOutput(t, out.String()), module.ID+"-stale")["error"] == nil {
				t.Fatal("stale renderer fingerprint accepted")
			}
		})
	}
	module := modules[0]
	registry := rt.PluginHost.ServiceRegistry()
	for _, phase := range []string{"registration", "render", "cleanup"} {
		if err := registry.ReportRenderer(module.ID, module.Fingerprint, "test-window", pluginhost.RendererActivationReport{
			Activation: 1, Phase: phase, Status: "failed", Message: phase + " failure",
		}); err != nil {
			t.Fatal(err)
		}
	}
	callPluginPackageRPC(t, srv, "renderer-failures", MethodPluginDiagnosticsList, PluginDiagnosticsParams{ID: module.SubjectID, Fingerprint: module.Fingerprint})
	response := responseByID(t, parseOutput(t, out.String()), "renderer-failures")
	if response["error"] != nil {
		t.Fatalf("failed renderer diagnostics unavailable: %+v", response)
	}
	result := remarshal[PluginDiagnosticsResult](t, response["result"])
	if len(result.Diagnostics) != 3 {
		t.Fatalf("renderer failures were hidden: %+v", result)
	}
	for _, diagnostic := range result.Diagnostics {
		if !strings.Contains(diagnostic.Contribution, "test-window") || !strings.Contains(diagnostic.Message, "failure") {
			t.Fatalf("renderer failure lost attribution: %+v", diagnostic)
		}
	}
	for id, params := range map[string]PluginDiagnosticsParams{
		"missing": {ID: "unknown-plugin", Fingerprint: module.Fingerprint},
		"empty":   {ID: module.SubjectID},
	} {
		callPluginPackageRPC(t, srv, id, MethodPluginDiagnosticsList, params)
		if responseByID(t, parseOutput(t, out.String()), id)["error"] == nil {
			t.Fatalf("invalid diagnostic identity accepted: %s", id)
		}
	}
	// Admission retains the original graph even if Session publishes a new host.
	srv.Close()
	srv = &Server{rt: rt, out: out}
	original := rt.PluginHost
	rt.PluginHost = pluginhost.New()
	defer func() { rt.PluginHost = original }()
	callPluginPackageRPC(t, srv, "retained-renderer", MethodPluginDiagnosticsList, PluginDiagnosticsParams{ID: module.SubjectID, Fingerprint: module.Fingerprint})
	retained := responseByID(t, parseOutput(t, out.String()), "retained-renderer")
	if retained["error"] != nil || len(remarshal[PluginDiagnosticsResult](t, retained["result"]).Diagnostics) != 3 {
		t.Fatalf("renderer diagnostics escaped their admitted graph: %+v", retained)
	}
}

func TestPluginDiagnosticsIncludePackageRendererFailures(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t, func(id string) pluginhost.Client {
		return &isolatedFailureClient{id: id, invokeErr: errors.New("observer failure")}
	})
	if err := srv.notifyPluginTurnCompleted(testRuntimeState(t, srv.rt), context.Background(), pluginhost.AgentTurnCompletedInput{}); err != nil {
		t.Fatal(err)
	}
	registry := srv.rt.PluginHost.ServiceRegistry()
	registry.RegisterRenderer(item.ID, item.Fingerprint)
	registry.SetComposition(pluginhost.CompositionSnapshot{Modules: []pluginhost.CompositionModuleSnapshot{{ID: item.ID}}})
	if err := registry.ReportRenderer(item.ID, item.Fingerprint, "package-window", pluginhost.RendererActivationReport{
		Activation: 1, Phase: "registration", Status: "failed", Message: "package registration failure",
	}); err != nil {
		t.Fatal(err)
	}
	callPluginPackageRPC(t, srv, "combined", MethodPluginDiagnosticsList, PluginDiagnosticsParams{ID: item.SubjectID, Fingerprint: item.Fingerprint})
	result := remarshal[PluginDiagnosticsResult](t, responseByID(t, parseOutput(t, out.String()), "combined")["result"])
	if len(result.Diagnostics) != 2 || !strings.Contains(result.Diagnostics[0].Message, "observer failure") || result.Diagnostics[1].Message != "package registration failure" {
		t.Fatalf("runtime and renderer failures must both remain visible: %+v", result)
	}
}

func TestPluginDiagnosticsKeepAdmissionGeneration(t *testing.T) {
	var observer *diagnosticAdmissionClient
	srv, item, out := newPluginStateTestServer(t, func(id string) pluginhost.Client {
		observer = &diagnosticAdmissionClient{isolatedFailureClient: &isolatedFailureClient{id: id, invokeErr: errors.New("original observer failure")}}
		return observer
	})
	if err := srv.notifyPluginTurnCompleted(testRuntimeState(t, srv.rt), context.Background(), pluginhost.AgentTurnCompletedInput{}); err != nil {
		t.Fatal(err)
	}
	// Exercise dispatch deterministically without concurrent discovery watchers.
	srv.Close()
	srv = &Server{rt: srv.rt, out: out}
	original := srv.rt.PluginHost
	replacement := pluginhost.New()
	defer replacement.Close(context.Background())
	defer func() { srv.rt.PluginHost = original }()
	// Publish another host after fingerprint admission has captured the original.
	observer.onStatus = func() { srv.rt.PluginHost = replacement }
	callPluginPackageRPC(t, srv, "diagnostics", MethodPluginDiagnosticsList, PluginDiagnosticsParams{ID: item.SubjectID, Fingerprint: item.Fingerprint})
	response := responseByID(t, parseOutput(t, out.String()), "diagnostics")
	if response["error"] != nil {
		t.Fatalf("diagnostics failed: %+v", response)
	}
	result := remarshal[PluginDiagnosticsResult](t, response["result"])
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "original observer failure") {
		t.Fatalf("diagnostics escaped their admission generation: %+v", result)
	}
}

type diagnosticAdmissionClient struct {
	*isolatedFailureClient
	onStatus func()
}

func (c *diagnosticAdmissionClient) Status() pluginhost.Status {
	if callback := c.onStatus; callback != nil {
		c.onStatus = nil
		callback()
	}
	return c.isolatedFailureClient.Status()
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

func newPluginStateTestServer(t *testing.T, runtimeClients ...func(string) pluginhost.Client) (*Server, pluginpkg.Plugin, *lockedBuffer) {
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
	// Complete runtime construction before New starts recovery watchers.
	var clients []pluginhost.Client
	for _, create := range runtimeClients {
		clients = append(clients, create(item.ID))
	}
	settings := pluginsettings.NewService(pluginsettings.Module, rt.WuuHome, rt.RootDir)
	credentials := credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) {
		return credentialstore.NewFileStore(filepath.Join(rt.WuuHome, "test-credentials.json")), nil
	})
	clients = append(clients, settings, credentials, &stateHandlerConsumer{})
	rt.PluginHost = pluginhost.New(clients...)
	registry, conflicts := pluginhost.BuildServiceRegistry(clients...)
	rt.PluginHost.AttachServiceRegistry(registry, conflicts)
	if err := rt.PluginHost.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	return srv, item, out
}

func TestPluginRegistryIntrospectReturnsKernelServices(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	preparePluginRuntimePathsForTest(t, rt)
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
			foundStorage = entry.Provider == "kernel"
		case pluginhost.KernelRegistryIntrospectService:
			foundIntrospect = entry.Provider == "kernel"
		}
	}
	if !foundStorage || !foundIntrospect {
		t.Fatalf("kernel services missing from snapshot: %+v", result.Services)
	}
}

func TestPluginExecutionsListReturnsLiveTable(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	preparePluginRuntimePathsForTest(t, rt)
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

type stateHandlerConsumer struct{}

func (*stateHandlerConsumer) ID() string { return runtime.StateServicesConsumer }
func (*stateHandlerConsumer) Status() pluginhost.Status {
	return pluginhost.Status{ID: runtime.StateServicesConsumer, State: pluginhost.StateActive}
}
func (*stateHandlerConsumer) Close(context.Context) error                      { return nil }
func (*stateHandlerConsumer) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*stateHandlerConsumer) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginsettings.ServiceName, MajorVersion: 1, Required: true}, {Name: credentialstore.ServiceName, MajorVersion: 1, Required: true}}
}

func TestPluginStateHandlersStopWhenSelectedProviderIsRevoked(t *testing.T) {
	srv, item, out := newPluginStateTestServer(t)
	callPluginPackageRPC(t, srv, "before", MethodPluginStorageSet, PluginStorageSetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: PluginValueScopeUser, Key: "entry", Value: "old"})
	if responseByID(t, parseOutput(t, out.String()), "before")["error"] != nil {
		t.Fatal("initial selected write failed")
	}
	srv.rt.PluginHost.ServiceRegistry().RevokePlugin(context.Background(), pluginsettings.Module)
	callPluginPackageRPC(t, srv, "after", MethodPluginStorageSet, PluginStorageSetParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: PluginValueScopeUser, Key: "entry", Value: "new"})
	if responseByID(t, parseOutput(t, out.String()), "after")["error"] == nil {
		t.Fatal("revoked provider fell back to local settings")
	}
	document, err := pluginsettings.ReadState(srv.rt.WuuHome, srv.rt.RootDir, item.SubjectID, pluginsettings.ScopeUser)
	if err != nil || document.Values["entry"] != "old" {
		t.Fatalf("disabled provider wrote local state: %+v %v", document, err)
	}
}

func TestDesktopInventoryKeepsAdmittedGraphGeneration(t *testing.T) {
	srv, item, _ := newPluginStateTestServer(t)
	snapshot := srv.rt.ExtensionSnapshot()
	snapshot.GenerationID = "admitted-graph"
	for _, record := range srv.extensionInventory(snapshot) {
		if record.ID == item.SubjectID && record.Desktop != nil {
			if record.GraphGeneration != "admitted-graph" {
				t.Fatal("desktop record lost its snapshot graph identity")
			}
			return
		}
	}
	t.Fatal("desktop inventory record missing")
}
