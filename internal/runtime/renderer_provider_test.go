package runtime

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
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
		{name: "default", modules: []string{defaultShellModule}, want: defaultShellModule},
		{name: "external", modules: []string{defaultShellModule, "research"}, provider: "research", want: plugin.SubjectID},
		{name: "disabled", modules: []string{}},
		{name: "ambiguous", modules: []string{defaultShellModule, "research"}, invalid: true},
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
			modules := []string{defaultShellModule, "consumer"}
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
