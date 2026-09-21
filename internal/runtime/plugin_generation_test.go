package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type generationClient struct {
	id           string
	closed       bool
	closeOrder   *[]string
	capabilities []pluginhost.CapabilityDescriptor
	invoke       func(pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error)
	invoked      int
	status       *pluginhost.Status
}

func (c *generationClient) ID() string { return c.id }
func (c *generationClient) Status() pluginhost.Status {
	if c.status != nil {
		return *c.status
	}
	return pluginhost.Status{ID: c.id, State: pluginhost.StateActive}
}
func (c *generationClient) Close(context.Context) error {
	c.closed = true
	if c.closeOrder != nil {
		*c.closeOrder = append(*c.closeOrder, c.id)
	}
	return nil
}

func (c *generationClient) ProtocolVersion() int { return pluginhost.CapabilityProtocolVersion }
func (c *generationClient) Capabilities() []pluginhost.CapabilityDescriptor {
	return append([]pluginhost.CapabilityDescriptor(nil), c.capabilities...)
}
func (c *generationClient) InvokeCapability(_ context.Context, params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
	c.invoked++
	if c.invoke != nil {
		return c.invoke(params)
	}
	return pluginhost.CapabilityInvokeResult{}, nil
}

type generationCompactionProvider struct{ key string }

func (p *generationCompactionProvider) CompactionKey() string   { return p.key }
func (p *generationCompactionProvider) CompactionPriority() int { return 1 }
func (p *generationCompactionProvider) Compact(_ context.Context, _ string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	return messages, nil
}

func promptGenerationClient(id, text string, priority int) *generationClient {
	return &generationClient{
		id: id,
		capabilities: []pluginhost.CapabilityDescriptor{{
			ID: pluginhost.CapabilityAgentSystemPromptSection, Kind: "transform", Version: 1, Priority: priority,
		}},
		invoke: func(params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
			var input pluginhost.SystemPromptSectionInput
			if err := json.Unmarshal(params.Input, &input); err != nil {
				return pluginhost.CapabilityInvokeResult{}, err
			}
			if input.Provider == "" || input.Model == "" || input.CWD == "" {
				return pluginhost.CapabilityInvokeResult{}, errors.New("missing typed prompt input")
			}
			data, err := json.Marshal(pluginhost.SystemPromptSectionOutput{Text: text})
			return pluginhost.CapabilityInvokeResult{Output: data}, err
		},
	}
}

