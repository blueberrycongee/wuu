package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Exercise the runtime-to-toolkit binding, not only a standalone skill filter:
// existing conversation and worker clones must observe the same revocation.
func TestDependencyRevocationReachesPinnedSkillCatalogs(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "test-only")
	cfg := config.Config{DefaultProvider: "test", Providers: map[string]config.ProviderConfig{
		"test": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "test-model"},
	}}
	s, err := NewSession(Options{RootDir: root, HomeDir: home, ConfigPath: filepath.Join(root, ".wuu.json"), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cleanup()
	provider := testRuntimePlugin("provider")
	bridge := testRuntimePlugin("skill-bridge")
	bridge.Runtime, bridge.Source, bridge.Root = nil, "project", t.TempDir()
	bridge.Requires = []string{"provider"}
	bridge.Hooks = map[string][]config.HookEntry{string(hooks.PreToolUse): {{Type: "command", Command: "must-not-run"}}}
	writeSessionTestFile(t, filepath.Join(bridge.Root, "skills", "revocable.md"), "---\nname: revocable\ndescription: Revocable plugin guidance.\n---\nPlugin-owned body.\n")
	consumer := testRuntimePlugin("consumer")
	consumer.Requires = []string{"skill-bridge"}
	packages := []pluginpkg.Plugin{provider, bridge, consumer}
	clients := map[string]*preparedGenerationClient{}
	failNextInitialize := filepath.Join(t.TempDir(), "fail-initialize")
	var providerClient *pluginhost.ProcessClient
	startClient := func(ctx context.Context, c pluginhost.ProcessConfig) (pluginhost.Client, error) {
		if c.ID == "provider" {
			c.Command, c.Args = os.Args[0], []string{"-test.run=^TestDependencyRevocationProcessHelper$"}
			c.Env = map[string]string{"WUU_REVOCATION_PROCESS_HELPER": "1", "WUU_REVOCATION_FAIL_INITIALIZE": failNextInitialize}
			client, err := pluginhost.Start(ctx, c)
			if err == nil {
				providerClient = client
			}
			return client, err
		}
		client := &preparedGenerationClient{generationClient: &generationClient{id: c.ID}}
		clients[c.ID] = client
		return client, nil
	}
	generation, err := s.buildPluginGeneration(cfg, packages, nil, nil, startClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ActivatePluginGeneration(generation, nil); err != nil {
		t.Fatal(err)
	}
	thread, err := s.NewThreadRuntime("pinned-skills")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { thread.AgentControl.Close(); s.ReleasePluginGeneration(thread.PluginGeneration) }()
	worker, err := thread.Toolkit.CloneForRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	load := providers.ToolCall{Name: "load_skill", Arguments: `{"name":"revocable"}`}
	originalPrompt := thread.StreamRunner.SystemPrompt
	if !strings.Contains(originalPrompt, "revocable") {
		t.Fatal("fixture never materialized plugin guidance")
	}
	for _, kit := range []*tools.Toolkit{thread.Toolkit, worker} {
		if result, err := kit.Execute(context.Background(), load); err != nil || !strings.Contains(result, "Plugin-owned body") {
			t.Fatalf("fixture load: %s %v", result, err)
		}
	}
	crossRoot, overrideRoot := t.TempDir(), t.TempDir()
	writeSessionTestFile(t, filepath.Join(overrideRoot, ".agents", "skills", "revocable", "SKILL.md"), "---\nname: revocable\ndescription: Independent checkout guidance.\n---\nWorkspace-owned body.\n")

	// The provider really exits after activation; the invocation completes only
	// after the process transport reports its failure.
	if _, err := providerClient.InvokeCapability(context.Background(), pluginhost.CapabilityInvokeParams{Capability: pluginhost.CapabilityPluginClientRequest}); err == nil {
		t.Fatal("provider crash unexpectedly returned success")
	}
	if !s.PluginGenerationNeedsRecovery() {
		t.Fatal("active provider exit was not detected")
	}
	shutdownRead := make(chan bool, 1)
	clients["consumer"].onClose = func() {
		// A shutdown callback may call back into catalog APIs synchronously.
		_, found := skills.Find(worker.Skills(), "revocable")
		_ = s.ExtensionSnapshot()
		shutdownRead <- !found
	}
	start, done := make(chan struct{}), make(chan struct{})
	ready := make(chan struct{}, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			_ = worker.Skills()
			ready <- struct{}{}
			for {
				_ = worker.Skills()
				_ = s.ExtensionSnapshot()
				_, _ = s.guidanceForRoot(root, generation)
				select {
				case <-done:
					return
				default:
				}
			}
		}()
	}
	close(start)
	for range 4 {
		<-ready
	}
	changed := s.RevokeFailedPluginDependencies()
	close(done)
	readers.Wait()
	if !changed || !clients["consumer"].closed {
		t.Fatal("consumer was not revoked")
	}
	if !<-shutdownRead {
		t.Fatal("shutdown observed an available revoked skill")
	}
	for _, kit := range []*tools.Toolkit{s.Toolkit, thread.Toolkit, worker} {
		if _, found := skills.Find(kit.Skills(), "revocable"); found {
			t.Fatal("old toolkit lists revoked skill")
		}
		if result, err := kit.Execute(context.Background(), load); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("old toolkit still loads revoked skill: %s %v", result, err)
		}
	}
	for _, checkout := range []string{root, crossRoot} {
		_, catalog := s.guidanceForRoot(checkout, generation)
		if _, found := skills.Find(catalog, "revocable"); found {
			t.Fatalf("future guidance retained revoked skill at %s", checkout)
		}
	}
	for i, checkout := range []string{crossRoot, overrideRoot} {
		fresh, err := s.NewThreadRuntimeForRoot([]string{"fresh-revoked", "fresh-independent"}[i], checkout)
		if err != nil {
			t.Fatal(err)
		}
		result, loadErr := fresh.Toolkit.Execute(context.Background(), load)
		fresh.AgentControl.Close()
		s.ReleasePluginGeneration(fresh.PluginGeneration)
		if i == 0 && loadErr == nil {
			t.Fatal("new cross-root runtime loaded revoked plugin skill")
		}
		if i == 1 && (loadErr != nil || !strings.Contains(result, "Workspace-owned body")) {
			t.Fatalf("independent same-name skill cannot load: %s %v", result, loadErr)
		}
	}
	_, override := s.guidanceForRoot(overrideRoot, generation)
	if skill, found := skills.Find(override, "revocable"); !found || !strings.Contains(skill.Content, "Workspace-owned body") {
		t.Fatal("revocation hid an independent same-name workspace skill")
	}
	snapshot := s.ExtensionSnapshot()
	if runtimeHasPlugin(snapshot.ActivePlugins, "skill-bridge") || runtimeHasPlugin(snapshot.ActivePlugins, "consumer") {
		t.Fatal("snapshot lists blocked packages as active")
	}
	if generation.hooks.HasHooks(hooks.PreToolUse) {
		t.Fatal("future hook admissions retained blocked bridge")
	}
	if thread.StreamRunner.SystemPrompt != originalPrompt {
		t.Fatal("revocation rewrote an already materialized thread prompt")
	}

	// A failed replacement cannot re-enable the already revoked generation.
	writeSessionTestFile(t, failNextInitialize, "fail")
	_, err = s.buildPluginGeneration(cfg, packages, map[string]bool{"provider": true}, nil, startClient)
	if err == nil || !strings.Contains(err.Error(), "unsupported negotiated protocol version 999") {
		t.Fatalf("replacement did not fail its real initialize handshake: %v", err)
	}
	if s.RevokeFailedPluginDependencies() {
		t.Fatal("unchanged dependency failure was revoked twice")
	}
	if _, found := skills.Find(worker.Skills(), "revocable"); found {
		t.Fatal("failed recovery revived old clone")
	}
}

