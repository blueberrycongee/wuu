package appserver

import (
	"context"
	"testing"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestRendererDefaultIdentityIsOwnedByConnection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	item := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "renderer", Desktop: &pluginpkg.DesktopSpec{Entry: "desktop.mjs"}}, SubjectID: "plugin:user:renderer", Fingerprint: "renderer-v1"}
	rt.Plugins = []pluginpkg.Plugin{item}
	rt.ActivePlugins = []pluginpkg.Plugin{item}
	client := &pluginClientRequestTestRuntime{id: item.ID}
	host := pluginhost.New(client)
	registry, conflicts := pluginhost.BuildServiceRegistry(client)
	registry.RegisterRenderer(item.ID, item.Fingerprint)
	registry.SetComposition(pluginhost.CompositionSnapshot{Modules: []pluginhost.CompositionModuleSnapshot{{ID: item.ID}}})
	host.AttachServiceRegistry(registry, conflicts)
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.PluginHost = host
	generation := rt.RetainPluginGeneration()
	defer rt.ReleasePluginGeneration(generation)
	firstOut, secondOut := &lockedBuffer{}, &lockedBuffer{}
	first, second := New(rt, firstOut), New(rt, secondOut)
	defer first.Close()
	defer second.Close()
	params := PluginDesktopActivationParams{ID: item.SubjectID, Fingerprint: item.Fingerprint, GraphGeneration: rt.ExtensionSnapshot().GenerationID, RendererActivationReport: pluginhost.RendererActivationReport{Activation: 1, Phase: "registration", Status: "active"}}
	for i, srv := range []*Server{first, second} {
		callPluginPackageRPC(t, srv, "register", MethodPluginDesktopActivation, params)
		out := []*lockedBuffer{firstOut, secondOut}[i]
		if response := responseByID(t, parseOutput(t, out.String()), "register"); response["error"] != nil {
			t.Fatalf("register: %+v", response)
		}
	}
	clients := registry.Snapshot(0).Composition.Modules[0].Renderer.Clients
	if len(clients) != 2 || clients[0].RendererID == clients[1].RendererID {
		t.Fatalf("renderer connections: %+v", clients)
	}
	first.Close()
	clients = registry.Snapshot(0).Composition.Modules[0].Renderer.Clients
	if len(clients) != 1 {
		t.Fatalf("closing one connection removed other identity: %+v", clients)
	}
}
