package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/stretchr/testify/require"
)

// A packed-SDK-only adapter changes one real settings method, forwarding every
// other method through the selected graph. No native implementation is copied.
func TestProviderDelegationSDKProcess(t *testing.T) {
	fixture := os.Getenv("WUU_DELEGATION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/provider-delegation-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, nested := range []bool{false, true} {
		name := "native-base"
		if nested {
			name = "external-base"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			root, home := t.TempDir(), t.TempDir()
			adapter := testRuntimePlugin("independent.settings-adapter")
			adapter.Root = fixture
			adapter.Runtime.Command = node
			adapter.Runtime.Args = []string{"settings-decorator.js"}
			modules := []string{pluginsettings.Module, adapter.ID, "independent.reader"}
			plugins := []pluginpkg.Plugin{adapter}
			bindings := []composition.Binding{{Service: pluginsettings.ServiceName, Major: 1, Provider: adapter.ID}, {Consumer: adapter.ID, Service: pluginsettings.ServiceName, Major: 1, Provider: pluginsettings.Module}}
			if nested {
				inner := testRuntimePlugin("independent.inner-adapter")
				inner.Root = fixture
				inner.Runtime.Command = node
				inner.Runtime.Args = []string{"settings-decorator.js"}
				plugins = append(plugins, inner)
				modules = append(modules, inner.ID)
				bindings[1].Provider = inner.ID
				bindings = append(bindings, composition.Binding{Consumer: inner.ID, Service: pluginsettings.ServiceName, Major: 1, Provider: pluginsettings.Module})
			}
			reader := &delegationSettingsReader{state: pluginhost.StatePrepared}
			selection := &composition.Config{Version: composition.Version, Modules: &modules, Services: bindings}
			host, _, err := buildPluginHost(plugins, root, "delegation", home, t.TempDir(), nil, startPluginClient, nil, nil, selection, "", NativeProviderFactory{ID: reader.ID(), Build: func(NativeProviderContext) (pluginhost.Client, error) { return reader, nil }})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, host.Close(ctx)) })
			require.NoError(t, host.Activate(ctx))
			var settings pluginsettings.SettingsView
			address := pluginsettings.ReadRequest{Subject: "plugin:downstream", Scope: pluginsettings.ScopeUser}
			call := func(method string, input, output any) {
				t.Helper()
				raw, err := json.Marshal(input)
				require.NoError(t, err)
				result, callErr := host.CallService(ctx, reader.ID(), pluginsettings.ServiceName, method, raw)
				if callErr != nil {
					t.Fatal(callErr)
				}
				reflect.ValueOf(output).Elem().SetZero()
				require.NoError(t, json.Unmarshal(result, output))
			}
			call("read", address, &settings)
			require.JSONEq(t, `"compact"`, string(settings.Document.Values["appearance"]))
			call("update", pluginsettings.UpdateRequest{ReadRequest: address, ExpectedRevision: settings.Revision, Fingerprint: "public-fixture", Values: map[string]json.RawMessage{"label": json.RawMessage(`"original persistence"`)}}, &settings)
			require.NotContains(t, settings.Document.Values, "appearance", "non-overridden update must retain base semantics")
			call("read", address, &settings)
			require.Contains(t, settings.Document.Values, "appearance")
			require.JSONEq(t, `"original persistence"`, string(settings.Document.Values["label"]))
			persisted, err := pluginsettings.Read(home, root, address.Subject, address.Scope)
			require.NoError(t, err)
			require.NotContains(t, persisted.Values, "appearance", "read decoration must not copy or rewrite base state")
			var state pluginsettings.StateView
			call("read-state", address, &state)
			call("update-state", pluginsettings.UpdateStateRequest{ReadRequest: address, ExpectedRevision: state.Revision, Values: map[string]string{"resume": "opaque-state"}}, &state)
			call("read-state", address, &state)
			require.Equal(t, "opaque-state", state.Document.Values["resume"])
			stored, err := pluginsettings.ReadState(home, root, address.Subject, address.Scope)
			require.NoError(t, err)
			require.Equal(t, state.Document, stored)
			snapshot := host.ServiceRegistry().Snapshot(0)
			require.Len(t, snapshot.Services, len(bindings))
			selected, found := host.ServiceRegistry().ProviderPluginIDFor(adapter.ID, pluginsettings.ServiceName, 1)
			require.True(t, found)
			require.Equal(t, bindings[1].Provider, selected)
			for _, binding := range bindings {
				if binding.Consumer != "" {
					require.Contains(t, host.Dependencies()[binding.Consumer], pluginhost.Dependency{Provider: binding.Provider, Required: true, Service: binding.Service, Major: binding.Major})
				}
			}
			outcome, found := host.RetirePlugin(ctx, pluginsettings.Module, errors.New("delegate removed"))
			require.True(t, found)
			require.NoError(t, outcome.Err)
			_, callErr := host.CallService(ctx, reader.ID(), pluginsettings.ServiceName, "read", json.RawMessage(`{"subject":"plugin:downstream","scope":"user"}`))
			require.Error(t, callErr)
		})
	}
}