func TestPluginSystemPromptSectionsEvaluateBeforeActivationInPriorityOrder(t *testing.T) {
	high := promptGenerationClient("high", "high section", 20)
	low := promptGenerationClient("low", "low section", 5)
	session := &Session{ProviderName: "openai", Model: "model", RootDir: "/workspace"}
	generation, err := session.buildPluginGeneration(config.Config{}, []pluginpkg.Plugin{
		testRuntimePlugin("low"), testRuntimePlugin("high"),
	}, nil, nil, func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		if cfg.ID == "high" {
			return high, nil
		}
		return low, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer generation.close()
	prompt, _ := generation.systemPrompts.Assemble("")
	if high.invoked != 1 || low.invoked != 1 || prompt != "high section\n\nlow section" {
		t.Fatalf("invocations high=%d low=%d prompt=%q", high.invoked, low.invoked, prompt)
	}
}

func TestPluginSystemPromptFailureRejectsCandidateAndPreservesOldGeneration(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	session := testGenerationSession(old)
	broken := promptGenerationClient("broken", "", 1)
	broken.invoke = func(pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
		return pluginhost.CapabilityInvokeResult{}, errors.New("prompt failed")
	}
	_, err := session.buildPluginGeneration(config.Config{}, []pluginpkg.Plugin{testRuntimePlugin("broken")}, nil, nil, func(context.Context, pluginhost.ProcessConfig) (pluginhost.Client, error) {
		return broken, nil
	})
	if err == nil || !strings.Contains(err.Error(), "prompt failed") {
		t.Fatalf("error = %v", err)
	}
	if !broken.closed || oldClient.closed || session.PluginHost != old.host {
		t.Fatalf("candidate closed=%v old closed=%v host preserved=%v", broken.closed, oldClient.closed, session.PluginHost == old.host)
	}
}

func TestPluginCompactionInvokesHighestPriorityAndGenerationCleanupReachesClones(t *testing.T) {
	makeClient := func(id, marker string, priority int) *generationClient {
		return &generationClient{
			id: id,
			capabilities: []pluginhost.CapabilityDescriptor{{
				ID: pluginhost.CapabilityAgentCompaction, Kind: "decision", Version: 1, Priority: priority,
			}},
			invoke: func(params pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
				var input pluginhost.CompactionInput
				if err := json.Unmarshal(params.Input, &input); err != nil {
					return pluginhost.CapabilityInvokeResult{}, err
				}
				data, err := json.Marshal(pluginhost.CompactionOutput{Messages: []providers.ChatMessage{{Role: "system", Content: marker + ":" + input.Model}}})
				return pluginhost.CapabilityInvokeResult{Output: data}, err
			},
		}
	}
	high := makeClient("high", "high", 20)
	low := makeClient("low", "low", 5)
	host := pluginhost.New(low, high)
	_, registry, err := buildPluginAgentCapabilities(context.Background(), host, "openai", "model", "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	base := &agent.StreamRunner{CompactionRegistry: registry}
	clone := cloneStreamRunnerForThread(base, nil)
	messages, err := clone.CompactionRegistry.Resolve(nil).Compact(context.Background(), "runtime-model", []providers.ChatMessage{{Role: "user", Content: "history"}})
	if err != nil {
		t.Fatal(err)
	}
	if high.invoked != 1 || low.invoked != 0 || messages[0].Content != "high:runtime-model" {
		t.Fatalf("high=%d low=%d messages=%+v", high.invoked, low.invoked, messages)
	}
	generation := &PluginGeneration{host: host, hooks: hooks.NewDispatcher(nil), compactions: registry}
	if err := generation.close(); err != nil {
		t.Fatal(err)
	}
	if clone.CompactionRegistry.Count() != 0 || !clone.ContextWindowsAvailable() {
		t.Fatal("cloned runner retained compaction from closed generation")
	}
}

func TestFailedCandidateClosesStartedProcessesAndPreservesOldGeneration(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	session := testGenerationSession(old)

	var closeOrder []string
	first := &generationClient{id: "first", closeOrder: &closeOrder}
	second := &generationClient{id: "second", closeOrder: &closeOrder}
	plugins := []pluginpkg.Plugin{
		testRuntimePlugin("first"),
		testRuntimePlugin("second"),
		testRuntimePlugin("target"),
	}
	_, err := session.buildPluginGeneration(config.Config{}, plugins, map[string]bool{"target": true}, nil, func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		switch cfg.ID {
		case "first":
			return first, nil
		case "second":
			return second, nil
		default:
			return nil, errors.New("candidate initialization failed")
		}
	})
	if err == nil {
		t.Fatal("candidate generation unexpectedly succeeded")
	}
	if oldClient.closed || session.PluginHost != old.host || session.ActivePlugins[0].ID != "old" {
		t.Fatalf("old generation changed after failed candidate: closed=%v active=%+v", oldClient.closed, session.ActivePlugins)
	}
	if !first.closed || !second.closed {
		t.Fatalf("candidate processes were not closed: first=%v second=%v", first.closed, second.closed)
	}
	if len(closeOrder) != 2 || closeOrder[0] != "second" || closeOrder[1] != "first" {
		t.Fatalf("candidate close order = %v, want [second first]", closeOrder)
	}
}

func TestCapabilityConflictClosesCandidateAndPreservesOldGeneration(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	session := testGenerationSession(old)
	one := &generationClient{id: "one", capabilities: []pluginhost.CapabilityDescriptor{{
		ID: "agent.capability.one", Kind: "transform", Version: 1, Conflicts: []string{"agent.capability.two"},
	}}}
	two := &generationClient{id: "two", capabilities: []pluginhost.CapabilityDescriptor{{
		ID: "agent.capability.two", Kind: "observe", Version: 1,
	}}}
	_, err := session.buildPluginGeneration(config.Config{}, []pluginpkg.Plugin{
		testRuntimePlugin("one"), testRuntimePlugin("two"),
	}, nil, nil, func(_ context.Context, cfg pluginhost.ProcessConfig) (pluginhost.Client, error) {
		if cfg.ID == "one" {
			return one, nil
		}
		return two, nil
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("error = %v", err)
	}
	if !one.closed || !two.closed {
		t.Fatalf("candidate not closed: one=%v two=%v", one.closed, two.closed)
	}
	if oldClient.closed || session.PluginHost != old.host || session.ActivePlugins[0].ID != "old" {
		t.Fatal("old generation was polluted by rejected capabilities")
	}
}

func TestSuccessfulCandidateSwapsThenClosesOldGeneration(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	old.systemPrompts = agent.NewSystemPromptAssembler()
	old.systemPrompts.Add(agent.NewStaticPromptSection("old", "old section", 1))
	old.compactions = agent.NewCompactionRegistry()
	old.compactions.Register(&generationCompactionProvider{key: "old"})
	session := testGenerationSession(old)
	session.systemPrompts = old.systemPrompts
	session.StreamRunner = &agent.StreamRunner{CompactionRegistry: old.compactions}
	threadClone := cloneStreamRunnerForThread(session.StreamRunner, nil)
	candidateClient := &generationClient{id: "candidate"}
	candidate := testPluginGeneration("candidate", candidateClient)
	candidate.systemPrompts = agent.NewSystemPromptAssembler()
	candidate.systemPrompts.Add(agent.NewStaticPromptSection("candidate", "candidate section", 1))
	candidate.compactions = agent.NewCompactionRegistry()
	candidate.compactions.Register(&generationCompactionProvider{key: "candidate"})

	if err := session.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	if session.PluginHost != candidate.host || len(session.ActivePlugins) != 1 || session.ActivePlugins[0].ID != "candidate" {
		t.Fatalf("candidate was not installed: host=%p active=%+v", session.PluginHost, session.ActivePlugins)
	}
	if !oldClient.closed {
		t.Fatal("old generation was not closed after the swap")
	}
	if candidateClient.closed {
		t.Fatal("active candidate was closed")
	}
	if old.systemPrompts != nil || old.compactions != nil || threadClone.CompactionRegistry.Count() != 0 {
		t.Fatal("old generation capability state survived the swap")
	}
	if session.StreamRunner.CompactionRegistry != candidate.compactions || !strings.Contains(session.StreamRunner.SystemPrompt, "candidate section") {
		t.Fatal("candidate capability state was not installed atomically")
	}
}

func TestActivatePluginGenerationKeepsPinnedConversationGeneration(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	session := testGenerationSession(old)
	pinned := session.RetainPluginGeneration()
	if pinned != old {
		t.Fatal("session did not pin the current generation")
	}
	candidateClient := &generationClient{id: "candidate"}
	candidate := testPluginGeneration("candidate", candidateClient)
	if err := session.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	if session.PluginHost != candidate.host {
		t.Fatal("candidate was not published as the live generation")
	}
	if oldClient.closed {
		t.Fatal("pinned conversation generation was closed during the swap")
	}
	if candidateClient.closed {
		t.Fatal("live candidate was closed")
	}
	session.ReleasePluginGeneration(pinned)
	if !oldClient.closed {
		t.Fatal("pinned generation survived after the last conversation released it")
	}
}

func TestPluginGenerationNeedsRecoveryOnlyAfterActiveRuntimeFails(t *testing.T) {
	started := time.Now().UTC()
	for _, test := range []struct {
		name   string
		status pluginhost.Status
		want   bool
	}{
		{name: "active", status: pluginhost.Status{ID: "plugin", State: pluginhost.StateActive, StartedAt: started}},
		{name: "startup failure", status: pluginhost.Status{ID: "plugin", State: pluginhost.StateFailed}},
		{name: "runtime failure", status: pluginhost.Status{ID: "plugin", State: pluginhost.StateFailed, StartedAt: started}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &generationClient{id: test.status.ID, status: &test.status}
			session := &Session{PluginHost: pluginhost.New(client)}
			if got := session.PluginGenerationNeedsRecovery(); got != test.want {
				t.Fatalf("PluginGenerationNeedsRecovery() = %v, want %v", got, test.want)
			}
		})
	}
}

// preparedGenerationClient reports StatePrepared so Host.Activate invokes its
// lifecycle hook, letting tests exercise candidate activation success/failure.
type preparedGenerationClient struct {
	*generationClient
	activateErr  error
	activateCall int
}

func (c *preparedGenerationClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.id, State: pluginhost.StatePrepared}
}

