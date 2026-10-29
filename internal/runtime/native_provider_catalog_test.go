package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
)

// Native composition entries are replaceable constructors, not privileged
// registrations that survive an embedding application's explicit selection.
func TestNativeProviderCatalogReplacement(t *testing.T) {
	for _, selected := range []bool{true, false} {
		t.Run(map[bool]string{true: "selected", false: "removed"}[selected], func(t *testing.T) {
			root := t.TempDir()
			home, state := testWorkspaceState(t, root)
			modules := []string{"example.reader"}
			if selected {
				modules = append(modules, promptservice.NativeModule)
			}
			builds := 0
			factory := NativeProviderFactory{ID: promptservice.NativeModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
				builds++
				return &driverProviderClient{id: promptservice.NativeModule, descriptor: promptservice.NewService().ProvidedServices()[0], invoke: func(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
					return json.RawMessage(`{"content":"independent implementation","sections":[]}`), nil
				}}, nil
			}}
			reader := NativeProviderFactory{ID: "example.reader", Build: func(NativeProviderContext) (pluginhost.Client, error) {
				return &runtimeServiceConsumer{id: "example.reader", state: pluginhost.StatePrepared, requirements: []pluginhost.ServiceRequirement{{Name: promptservice.ServiceName, MajorVersion: 1}}}, nil
			}}
			host, _, err := buildPluginHost(nil, root, "catalog", home, state, nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "", factory, reader)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = host.Close(context.Background()) })
			if err := host.Activate(context.Background()); err != nil {
				t.Fatal(err)
			}
			raw, callErr := host.CallService(context.Background(), "example.reader", promptservice.ServiceName, "assemble", json.RawMessage(`{}`))
			if selected {
				if builds != 1 || callErr != nil || !strings.Contains(string(raw), "independent implementation") {
					t.Fatalf("replacement: builds=%d result=%s err=%v", builds, raw, callErr)
				}
			} else if builds != 0 || callErr == nil {
				t.Fatalf("removed provider constructed or silently substituted: builds=%d result=%s err=%v", builds, raw, callErr)
			}
		})
	}
}

func TestNativeProviderCatalogRejectsDuplicateFactoryBeforeBuild(t *testing.T) {
	root := t.TempDir()
	home, state := testWorkspaceState(t, root)
	builds := 0
	factory := NativeProviderFactory{ID: "example.duplicate", Build: func(NativeProviderContext) (pluginhost.Client, error) {
		builds++
		return nil, errors.New("must not construct ambiguous selection")
	}}
	modules := []string{factory.ID}
	_, _, err := buildPluginHost(nil, root, "catalog", home, state, nil, startPluginClient, nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "", factory, factory)
	if err == nil || !strings.Contains(err.Error(), "duplicate native provider") || builds != 0 {
		t.Fatalf("ambiguous catalog: builds=%d err=%v", builds, err)
	}
}

// A persisted composition names the manifest identity, not its trust subject.
// Exercise discovery as well as resolution so migrations retain actual packages.
func TestLegacyFeaturePresentationSelectsDiscoveredPackages(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	discovered := pluginpkg.DiscoverWithOptions(root, home, pluginpkg.DiscoverOptions{
		WuuVersion: "2026.10.19",
		LookPath:   func(command string) (string, error) { return command, nil },
	})
	packages := map[string]pluginpkg.Plugin{}
	for _, item := range discovered {
		packages[item.ID] = item
	}
	for _, feature := range []struct{ provider, presentation string }{{"goal", "goal-ui"}, {"automation-client", "automation-ui"}} {
		provider, found := packages[feature.provider]
		if !found || provider.Runtime == nil {
			t.Fatalf("bundled business provider %q was not discovered", feature.provider)
		}
		presentation, found := packages[feature.presentation]
		if !found || presentation.Desktop == nil {
			t.Fatalf("bundled presentation %q was not discovered", feature.presentation)
		}
		for _, tc := range []struct {
			name              string
			desktop, excluded bool
		}{{name: "desktop", desktop: true}, {name: "headless"}, {name: "explicit removal", desktop: true, excluded: true}} {
			t.Run(feature.provider+"/"+tc.name, func(t *testing.T) {
				modules := []string{provider.ID}
				if tc.desktop {
					modules = append(modules, "wuu.default-shell")
				}
				cfg := &composition.Config{Version: 4, Modules: &modules}
				if tc.excluded {
					cfg.Patches = []composition.Patch{{Modules: map[string]bool{presentation.ID: false}}}
				}
				selected, _, err := selectCompositionPlugins(cfg, discovered)
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]bool{}
				for _, item := range selected {
					got[item.ID] = true
				}
				wantPresentation := tc.desktop && !tc.excluded
				if !got[provider.ID] || got[presentation.ID] != wantPresentation {
					t.Fatalf("discovered selection=%v; want provider=%q presentation=%v", got, provider.ID, wantPresentation)
				}
			})
		}
	}
}