type delegationSettingsReader struct{ state pluginhost.State }

func (*delegationSettingsReader) ID() string { return "independent.reader" }
func (c *delegationSettingsReader) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: c.state}
}
func (*delegationSettingsReader) ProvidedServices() []pluginhost.ServiceDescriptor { return nil }
func (*delegationSettingsReader) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: pluginsettings.ServiceName, MajorVersion: 1, Required: true}}
}
func (c *delegationSettingsReader) Activate(context.Context) error {
	c.state = pluginhost.StateActive
	return nil
}
func (c *delegationSettingsReader) Close(context.Context) error {
	c.state = pluginhost.StateStopped
	return nil
}

// State has one durable authority per composition. Adapters are implementation
// nodes before admission; ordinary consumers cannot route around that guard.
func TestStateProviderDelegationAuthority(t *testing.T) {
	for _, scenario := range []string{"chain", "ordinary-consumer-bypass", "cycle"} {
		t.Run(scenario, func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			modules := []string{persistence.SQLiteModule, "outer", "inner", "state.reader"}
			bindings := []composition.Binding{
				{Service: persistence.ServiceName, Major: 1, Provider: "outer"},
				{Consumer: "outer", Service: persistence.ServiceName, Major: 1, Provider: "inner"},
				{Consumer: "inner", Service: persistence.ServiceName, Major: 1, Provider: persistence.SQLiteModule},
			}
			if scenario == "ordinary-consumer-bypass" {
				bindings = append(bindings, composition.Binding{Consumer: "state.reader", Service: persistence.ServiceName, Major: 1, Provider: persistence.SQLiteModule})
			}
			if scenario == "cycle" {
				bindings[2].Provider = "outer"
			}
			var registry *pluginhost.ServiceRegistry
			var reader *delegationDocumentAdapter
			factories := []NativeProviderFactory{}
			for _, id := range []string{"outer", "inner", "state.reader"} {
				factories = append(factories, NativeProviderFactory{ID: id, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
					adapter := &delegationDocumentAdapter{id: id, registry: input.Registry, state: pluginhost.StatePrepared}
					registry = input.Registry
					if id == "state.reader" {
						reader = adapter
					}
					return adapter, nil
				}})
			}
			host, _, err := buildPluginHost(nil, root, "delegation", home, t.TempDir(), nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules, Services: bindings}, "", factories...)
			if scenario != "chain" {
				require.Error(t, err)
				if host != nil {
					host.Close(context.Background())
				}
				return
			}
			require.NoError(t, err)
			defer host.Close(context.Background())
			require.NoError(t, host.Activate(context.Background()))
			documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				result, err := registry.Call(ctx, reader.ID(), pluginhost.ServiceCallParams{Service: persistence.ServiceName, Method: method, Params: raw})
				if err != nil {
					return nil, err
				}
				return result, nil
			})
			before, err := documents.Read(context.Background(), persistence.Query{Namespace: "acceptance.delegation"})
			require.NoError(t, err)
			receipt, err := documents.Commit(context.Background(), persistence.Transaction{ID: "through-two-adapters", ExpectedRevision: before.Revision, Writes: []persistence.Write{{Namespace: "acceptance.delegation", Key: "one", Value: json.RawMessage(`1`)}}})
			require.NoError(t, err)
			require.Equal(t, before.Revision+1, receipt.Revision)
			view, err := documents.Read(context.Background(), persistence.Query{Namespace: "acceptance.delegation"})
			require.NoError(t, err)
			require.Len(t, view.Documents, 1)
		})
	}
}