func (c *preparedGenerationClient) Activate(context.Context) error {
	c.activateCall++
	return c.activateErr
}

func TestCandidateActivationFailureLeavesCurrentGenerationUntouched(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	session := testGenerationSession(old)

	broken := &preparedGenerationClient{
		generationClient: &generationClient{id: "broken"},
		activateErr:      errors.New("activate failed"),
	}
	candidate := testPluginGeneration("broken", broken)

	commitCalled := false
	err := session.ActivatePluginGeneration(candidate, func() error {
		commitCalled = true
		return nil
	})
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if commitCalled {
		t.Fatal("commit ran after activation failure")
	}
	if session.pluginGeneration != old || session.PluginHost != old.host || session.ActivePlugins[0].ID != "old" {
		t.Fatalf("current generation changed after failed activation: active=%+v", session.ActivePlugins)
	}
	if oldClient.closed {
		t.Fatal("old generation was closed by the failed candidate")
	}
	if !broken.closed {
		t.Fatal("failed candidate kept candidate-owned resources")
	}
}

func TestCandidateCommitFailureRollsBackAllLiveSurfaces(t *testing.T) {
	oldClient := &generationClient{id: "old"}
	old := testPluginGeneration("old", oldClient)
	old.hooks = hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.PreToolUse: {{Command: "old-hook"}},
	}))
	session := testGenerationSession(old)
	liveHooks := hooks.NewDispatcher(nil)
	liveHooks.Replace(old.hooks)
	session.HookDispatcher = liveHooks

	candidateClient := &generationClient{id: "candidate"}
	candidate := testPluginGeneration("candidate", candidateClient)
	candidate.hooks = hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.PostToolUse: {{Command: "candidate-hook"}},
	}))

	err := session.ActivatePluginGeneration(candidate, func() error { return errors.New("publish failed") })
	if err == nil {
		t.Fatal("activation unexpectedly succeeded")
	}
	if session.PluginHost != old.host || session.ActivePlugins[0].ID != "old" || oldClient.closed {
		t.Fatalf("old generation was not restored: closed=%v active=%+v", oldClient.closed, session.ActivePlugins)
	}
	if !session.HookDispatcher.HasHooks(hooks.PreToolUse) || session.HookDispatcher.HasHooks(hooks.PostToolUse) {
		t.Fatal("old hook registry was not restored after rollback")
	}
	if !candidateClient.closed {
		t.Fatal("rolled-back candidate was not closed")
	}
}

