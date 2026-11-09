package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestProductStartupWithUnavailablePackageDependency(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, mismatch := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%t/mismatch=%t", explicit, mismatch), func(t *testing.T) {
				consumer := testRuntimePlugin("dependent-addon")
				consumer.Runtime = nil
				consumer.Dependencies = []pluginpkg.Dependency{{ID: "provider-addon", Version: "^1.0.0"}}
				independent := testRuntimePlugin("independent-addon")
				independent.Runtime = nil
				optional := testRuntimePlugin("optional-addon")
				optional.Runtime = nil
				optional.Dependencies = []pluginpkg.Dependency{{ID: "provider-addon", Version: "^1.0.0", Optional: true}}
				catalog := []pluginpkg.Plugin{consumer, independent, optional}
				if mismatch {
					provider := testRuntimePlugin("provider-addon")
					provider.Runtime = nil
					provider.Version = "2.0.0"
					catalog = append(catalog, provider)
				}
				cfg := config.Default()
				cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "synthetic", Model: "fixture"}}
				cfg.DefaultProvider = "fixture"
				if explicit {
					var ids []string
					for _, item := range catalog {
						ids = append(ids, item.ID)
					}
					cfg.Composition = composition.DefaultConfig("headless", ids...)
				}
				home := t.TempDir()
				rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: home, WuuHome: home, Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true})
				if explicit {
					if err == nil {
						defer rt.Cleanup()
						t.Fatal("an unavailable explicitly selected consumer did not reject startup")
					}
					if !strings.Contains(err.Error(), consumer.ID) {
						t.Fatalf("explicit selection failed for an unrelated reason: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rt.Cleanup()
				active := map[string]bool{}
				for _, item := range rt.ActivePlugins {
					active[item.ID] = true
				}
				if active[consumer.ID] || !active[independent.ID] || !active[optional.ID] {
					t.Fatalf("package dependency admission = %v", active)
				}
			})
		}
	}
}