// The subprocess speaks the same initialize/activate protocol as an installed
// runtime. A sentinel makes only its replacement fail the initialize handshake.
func TestDependencyRevocationProcessHelper(t *testing.T) {
	if os.Getenv("WUU_REVOCATION_PROCESS_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct{ ID, Method string }
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			version := pluginhost.CapabilityProtocolVersion
			if _, err := os.Stat(os.Getenv("WUU_REVOCATION_FAIL_INITIALIZE")); err == nil {
				version = 999
			}
			result = map[string]any{"protocol_version": version, "lifecycle_version": pluginhost.RuntimeLifecycleVersion}
		case "capability.invoke":
			os.Exit(23)
		case "shutdown":
			_ = encoder.Encode(map[string]any{"id": request.ID, "result": result})
			return
		}
		if encoder.Encode(map[string]any{"id": request.ID, "result": result}) != nil {
			os.Exit(3)
		}
	}
}

func TestDependencyRevocationKeepsHealthyReplacementGeneration(t *testing.T) {
	oldProvider := &generationClient{id: "provider"}
	oldConsumer := &generationClient{id: "consumer"}
	old := testPluginGeneration("provider", oldProvider)
	old.host.Add(oldConsumer)
	consumer := testRuntimePlugin("consumer")
	consumer.Requires = []string{"provider"}
	old.active = append(old.active, consumer)
	old.host.SetPackageRequirements(map[string][]string{"consumer": {"provider"}})
	skill := skills.Skill{Name: "review", Source: "plugin:consumer", Content: "Review."}
	old.skills = []skills.Skill{skill}
	s := testGenerationSession(old)
	s.RootDir = t.TempDir()
	defer s.Cleanup()
	pinned := s.RetainPluginGeneration()
	defer s.ReleasePluginGeneration(pinned)
	oldKit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldKit.SetSkillsWithAvailability(old.skills, old.skillAvailable)
	newProvider, newConsumer := &generationClient{id: "provider"}, &generationClient{id: "consumer"}
	current := testPluginGeneration("provider", newProvider)
	current.host.Add(newConsumer)
	current.active = append(current.active, consumer)
	current.host.SetPackageRequirements(map[string][]string{"consumer": {"provider"}})
	current.skills = []skills.Skill{skill}
	if err := s.ActivatePluginGeneration(current, nil); err != nil {
		t.Fatal(err)
	}
	oldProvider.status = &pluginhost.Status{ID: "provider", State: pluginhost.StateFailed, Error: "old provider exited"}
	if s.PluginGenerationNeedsRecovery() {
		t.Fatal("old generation failure incorrectly requires replacing the healthy current runtime")
	}
	if !s.RevokeFailedPluginDependencies() {
		t.Fatal("pinned old generation failure was ignored")
	}
	if !oldConsumer.closed || newConsumer.closed || newProvider.closed {
		t.Fatal("revocation crossed generation ownership")
	}
	if len(oldKit.Skills()) != 0 || !current.skillAvailable(skill) {
		t.Fatal("same-id generations shared their skill revocation gate")
	}
	if !s.IsCurrentPluginGeneration(current) {
		t.Fatal("revocation replaced the healthy current generation")
	}
	if old.refs.Load() != 1 {
		t.Fatalf("revocation leaked or consumed the pinned reference: %d", old.refs.Load())
	}
}