func TestClosedGenerationReleasesCapabilityRegistry(t *testing.T) {
	client := &runtimeCapabilityClient{id: "capability"}
	host := pluginhost.New(client)
	generation := &PluginGeneration{
		host:              host,
		requestTransforms: buildPluginRequestTransforms(host, "openai", "", "/workspace"),
	}
	if generation.requestTransforms.Count() != 1 {
		t.Fatalf("transform count = %d", generation.requestTransforms.Count())
	}
	if err := generation.close(); err != nil {
		t.Fatal(err)
	}
	if generation.host != nil || generation.requestTransforms != nil {
		t.Fatal("closed generation retained capability state")
	}
	if capabilities := host.Capabilities(pluginhost.CapabilityAgentRequestTransform); len(capabilities) != 0 {
		t.Fatalf("closed host capabilities = %+v", capabilities)
	}
}

func TestRequiredCandidateMCPServerMustBeReady(t *testing.T) {
	manager, err := startMCPManager(config.Config{}, []pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{
			ID: "candidate",
			MCPServers: map[string]config.MCPServerConfig{
				"broken": {Command: "/definitely/not/a/wuu-mcp-server"},
			},
		},
	}}, map[string]bool{"candidate": true})
	if err == nil {
		if manager != nil {
			_ = manager.Close()
		}
		t.Fatal("required MCP startup unexpectedly succeeded")
	}
	if manager != nil {
		t.Fatal("failed required MCP startup returned a live manager")
	}
}