// Failure must cascade through declarative-only intermediary packages too.
func TestPackageDependencyFailureBlocksConsumers(t *testing.T) {
	for _, phase := range []string{"prepare", "activate"} {
		t.Run(phase, func(t *testing.T) {
			provider := testRuntimePlugin("provider")
			bridge := testRuntimePlugin("bridge")
			bridge.Runtime = nil
			bridge.Requires = []string{"provider"}
			bridge.Hooks = map[string][]config.HookEntry{string(hooks.PreToolUse): {{Type: "command", Command: "must-not-run"}}}
			consumer := testRuntimePlugin("consumer")
			consumer.Requires = []string{"bridge"}
			independent := testRuntimePlugin("independent")
			clients := map[string]*preparedGenerationClient{}
			var initialized []string
			s := testGenerationSession(t, testPluginGeneration("old", &generationClient{id: "old"}))
			s.RootDir = t.TempDir()
			defer s.Cleanup()
			candidate, err := s.buildPluginGeneration(config.Config{}, []pluginpkg.Plugin{consumer, independent, bridge, provider}, nil, nil,
				func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
					initialized = append(initialized, cfg.ID)
					if cfg.ID == "provider" && phase == "prepare" {
						return nil, errors.New("provider initialization failed")
					}
					client := &preparedGenerationClient{generationClient: &generationClient{id: cfg.ID}}
					if cfg.ID == "provider" {
						client.activateErr = errors.New("provider activation failed")
					}
					clients[cfg.ID] = client
					return client, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			err = s.ActivatePluginGeneration(candidate, nil)
			if phase == "activate" && !PluginGenerationWasCommitted(err) {
				t.Fatalf("activation error = %v", err)
			}
			if phase == "prepare" && strings.Contains(strings.Join(initialized, ","), "consumer") {
				t.Fatalf("initialized blocked consumer: %v", initialized)
			}
			if c := clients["consumer"]; c != nil && (c.activateCall != 0 || !c.closed) {
				t.Fatalf("consumer activated=%d closed=%v", c.activateCall, c.closed)
			}
			if clients["independent"].activateCall != 1 {
				t.Fatal("unrelated plugin did not activate")
			}
			if candidate.hooks.HasHooks(hooks.PreToolUse) {
				t.Fatal("blocked declarative consumer retained its hook")
			}
			for _, active := range s.ActivePlugins {
				if active.ID == "bridge" || active.ID == "consumer" {
					t.Fatalf("blocked package remains active: %s", active.ID)
				}
			}
			states := map[string]pluginhost.Status{}
			for _, status := range s.PluginHost.Statuses() {
				states[status.ID] = status
			}
			for _, id := range []string{"bridge", "consumer"} {
				if status := states[id]; status.State != pluginhost.StateFailed || !strings.Contains(status.Error, "requires") {
					t.Fatalf("missing dependency diagnostic for %s: %+v", id, status)
				}
			}
		})
	}
}

func TestRetainedGenerationRetiresConsumersBeforeProviders(t *testing.T) {
	var order []string
	provider := &generationClient{id: "provider", closeOrder: &order}
	consumer := &generationClient{id: "consumer", closeOrder: &order}
	generation := testPluginGeneration("retained", provider)
	generation.host.Add(consumer)
	generation.active = []pluginpkg.Plugin{testRuntimePlugin("provider"), testRuntimePlugin("consumer")}
	generation.active[1].Requires = []string{"provider"}
	generation.host.SetPackageRequirements(map[string][]string{"consumer": {"provider"}, "provider": nil})
	generation.revokeMissingPlugins(map[string]bool{}, nil, "")
	if !reflect.DeepEqual(order, []string{"consumer", "provider"}) {
		t.Fatalf("retirement order = %v", order)
	}
}

func TestRetainedGenerationUsesItsOwnRequiredGraph(t *testing.T) {
	var order []string
	provider := &generationClient{id: "provider", closeOrder: &order}
	consumer := &generationClient{id: "consumer", closeOrder: &order}
	dependent := &generationClient{id: "dependent", closeOrder: &order}
	generation := testPluginGeneration("retained", provider)
	generation.host.Add(consumer)
	generation.host.Add(dependent)
	generation.active = []pluginpkg.Plugin{testRuntimePlugin("provider"), testRuntimePlugin("consumer"), testRuntimePlugin("dependent")}
	generation.active[1].Dependencies = []pluginpkg.Dependency{{ID: "provider"}}
	generation.active[2].Requires = []string{"consumer"}
	generation.host.SetPackageRequirements(map[string][]string{"dependent": {"consumer"}, "consumer": {"provider"}, "provider": nil})
	// The replacement consumer no longer requires provider. Its pinned previous
	// implementation still does, and must stop when that provider is removed.
	replacementIDs := map[string]bool{"consumer": true, "dependent": true}
	generation.revokeMissingPlugins(replacementIDs, nil, "")
	if !reflect.DeepEqual(order, []string{"dependent", "consumer", "provider"}) {
		t.Fatalf("retirement order = %v", order)
	}
	if !replacementIDs["consumer"] || !replacementIDs["dependent"] {
		t.Fatal("old graph changed the replacement generation's membership")
	}
}

// Use an actual initialize handshake to exercise the initial-session fallback,
// which otherwise hides fatal negotiation behind a generation-level diagnostic.
func TestInitialPluginHostFailureBlocksRequiredConsumers(t *testing.T) {
	if os.Getenv("WUU_DEPENDENCY_NEGOTIATION_HELPER") == "1" {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			var request struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"id": request.ID, "result": map[string]any{"protocol_version": 999}}); err != nil {
				t.Fatal(err)
			}
		}
		for scanner.Scan() {
		}
		return
	}
	provider := testRuntimePlugin("provider")
	provider.Runtime = &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestInitialPluginHostFailureBlocksRequiredConsumers$"}, Env: map[string]string{"WUU_DEPENDENCY_NEGOTIATION_HELPER": "1"}}
	provider.Root = t.TempDir()
	consumer := testRuntimePlugin("consumer")
	consumer.Runtime = nil
	consumer.Requires = []string{"provider"}
	consumer.Hooks = map[string][]config.HookEntry{string(hooks.PreToolUse): {{Type: "command", Command: "must-not-run"}}}
	plugins := []pluginpkg.Plugin{provider, consumer}
	host, _, _ := startPluginHost(plugins, t.TempDir(), "workspace", t.TempDir(), t.TempDir(), nil, nil, nil, "")
	defer host.Close(context.Background())
	available := availablePluginPackages(plugins, host)
	if dispatcher := buildHookDispatcher(config.Config{}, available, nil, "", nil); dispatcher.HasHooks(hooks.PreToolUse) {
		t.Fatal("fatal initial negotiation left a required consumer's hook active")
	}
	failures := host.DependencyFailures()
	if failures["consumer"] == nil {
		t.Fatalf("missing initial dependency diagnostic: %v", failures)
	}
}

