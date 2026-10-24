package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestSettingsRendererSelectedIndependently(t *testing.T) {
	testRendererSelection(t, "wuu.default-settings", "ui.settings")
}

func TestNavigationRendererSelectedIndependently(t *testing.T) {
	testRendererSelection(t, "wuu.default-navigation", "ui.navigation")
}

func testRendererSelection(t *testing.T, moduleID, serviceName string) {
	t.Helper()
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "external"}[replacement], func(t *testing.T) {
			ctx := context.Background()
			var settings RendererModule
			clients := []pluginhost.Client{}
			for _, module := range nativeRendererModules() {
				if module.ID == moduleID {
					settings = module
				}
				clients = append(clients, newRendererService(module, nil, nil))
			}
			if settings.ID == "" {
				t.Fatal("default settings module missing")
			}
			plugins := []pluginpkg.Plugin{}
			bindings := []composition.Binding{}
			want := moduleID
			if replacement {
				plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "alternate-settings", Desktop: &pluginpkg.DesktopSpec{Entry: "settings.js", Services: settings.Services}}, SubjectID: "plugin:user:alternate-settings", Fingerprint: "replacement-v1"}
				plugins = append(plugins, plugin)
				clients = append(clients, newRendererService(rendererModule(plugin), nil, nil))
				bindings = append(bindings, composition.Binding{Service: serviceName, Major: 1, Provider: plugin.ID})
				want = plugin.SubjectID
			}
			registry, err := pluginhost.NewServiceRegistryWithBindings(bindings)
			if err != nil {
				t.Fatal(err)
			}
			host := pluginhost.New(clients...)
			defer host.Close(ctx)
			host.AttachServiceRegistry(registry, registry.RegisterClients(clients...))
			if err := registry.ValidateBindings(); err != nil {
				t.Fatal(err)
			}
			if err := host.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			if err := host.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			owner := &Session{PluginHost: host, ActivePlugins: plugins}
			shell, surfaces := owner.ApplicationBindings()
			if shell == nil || shell.PluginID != defaultShellModule || surfaces[serviceName].PluginID != want {
				t.Fatalf("wrong independent selection: %+v %+v", shell, surfaces)
			}
			for _, client := range clients {
				if client.ID() == moduleID && !replacement || client.ID() == "alternate-settings" {
					raw, err := client.(pluginhost.ServiceInvoker).InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: serviceName, Method: "describe"})
					if err != nil {
						t.Fatal(err)
					}
					var description ApplicationShellBinding
					if err := json.Unmarshal(raw, &description); err != nil {
						t.Fatal(err)
					}
					if description.PluginID != want || description.ProviderID != serviceName {
						t.Fatalf("wrong description: %+v", description)
					}
					if err := client.Close(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			shell, surfaces = owner.ApplicationBindings()
			if shell == nil || surfaces[serviceName].PluginID != "" {
				t.Fatalf("stopped settings fell back: %+v", surfaces)
			}
		})
	}
}