func TestNewSessionDoesNotDiscoverPluginsDuringMutation(t *testing.T) {
	homeDir := t.TempDir()
	wuuHome := filepath.Join(homeDir, ".wuu")
	mutation, acquired, err := session.TryAcquirePluginGenerationMutationLease(wuuHome)
	if err != nil || !acquired {
		t.Fatalf("acquire mutation generation: acquired=%v err=%v", acquired, err)
	}
	defer mutation.Release()

	_, err = NewSession(Options{RootDir: t.TempDir(), HomeDir: homeDir})
	if err == nil || !strings.Contains(err.Error(), "plugin packages are being changed") {
		t.Fatalf("NewSession error = %v, want active plugin mutation", err)
	}
}

func testGenerationSession(generation *PluginGeneration) *Session {
	if generation != nil && generation.refs.Load() == 0 {
		generation.retain()
	}
	return &Session{
		PluginHost:       generation.host,
		HookDispatcher:   hooks.NewDispatcher(nil),
		Plugins:          append([]pluginpkg.Plugin(nil), generation.plugins...),
		ActivePlugins:    append([]pluginpkg.Plugin(nil), generation.active...),
		pluginGeneration: generation,
	}
}

func TestRefreshPluginCatalogKeepsActiveGeneration(t *testing.T) {
	host := pluginhost.New(&generationClient{id: "live"})
	livePlugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "live"}}
	generation := &PluginGeneration{
		id:      "gen-live",
		plugins: []pluginpkg.Plugin{livePlugin},
		active:  []pluginpkg.Plugin{livePlugin},
		host:    host,
	}
	session := &Session{
		RootDir:          t.TempDir(),
		WuuHome:          t.TempDir(),
		Plugins:          []pluginpkg.Plugin{livePlugin},
		ActivePlugins:    []pluginpkg.Plugin{livePlugin},
		PluginHost:       host,
		pluginGeneration: generation,
	}
	if err := session.RefreshPluginCatalog(); err != nil {
		t.Fatal(err)
	}
	if session.PluginHost != host || session.pluginGeneration != generation || generation.host != host {
		t.Fatal("catalog refresh touched the active plugin generation")
	}
	if len(session.ActivePlugins) != 1 || session.ActivePlugins[0].ID != "live" {
		t.Fatalf("catalog refresh changed the active plugin set: %+v", session.ActivePlugins)
	}
	if session.Skills != nil || session.systemPrompts != nil {
		t.Fatal("catalog refresh installed model-facing surfaces")
	}
}

func testPluginGeneration(id string, client pluginhost.Client) *PluginGeneration {
	plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: id}}
	return &PluginGeneration{
		id:      "gen-test-" + id,
		plugins: []pluginpkg.Plugin{plugin},
		active:  []pluginpkg.Plugin{plugin},
		host:    pluginhost.New(client),
		hooks:   hooks.NewDispatcher(nil),
	}
}

func testRuntimePlugin(id string) pluginpkg.Plugin {
	return pluginpkg.Plugin{
		Manifest: pluginpkg.Manifest{
			ID: id,
			Runtime: &pluginpkg.RuntimeSpec{
				Protocol: pluginhost.ProtocolName,
				Command:  id,
			},
		},
		Official: true,
	}
}