// A service consumer visited first can pull a package ahead of its optional
// provider; preserving discovery order alone does not preserve this contract.
func TestOptionalPackageDependencySurvivesServiceOrdering(t *testing.T) {
	provider := testRuntimePlugin("b-provider")
	provider.Version = "1.0.0"
	consumer := testRuntimePlugin("c-consumer")
	consumer.Dependencies = []pluginpkg.Dependency{{ID: provider.ID, Version: "^1.0.0", Optional: true}}
	caller := testRuntimePlugin("a-caller")
	var prepared, activated, closed []string
	clients := map[string]*packageOrderingClient{}
	for _, item := range []pluginpkg.Plugin{provider, consumer, caller} {
		id := item.ID
		clients[id] = &packageOrderingClient{fakeServicePluginClient: &fakeServicePluginClient{id: id}, onPrepare: func() { prepared = append(prepared, id) }, onActivate: func() { activated = append(activated, id) }, onClose: func() { closed = append(closed, id) }}
	}
	clients[consumer.ID].provided = []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}
	clients[caller.ID].required = []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
	s := testGenerationSession(t, testPluginGeneration("old", &generationClient{id: "old"}))
	s.RootDir = t.TempDir()
	defer s.Cleanup()
	candidate, err := s.buildPluginGeneration(config.Config{}, []pluginpkg.Plugin{caller, consumer, provider}, nil, nil,
		func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
			return clients[cfg.ID], nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	if err := candidate.host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{provider.ID, consumer.ID, caller.ID}; !reflect.DeepEqual(prepared, want) {
		t.Errorf("preparation order = %v, want %v", prepared, want)
	}
	if want := []string{provider.ID, consumer.ID, caller.ID}; !reflect.DeepEqual(activated, want) {
		t.Errorf("activation order = %v, want %v", activated, want)
	}
	if want := []string{caller.ID, consumer.ID, provider.ID}; !reflect.DeepEqual(closed, want) {
		t.Errorf("cleanup order = %v, want %v", closed, want)
	}
	if edges := candidate.host.Dependencies()[consumer.ID]; len(edges) != 1 || edges[0].Provider != provider.ID || edges[0].Required {
		t.Errorf("resolved optional package edge lost: %+v", edges)
	}
}

type packageOrderingClient struct {
	*fakeServicePluginClient
	onPrepare  func()
	onActivate func()
	onClose    func()
}

func (c *packageOrderingClient) Prepare(context.Context) error { c.onPrepare(); return nil }
func (c *packageOrderingClient) Activate(ctx context.Context) error {
	c.onActivate()
	return c.fakeServicePluginClient.Activate(ctx)
}
func (c *packageOrderingClient) Close(ctx context.Context) error {
	c.onClose()
	return c.fakeServicePluginClient.Close(ctx)
}

