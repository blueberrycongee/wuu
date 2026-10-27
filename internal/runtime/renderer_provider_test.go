package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestRendererShellUsesAuthoritativeComposition(t *testing.T) {
	plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "research", Desktop: &pluginpkg.DesktopSpec{
		Entry: "desktop.mjs", Services: []pluginhost.ServiceDescriptor{applicationShellDescriptor()},
	}}, SubjectID: "plugin:user:research", Fingerprint: "research-v1"}
	for _, tc := range []struct {
		name     string
		modules  []string
		provider string
		want     string
		invalid  bool
	}{
		{name: "default", modules: []string{defaultApplicationModule, defaultShellModule}, want: defaultShellModule},
		{name: "external", modules: []string{defaultApplicationModule, defaultShellModule, "research"}, provider: "research", want: plugin.SubjectID},
		{name: "disabled", modules: []string{}},
		{name: "ambiguous", modules: []string{defaultApplicationModule, defaultShellModule, "research"}, invalid: true},
		{name: "missing", modules: []string{}, provider: "research", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := &composition.Config{Version: composition.Version, Modules: &tc.modules}
			if tc.provider != "" {
				selection.Services = []composition.Binding{{Service: "ui.shell", Major: 1, Provider: tc.provider}}
			}
			host, _, err := buildPluginHost([]pluginpkg.Plugin{plugin}, t.TempDir(), "test", t.TempDir(), t.TempDir(), nil, startPluginClient, nil, nil, selection, "")
			if tc.invalid {
				if err == nil {
					host.Close(context.Background())
					t.Fatal("invalid shell composition accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			s := &Session{PluginHost: host, ActivePlugins: []pluginpkg.Plugin{plugin}}
			binding := s.ApplicationShellBinding()
			if tc.want == "" {
				if binding != nil {
					t.Fatalf("disabled shell fell back: %+v", binding)
				}
			} else if binding == nil || binding.PluginID != tc.want || binding.ProviderID != "ui.shell" || binding.Generation == "" {
				t.Fatalf("selected shell: %+v", binding)
			}
		})
	}
}

func TestRendererConsumerWithoutProvidedServicesJoinsGraph(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "desktop-only", true: "mixed"}[mixed], func(t *testing.T) {
			item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "consumer", Desktop: &pluginpkg.DesktopSpec{Entry: "desktop.mjs", RequiredServices: []pluginhost.ServiceRequirement{{Name: "ui.shell", MajorVersion: 1, Required: true}}}}, SubjectID: "plugin:user:consumer", Fingerprint: "consumer-v1"}
			if mixed {
				item.Runtime = &pluginpkg.RuntimeSpec{Command: "fixture"}
			}
			modules := []string{defaultApplicationModule, defaultShellModule, "consumer"}
			start := func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
				return newRendererService(RendererModule{ID: cfg.ID}, nil, nil), nil
			}
			host, _, err := buildPluginHost([]pluginpkg.Plugin{item}, t.TempDir(), "test", t.TempDir(), t.TempDir(), nil, start, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "")
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			result, callErr := host.CallService(context.Background(), item.ID, "ui.shell", "describe", nil)
			if callErr != nil || len(result) == 0 {
				t.Fatalf("renderer dependency: %s %v", result, callErr)
			}
		})
	}
}

// Surface selection is independent of the shell and excludes unselected or
// stopped providers even when their renderer package remains discoverable.
func TestRendererSurfacesUseSelectedGraph(t *testing.T) {
	ctx := context.Background()
	descriptor := pluginhost.ServiceDescriptor{Name: "ui.navigation", Version: "1.0.0", Methods: []pluginhost.ServiceMethodDescriptor{{Name: "describe", InputSchema: "ui.navigation.describe.input.v1", OutputSchema: "ui.navigation.describe.output.v1"}}}
	plugins := []pluginpkg.Plugin{}
	for _, id := range []string{"first", "selected"} {
		plugins = append(plugins, pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: id, Desktop: &pluginpkg.DesktopSpec{Entry: "desktop.mjs", Services: []pluginhost.ServiceDescriptor{descriptor}}}, SubjectID: "plugin:user:" + id, Fingerprint: id + "-generation"})
	}
	modules := []string{defaultApplicationModule, defaultShellModule, "first", "selected"}
	selection := &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: descriptor.Name, Major: 1, Provider: "selected"}}}
	host, _, err := buildPluginHost(plugins, t.TempDir(), "test", t.TempDir(), t.TempDir(), nil, startPluginClient, nil, nil, selection, "")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(ctx)
	if err := host.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Session{PluginHost: host, ActivePlugins: plugins}
	shell, surfaces := s.ApplicationBindings()
	if shell == nil || shell.PluginID != defaultShellModule {
		t.Fatalf("shell: %+v", shell)
	}
	binding, found := surfaces[descriptor.Name]
	if !found || len(surfaces) != 2 || binding.PluginID != "plugin:user:selected" || binding.Generation != "selected-generation" || binding.ProviderID != descriptor.Name {
		t.Fatalf("surface bindings: %+v", surfaces)
	}
	if err := host.Close(ctx); err != nil {
		t.Fatal(err)
	}
	shell, surfaces = s.ApplicationBindings()
	if shell != nil || len(surfaces) != 0 {
		t.Fatalf("stopped composition remained selectable: %+v %+v", shell, surfaces)
	}
}

