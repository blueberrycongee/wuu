package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

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
			s := testGenerationSession(testPluginGeneration("old", &generationClient{id: "old"}))
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
	host, _ := startPluginHost(plugins, t.TempDir(), "workspace", t.TempDir(), t.TempDir(), nil, nil)
	defer host.Close(context.Background())
	available := availablePluginPackages(plugins, host)
	if dispatcher := buildHookDispatcher(config.Config{}, available, nil, "", nil); dispatcher.HasHooks(hooks.PreToolUse) {
		t.Fatal("fatal initial negotiation left a required consumer's hook active")
	}
	failures := host.PackageDependencyFailures()
	if failures["consumer"] == nil {
		t.Fatalf("missing initial dependency diagnostic: %v", failures)
	}
}