// A third-party renderer's manifest supplies its UI identity; unique-provider
// resolution must work without a built-in surface or an explicit ui.* binding.
// Preflight, the admitted host and public scope diagnostics must agree, while
// current-version compositions must never inherit a legacy UI projection.
func TestLegacyFeaturePresentationWithIndependentRenderer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required for the real bundled goal runtime")
	}
	previous := currentWuuVersion
	currentWuuVersion = func() string { return "2026.10.19" }
	t.Cleanup(func() { currentWuuVersion = previous })
	root, home := t.TempDir(), t.TempDir()
	pluginRoot := filepath.Join(home, "plugins", "independent-shell")
	writeSessionTestFile(t, filepath.Join(pluginRoot, "plugin.json"), `{"schemaVersion":1,"id":"independent-shell","name":"Independent shell","version":"1.0.0","desktop":{"entry":"desktop.js","services":[{"name":"ui.shell","version":"1.0.0","methods":[{"name":"describe","input_schema":"ui.shell.describe.input.v1","output_schema":"ui.shell.describe.output.v1"}]}]}}`)
	writeSessionTestFile(t, filepath.Join(pluginRoot, "desktop.js"), "export function activate() {}\n")
	addonRoot := filepath.Join(home, "plugins", "independent-addon")
	writeSessionTestFile(t, filepath.Join(addonRoot, "plugin.json"), `{"schemaVersion":1,"id":"independent-addon","name":"Independent addon","version":"1.0.0","desktop":{"entry":"desktop.js"}}`)
	writeSessionTestFile(t, filepath.Join(addonRoot, "desktop.js"), "export function activate() {}\n")
	discovered := pluginpkg.DiscoverWithOptions(root, home, pluginpkg.DiscoverOptions{WuuVersion: "2026.10.19"})
	cfg := config.Default()
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "http://127.0.0.1:9/v1", APIKey: "synthetic", Model: "fixture"}}
	cfg.Extensions = &extensions.Settings{}
	rendererFound := false
	for _, item := range discovered {
		if item.ID != "independent-shell" && item.ID != "independent-addon" {
			continue
		}
		rendererFound = true
		if item.Desktop == nil || item.Runtime != nil || item.Official {
			t.Fatalf("not an independent renderer: %+v", item)
		}
		if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: item.EffectivePermissions, ApprovedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if !rendererFound {
		t.Fatal("independent renderer manifest was not discovered")
	}
	selection := func(version int, includeUI bool, renderer string) *composition.Config {
		modules := map[string]bool{"goal": true, renderer: true, credentialstore.OSModule: false, credentialstore.FileModule: true}
		if includeUI {
			modules["goal-ui"] = true
		}
		return &composition.Config{Version: version, Profile: "headless", Patches: []composition.Patch{{Modules: modules}}}
	}
	cases := []struct {
		name     string
		version  int
		renderer string
		wantUI   bool
	}{
		{"legacy custom shell", 4, "independent-shell", true},
		{"current custom shell", 5, "independent-shell", false},
		{"legacy headless addon", 4, "independent-addon", false},
	}
	for _, tc := range cases {
		cfg.Composition = selection(tc.version, false, tc.renderer)
		plan, err := ResolvePluginActivationPlan(cfg, discovered, withCapacityProviders(nil, nil)...)
		if err != nil {
			t.Fatal(err)
		}
		got := slices.ContainsFunc(plan.Plugins, func(item pluginpkg.Plugin) bool { return item.ID == "goal-ui" })
		if got != tc.wantUI {
			t.Fatalf("%s preflight feature UI=%v", tc.name, got)
		}
	}
	cfg.Composition = selection(5, true, "independent-shell")
	cfg.Composition.Patches[0].Modules["independent-addon"] = true
	owner, err := NewSession(Options{RootDir: root, WuuHome: home, HomeDir: t.TempDir(), Config: cfg, DiscoveredPlugins: &discovered, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Cleanup()
	kernel := newKernelHostServices(nil, nil)
	kernel.bindStateGeneration(owner.pluginGeneration, owner)
	defer kernel.compositions.close()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, err := json.Marshal(pluginhost.CompositionCreate{State: "parent", Selection: *selection(tc.version, false, tc.renderer)})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := kernel.compositions.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Caller: "controller", Method: "create", Params: input})
			if err != nil {
				t.Fatal(err)
			}
			var view pluginhost.CompositionView
			if err := json.Unmarshal(raw, &view); err != nil {
				t.Fatal(err)
			}
			child := kernel.compositions.scopes[view.ID].runtime
			provider, found := child.PluginHost.ServiceRegistry().ProviderPluginID("ui.shell", 1)
			if found != (tc.renderer == "independent-shell") || (found && provider != "independent-shell") {
				t.Fatalf("unique shell binding=%q found=%v", provider, found)
			}
			wantUI := tc.wantUI
			actualUI := slices.ContainsFunc(view.Modules, func(module pluginhost.Status) bool {
				return module.ID == "goal-ui" && module.State == pluginhost.StateActive
			})
			graphUI := slices.ContainsFunc(view.Services.Composition.Modules, func(module pluginhost.CompositionModuleSnapshot) bool { return module.ID == "goal-ui" })
			if view.Selection.Includes("goal-ui") != wantUI || actualUI != wantUI || graphUI != wantUI {
				t.Fatalf("v%d selection/host/graph differ: selection=%+v host=%v graph=%v", tc.version, view.Selection, actualUI, graphUI)
			}
			if wantUI && view.Selection.SourceForModule("goal-ui") != "migration:v4-to-v5" {
				t.Fatalf("migration provenance=%q", view.Selection.SourceForModule("goal-ui"))
			}
		})
	}
}