func TestOptionalPackageResolutionDoesNotBlockConsumer(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong-version", "mixed-cycle", "failed-provider", "activation-failed"} {
		t.Run(scenario, func(t *testing.T) {
			provider := testRuntimePlugin("provider")
			provider.Version = "1.0.0"
			consumer := testRuntimePlugin("consumer")
			consumer.Dependencies = []pluginpkg.Dependency{{ID: provider.ID, Version: "^1.0.0", Optional: true}}
			plugins := []pluginpkg.Plugin{consumer}
			if scenario == "wrong-version" {
				provider.Version = "2.0.0"
			}
			if scenario != "missing" {
				plugins = append(plugins, provider)
			}
			clients := map[string]*fakeServicePluginClient{
				consumer.ID: {id: consumer.ID}, provider.ID: {id: provider.ID},
			}
			if scenario == "mixed-cycle" {
				clients[consumer.ID].provided = []pluginhost.ServiceDescriptor{searchServiceDescriptor("1.0.0")}
				clients[provider.ID].required = []pluginhost.ServiceRequirement{{Name: "search.provider", MajorVersion: 1, Required: true}}
			}
			s := testGenerationSession(t, testPluginGeneration("old", &generationClient{id: "old"}))
			s.RootDir = t.TempDir()
			defer s.Cleanup()
			candidate, err := s.buildPluginGeneration(config.Config{}, plugins, nil, nil,
				func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
					if cfg.ID == provider.ID && scenario == "activation-failed" {
						return &preparedGenerationClient{generationClient: &generationClient{id: cfg.ID}, activateErr: errors.New("provider activation failed")}, nil
					}
					if cfg.ID == provider.ID && scenario == "failed-provider" {
						return nil, errors.New("provider initialization failed")
					}
					return clients[cfg.ID], nil
				})
			if err != nil {
				t.Fatal(err)
			}
			err = s.ActivatePluginGeneration(candidate, nil)
			if scenario == "activation-failed" {
				if !PluginGenerationWasCommitted(err) {
					t.Fatalf("activation failure must report committed generation: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !clients[consumer.ID].active {
				t.Fatal("optional package blocked consumer")
			}
			if scenario == "failed-provider" || scenario == "activation-failed" {
				return
			}
			snapshot := candidate.host.ServiceRegistry().Snapshot(0)
			if edges := snapshot.Dependencies[consumer.ID]; len(edges) != 0 {
				t.Errorf("unresolved optional relation became active edge: %+v", edges)
			}
			// Check the public introspection shape rather than coupling callers to
			// the host's internal diagnostic representation.
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics struct {
				Issues map[string][]struct {
					Provider string `json:"provider"`
					Kind     string `json:"kind"`
				} `json:"dependency_issues"`
			}
			if err := json.Unmarshal(raw, &diagnostics); err != nil {
				t.Fatal(err)
			}
			kind := map[string]string{"missing": "optional_missing", "wrong-version": "optional_version_mismatch", "mixed-cycle": "optional_cycle"}[scenario]
			found := false
			for _, issue := range diagnostics.Issues[consumer.ID] {
				found = found || issue.Provider == provider.ID && issue.Kind == kind
			}
			if !found {
				t.Errorf("missing %s diagnostic: %s", kind, raw)
			}
		})
	}
}

func TestBarePluginHostRejectsSelectedUnavailablePackage(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			consumer := testRuntimePlugin("consumer")
			consumer.Dependencies = []pluginpkg.Dependency{{ID: "provider", Version: "^1.0.0"}}
			plugins := []pluginpkg.Plugin{consumer}
			modules := []string{consumer.ID}
			if mismatch {
				provider := testRuntimePlugin("provider")
				provider.Version = "2.0.0"
				plugins = append(plugins, provider)
				modules = append(modules, provider.ID)
			}
			started := false
			host, _, err := buildPluginHost(plugins, t.TempDir(), "", t.TempDir(), t.TempDir(), nil,
				func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
					started = true
					return &fakeServicePluginClient{id: cfg.ID}, nil
				},
				nil, nil, &composition.Config{Version: composition.Version, Modules: &modules}, "")
			if host != nil {
				defer host.Close(context.Background())
			}
			if err == nil || started {
				t.Fatalf("unavailable selected package must fail before startup: err=%v started=%v", err, started)
			}
		})
	}
}