type delegationDocumentAdapter struct {
	id       string
	registry *pluginhost.ServiceRegistry
	state    pluginhost.State
}

func (c *delegationDocumentAdapter) ID() string { return c.id }
func (c *delegationDocumentAdapter) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: c.state}
}
func (c *delegationDocumentAdapter) ProvidedServices() []pluginhost.ServiceDescriptor {
	if c.id == "state.reader" {
		return nil
	}
	return persistence.NewService("descriptor", "", "sqlite").ProvidedServices()
}
func (c *delegationDocumentAdapter) RequiredServices() []pluginhost.ServiceRequirement {
	return []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}
}
func (c *delegationDocumentAdapter) Activate(context.Context) error {
	c.state = pluginhost.StateActive
	return nil
}
func (c *delegationDocumentAdapter) Close(context.Context) error {
	c.state = pluginhost.StateStopped
	return nil
}
func (c *delegationDocumentAdapter) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	raw, err := c.registry.Call(ctx, c.id, pluginhost.ServiceCallParams{Service: params.Service, Method: params.Method, Params: params.Params})
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func TestStateProviderDelegationInheritance(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{"outer": true, "inner": true, "state.reader": true}, Services: []composition.Binding{
		{Service: persistence.ServiceName, Major: 1, Provider: "outer"},
		{Consumer: "outer", Service: persistence.ServiceName, Major: 1, Provider: "inner"},
		{Consumer: "inner", Service: persistence.ServiceName, Major: 1, Provider: persistence.SQLiteModule},
	}}}}
	factories := []NativeProviderFactory{}
	for _, id := range []string{"outer", "inner", "state.reader"} {
		factories = append(factories, NativeProviderFactory{ID: id, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return &delegationDocumentAdapter{id: id, registry: input.Registry, state: pluginhost.StatePrepared}, nil
		}})
	}
	catalog := []pluginpkg.Plugin{}
	owner, err := NewSession(Options{RootDir: t.TempDir(), WuuHome: t.TempDir(), HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true, NativeProviders: factories})
	require.NoError(t, err)
	defer owner.Cleanup()
	documents := func(host *pluginhost.Host) *persistence.Client {
		return persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			raw, err := host.CallService(ctx, "state.reader", persistence.ServiceName, method, params)
			if err != nil {
				return nil, err
			}
			return raw, nil
		})
	}
	parent := documents(owner.PluginHost)
	before, err := parent.Read(context.Background(), persistence.Query{Namespace: "acceptance.inheritance"})
	require.NoError(t, err)
	_, err = parent.Commit(context.Background(), persistence.Transaction{ID: "parent-marker", ExpectedRevision: before.Revision, Writes: []persistence.Write{{Namespace: "acceptance.inheritance", Key: "parent", Value: json.RawMessage(`true`)}}})
	require.NoError(t, err)
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	defer kernel.compositions.close()
	for _, mode := range []string{"isolated", "parent"} {
		raw, err := kernel.compositions.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: "create", Params: json.RawMessage(`{"inherit":true,"state":"` + mode + `"}`)})
		require.NoError(t, err)
		var view pluginhost.CompositionView
		require.NoError(t, json.Unmarshal(raw, &view))
		child := kernel.compositions.scopes[view.ID].runtime.PluginHost
		rows, err := documents(child).Read(context.Background(), persistence.Query{Namespace: "acceptance.inheritance"})
		require.NoError(t, err)
		if mode == "isolated" {
			require.Empty(t, rows.Documents)
			target, found := child.ServiceRegistry().ProviderPluginIDFor("outer", persistence.ServiceName, 1)
			require.True(t, found)
			require.Equal(t, "inner", target)
			target, found = child.ServiceRegistry().ProviderPluginIDFor("inner", persistence.ServiceName, 1)
			require.True(t, found)
			require.Equal(t, persistence.SQLiteModule, target)
		} else {
			require.Len(t, rows.Documents, 1)
			for _, route := range view.Services.Services {
				if route.Service == persistence.ServiceName {
					require.Empty(t, route.Consumer, "imported state retained stale adapter routes")
				}
			}
		}
	}
}
