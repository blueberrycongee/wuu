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