func TestMixedRendererRequirementsUseOneValidatedUnion(t *testing.T) {
	process := newRendererService(RendererModule{ID: "mixed"}, []pluginhost.ServiceRequirement{{Name: "automation.scheduler", MajorVersion: 1}}, nil)
	wrapper := newRendererService(RendererModule{ID: "mixed"}, []pluginhost.ServiceRequirement{{Name: "automation.scheduler", MajorVersion: 1, Required: true}}, process)
	got := wrapper.RequiredServices()
	if len(got) != 1 || !got[0].Required {
		t.Fatalf("runtime and renderer dependency not merged: %+v", got)
	}
	if process.RequiredServices()[0].Required {
		t.Fatal("wrapper mutated its process declaration")
	}
	conflicting := newRendererService(RendererModule{ID: "mixed"}, []pluginhost.ServiceRequirement{{Name: "automation.scheduler", MajorVersion: 2, Required: true}}, process)
	if len(conflicting.RequiredServices()) != 2 {
		t.Fatal("incompatible majors were silently merged")
	}
}

// A desktop entry can consume its package's runtime service without introducing
// a package lifecycle cycle. A backend declaration of the same dependency is a
// real self-delegate and must still fail, even when merged with desktop needs.
func TestMixedRendererOwnRuntimeService(t *testing.T) {
	for _, backendRequires := range []bool{false, true} {
		for _, scoped := range []bool{false, true} {
			name := fmt.Sprintf("backend-requires=%t/scoped=%t", backendRequires, scoped)
			t.Run(name, func(t *testing.T) {
				item := testRuntimePlugin("mixed")
				requirements := []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
				item.Desktop = &pluginpkg.DesktopSpec{Entry: "desktop.mjs", RequiredServices: requirements}
				process := &fakeServicePluginClient{id: item.ID, provided: []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}}
				if backendRequires {
					process.required = requirements
				}
				modules := []string{item.ID}
				selection := &composition.Config{Version: composition.Version, Modules: &modules}
				if scoped {
					selection.Services = []composition.Binding{{Consumer: item.ID, Service: "search.provider", Major: 1, Provider: item.ID}}
				}
				start := func(context.Context, pluginhost.ProcessConfig) (pluginhost.Client, error) { return process, nil }
				host, _, err := buildPluginHost([]pluginpkg.Plugin{item}, t.TempDir(), "mixed", t.TempDir(), t.TempDir(), nil, start, nil, nil, selection, "")
				if backendRequires {
					if err == nil {
						host.Close(context.Background())
						t.Fatal("runtime self-delegate was accepted")
					}
					if !strings.Contains(err.Error(), "cycle") && !strings.Contains(err.Error(), "itself") {
						t.Fatalf("runtime self-delegate failed for the wrong reason: %v", err)
					}
					if process.active {
						t.Fatal("cyclic backend activated")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer host.Close(context.Background())
				if err := host.Activate(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, err := host.CallService(context.Background(), item.ID, "search.provider", "query", nil); err != nil {
					t.Fatalf("desktop cannot consume own runtime service: %v", err)
				}
				for _, dependency := range host.Dependencies()[item.ID] {
					if dependency.Provider == item.ID {
						t.Fatalf("intra-package desktop call became lifecycle edge: %+v", dependency)
					}
				}
			})
		}
	}
}

func TestMixedRendererOwnStateService(t *testing.T) {
	item := testRuntimePlugin("mixed-state")
	item.Desktop = &pluginpkg.DesktopSpec{Entry: "desktop.mjs", RequiredServices: []pluginhost.ServiceRequirement{{Name: persistence.ServiceName, MajorVersion: 1, Required: true}}}
	modules := []string{item.ID}
	selection := &composition.Config{Version: composition.Version, Modules: &modules}
	start := func(context.Context, pluginhost.ProcessConfig) (pluginhost.Client, error) {
		return persistence.NewService(item.ID, t.TempDir(), "sqlite"), nil
	}
	host, _, err := buildPluginHost([]pluginpkg.Plugin{item}, t.TempDir(), "mixed-state", t.TempDir(), t.TempDir(), nil, start, nil, nil, selection, "")
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := host.CallService(context.Background(), item.ID, persistence.ServiceName, "read", json.RawMessage(`{"namespace":"mixed.renderer"}`)); err != nil {
		t.Fatalf("desktop cannot consume own admitted state service: %v", err)
	}
}
