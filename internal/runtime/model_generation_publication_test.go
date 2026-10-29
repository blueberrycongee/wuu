package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/stretchr/testify/require"
)

// The selected prompt service reports its actual input. Its result makes it
// possible to inspect which model and surface were captured with each graph.
func TestModelGenerationPublishesMatchingPromptAndToolLoading(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", promptservice.NativeModule, "wuu.compaction.default", "wuu.agent.policy.default", "wuu.model.native", modelExecutionConsumer, capacityPolicyModule, capacityResourceModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "openai", Providers: map[string]config.ProviderConfig{"openai": {Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses", APIKey: "fixture", Model: "gpt-5.4"}, "kimi": {Type: "anthropic", BaseURL: "https://api.kimi.com/coding", APIKey: "fixture", Model: "k3"}}}
	factory := NativeProviderFactory{ID: promptservice.NativeModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &driverProviderClient{id: promptservice.NativeModule, descriptor: promptservice.NewService().ProvidedServices()[0], invoke: func(_ context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
			var request promptservice.Request
			if err := json.Unmarshal(call.Params, &request); err != nil {
				return nil, err
			}
			return json.Marshal(promptservice.Result{Content: fmt.Sprintf("fixture provider=%s model=%s surface=%s", request.Provider, request.Model, request.Surface.ProfileName), Sections: []promptservice.Section{}})
		}}, nil
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	require.NoError(t, err)
	defer rt.Cleanup()
	initial, err := rt.RetainState()
	require.NoError(t, err)
	defer initial.Close()
	initialModels, err := initial.Models()
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", initialModels.Roles.Main.Model)
	require.Contains(t, initial.SystemPrompt, "provider=openai model=gpt-5.4")
	require.NotNil(t, rt.Toolkit)
	require.True(t, rt.Toolkit.NativeDeferredToolDiscovery(), "fixture must start in native discovery mode")

	require.NoError(t, rt.RefreshModelConfiguration(cfg, "openai", "gpt-4o"))
	next, err := rt.RetainState()
	require.NoError(t, err)
	defer next.Close()
	nextModels, err := next.Models()
	require.NoError(t, err)
	require.Equal(t, "openai", nextModels.Roles.Main.Provider)
	require.Equal(t, "gpt-4o", nextModels.Roles.Main.Model)
	require.Contains(t, next.SystemPrompt, "provider=openai model=gpt-4o")
	require.False(t, strings.Contains(next.SystemPrompt, "model=gpt-5.4"))
	require.NotNil(t, rt.Toolkit)
	require.True(t, rt.Toolkit.ToolSearchEnabled())
	require.False(t, rt.Toolkit.NativeDeferredToolDiscovery(), "unsupported target must use client discovery")
	require.False(t, rt.StreamRunner.NativeDeferredToolDiscovery)
	require.Equal(t, "gpt-4o", rt.Toolkit.ActiveProfile().Model)

	// Provider changes must update prompt and tool surface together as well.
	require.NoError(t, rt.RefreshModelConfiguration(cfg, "kimi", "k3"))
	other, err := rt.RetainState()
	require.NoError(t, err)
	defer other.Close()
	otherModels, err := other.Models()
	require.NoError(t, err)
	require.Equal(t, "kimi", otherModels.Roles.Main.Provider)
	require.Equal(t, "k3", otherModels.Roles.Main.Model)
	require.Contains(t, other.SystemPrompt, "provider=kimi model=k3")
	// The toolkit uses the catalog rule provider, while the model and prompt
	// retain the user-selected provider name.
	require.Equal(t, "kimi-code-plan-cn", rt.Toolkit.ActiveProfile().ProviderName)
	require.Equal(t, "k3", rt.Toolkit.ActiveProfile().Model)
	require.True(t, rt.Toolkit.ToolSearchEnabled())
	require.False(t, rt.Toolkit.NativeDeferredToolDiscovery())
	// An already retained generation remains bound to its original prompt/model.
	require.Contains(t, initial.SystemPrompt, "provider=openai model=gpt-5.4")
}

// The public section capability receives the configured model alias, whereas
// the selected prompt service receives its resolved API model.
func TestPromptSectionUsesCandidateProviderAndConfiguredModelAlias(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", promptservice.NativeModule, "wuu.compaction.default", "wuu.agent.policy.default", "wuu.model.native", modelExecutionConsumer, capacityPolicyModule, capacityResourceModule, "fixture.section"}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "openai", Providers: map[string]config.ProviderConfig{
		"openai": {Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses", APIKey: "fixture", Model: "gpt-5.4"},
		"other":  {Type: "openai-compatible", BaseURL: "https://example.invalid/v1", WireAPI: "responses", APIKey: "fixture", Model: "display-model", Models: map[string]config.ProviderModelConfig{"display-model": {ID: "actual-api-model"}}},
	}}
	inputs := make(chan pluginhost.SystemPromptSectionInput, 16)
	section := NativeProviderFactory{ID: "fixture.section", Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &generationClient{id: "fixture.section", capabilities: []pluginhost.CapabilityDescriptor{{ID: pluginhost.CapabilityAgentSystemPromptSection, Kind: "transform", Version: 1}}, invoke: func(call pluginhost.CapabilityInvokeParams) (pluginhost.CapabilityInvokeResult, error) {
			var input pluginhost.SystemPromptSectionInput
			if err := json.Unmarshal(call.Input, &input); err != nil {
				return pluginhost.CapabilityInvokeResult{}, err
			}
			inputs <- input
			output, err := json.Marshal(pluginhost.SystemPromptSectionOutput{Text: "section"})
			return pluginhost.CapabilityInvokeResult{Output: output}, err
		}}, nil
	}}
	prompt := NativeProviderFactory{ID: promptservice.NativeModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &driverProviderClient{id: promptservice.NativeModule, descriptor: promptservice.NewService().ProvidedServices()[0], invoke: func(_ context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
			var input promptservice.Request
			if err := json.Unmarshal(call.Params, &input); err != nil {
				return nil, err
			}
			return json.Marshal(promptservice.Result{Content: fmt.Sprintf("provider=%s model=%s", input.Provider, input.Model), Sections: []promptservice.Section{}})
		}}, nil
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []NativeProviderFactory{section, prompt}})
	require.NoError(t, err)
	defer rt.Cleanup()
	initial := <-inputs
	require.Equal(t, "openai", initial.Provider)
	require.Equal(t, "gpt-5.4", initial.Model)
	require.NoError(t, rt.RefreshModelConfiguration(cfg, "other", "display-model"))
	changed := <-inputs
	require.Equal(t, "other", changed.Provider)
	require.Equal(t, "display-model", changed.Model)
	state, err := rt.RetainState()
	require.NoError(t, err)
	defer state.Close()
	models, err := state.Models()
	require.NoError(t, err)
	require.Equal(t, "display-model", models.Roles.Main.Model)
	require.Equal(t, "actual-api-model", models.Roles.Main.APIModel)
	require.Contains(t, state.SystemPrompt, "provider=other model=actual-api-model")
}

// Model activation can synchronously call a plugin, which can reenter advanced
// settings. A transition already in progress must reject rather than deadlock.
func TestAdvancedPublicationRejectsActiveTransitionWithoutBlocking(t *testing.T) {
	rt := &Session{}
	basis, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	rt.pluginTransitionMu.Lock()
	defer rt.pluginTransitionMu.Unlock()
	result := make(chan error, 1)
	go func() {
		_, err := rt.PublishAdvancedRuntime(basis, config.AgentConfig{Temperature: 0.8}, modelroles.Set{}, modelbudget.Budget{}, modelbudget.Budget{})
		result <- err
	}()
	select {
	case err := <-result:
		require.ErrorIs(t, err, ErrStaleAdvancedRuntime)
	case <-time.After(time.Second):
		t.Fatal("advanced publication waited on an active transition")
	}
	require.Zero(t, rt.runtimePublicationVersion.Load())
}

// Run reentrant callbacks in a child so a regression that holds the publication
// lock while invoking a provider can be killed without leaking a blocked test.
func TestSelectedPromptRetainStateReentry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSelectedPromptRetainStateReentryHelper$")
	cmd.Env = append(os.Environ(), "WUU_TEST_PROMPT_REENTRY=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("prompt callback could not retain state before timeout: %v", ctx.Err())
	}
	require.NoError(t, err, "child output: %s", output)
}

func TestSelectedPromptRetainStateReentryHelper(t *testing.T) {
	if os.Getenv("WUU_TEST_PROMPT_REENTRY") == "" {
		return
	}
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", promptservice.NativeModule, "wuu.compaction.default", "wuu.agent.policy.default", "wuu.model.native", modelExecutionConsumer, capacityPolicyModule, capacityResourceModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "openai", Providers: map[string]config.ProviderConfig{"openai": {Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses", APIKey: "fixture", Model: "gpt-5.4"}}}
	var owner atomic.Pointer[Session]
	captured := make(chan *State, 1)
	factory := NativeProviderFactory{ID: promptservice.NativeModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &driverProviderClient{id: promptservice.NativeModule, descriptor: promptservice.NewService().ProvidedServices()[0], invoke: func(_ context.Context, _ pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
			if rt := owner.Load(); rt != nil {
				state, err := rt.RetainState()
				if err != nil {
					return nil, err
				}
				select {
				case captured <- state:
				default:
					_ = state.Close()
				}
			}
			return json.Marshal(promptservice.Result{Content: "selected prompt", Sections: []promptservice.Section{}})
		}}, nil
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	require.NoError(t, err)
	defer rt.Cleanup()
	owner.Store(rt)
	_, err = rt.RefreshSystemPrompt("openai", "gpt-5.4")
	require.NoError(t, err)
	state := <-captured
	models, err := state.Models()
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", models.Roles.Main.Model)
	require.NoError(t, state.Close())
}

func TestModelGenerationPreparationAndCommittedActivationFailures(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", promptservice.NativeModule, "wuu.compaction.default", "wuu.agent.policy.default", "wuu.model.native", modelExecutionConsumer, capacityPolicyModule, capacityResourceModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "openai", Providers: map[string]config.ProviderConfig{"openai": {Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses", APIKey: "fixture", Model: "gpt-5.4"}}}
	var failPrompt atomic.Bool
	factory := NativeProviderFactory{ID: promptservice.NativeModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &driverProviderClient{id: promptservice.NativeModule, descriptor: promptservice.NewService().ProvidedServices()[0], invoke: func(_ context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
			if failPrompt.Load() {
				return nil, errors.New("controlled prompt failure")
			}
			var request promptservice.Request
			if err := json.Unmarshal(call.Params, &request); err != nil {
				return nil, err
			}
			return json.Marshal(promptservice.Result{Content: "prompt=" + request.Model, Sections: []promptservice.Section{}})
		}}, nil
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	require.NoError(t, err)
	defer rt.Cleanup()
	old, err := rt.RetainState()
	require.NoError(t, err)
	defer old.Close()
	oldModels, err := old.Models()
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", oldModels.Roles.Main.Model)
	require.Contains(t, old.SystemPrompt, "gpt-5.4")
	invalid := cfg
	invalidModules := []string{"wuu.model.native"}
	invalid.Composition = &composition.Config{Version: composition.Version, Modules: &invalidModules}
	err = rt.RefreshModelConfiguration(invalid, "openai", "gpt-4o", config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
	require.Error(t, err)
	require.False(t, PluginGenerationWasCommitted(err))
	unchanged, err := rt.RetainState()
	require.NoError(t, err)
	defer unchanged.Close()
	unchangedModels, err := unchanged.Models()
	require.NoError(t, err)
	require.Same(t, old.Generation, unchanged.Generation)
	require.Equal(t, oldModels.Roles.Main.Model, unchangedModels.Roles.Main.Model)
	require.Equal(t, oldModels.PermissionMode, unchangedModels.PermissionMode)
	require.Equal(t, old.SystemPrompt, unchanged.SystemPrompt)
	failPrompt.Store(true)
	err = rt.RefreshModelConfiguration(cfg, "openai", "gpt-4o", config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})
	require.ErrorContains(t, err, "controlled prompt failure")
	require.True(t, PluginGenerationWasCommitted(err), "activation failure must report committed, not pretend rollback")
	degraded, err := rt.RetainState()
	require.NoError(t, err)
	defer degraded.Close()
	degradedModels, err := degraded.Models()
	require.NoError(t, err)
	require.NotSame(t, old.Generation, degraded.Generation)
	require.Equal(t, "gpt-4o", degradedModels.Roles.Main.Model)
	require.Equal(t, config.PermissionModeReadOnly, degradedModels.PermissionMode)
	// An existing owner keeps the old graph through the failed activation.
	require.Equal(t, "gpt-5.4", oldModels.Roles.Main.Model)
	require.Contains(t, old.SystemPrompt, "gpt-5.4")
}

// A prompt prepared while another graph transition owns publication cannot
// publish into the old graph after that transition has begun.
func TestPromptRefreshRejectsActiveTransition(t *testing.T) {
	modules := []string{sessionStateConsumer, persistence.JSONLModule, kernelHostModule, loopdriver.DefaultDriverID, sessionExecutionConsumer, toolExecutionConsumer, StateServicesConsumer, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule, "wuu.session.default", "wuu.tools.default", promptservice.NativeModule, "wuu.compaction.default", "wuu.agent.policy.default", "wuu.model.native", modelExecutionConsumer, capacityPolicyModule, capacityResourceModule}
	cfg := config.Config{Composition: &composition.Config{Version: composition.Version, Modules: &modules}, DefaultProvider: "openai", Providers: map[string]config.ProviderConfig{"openai": {Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", WireAPI: "responses", APIKey: "fixture", Model: "gpt-5.4"}}}
	var changed atomic.Bool
	factory := NativeProviderFactory{ID: promptservice.NativeModule, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return &driverProviderClient{id: promptservice.NativeModule, descriptor: promptservice.NewService().ProvidedServices()[0], invoke: func(_ context.Context, _ pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
			content := "old prompt"
			if changed.Load() {
				content = "new prompt"
			}
			return json.Marshal(promptservice.Result{Content: content, Sections: []promptservice.Section{}})
		}}, nil
	}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true, NativeProviders: []NativeProviderFactory{factory}})
	require.NoError(t, err)
	defer rt.Cleanup()
	old, err := rt.RetainState()
	require.NoError(t, err)
	defer old.Close()
	require.Equal(t, "old prompt", old.SystemPrompt)
	rt.pluginTransitionMu.Lock()
	defer rt.pluginTransitionMu.Unlock()
	changed.Store(true)
	done := make(chan error, 1)
	go func() { _, err := rt.RefreshSystemPrompt("openai", "gpt-5.4"); done <- err }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrStateGenerationChanged)
	case <-time.After(5 * time.Second):
		t.Fatal("prompt refresh waited on an active transition")
	}
	current, err := rt.RetainState()
	require.NoError(t, err)
	defer current.Close()
	require.Equal(t, "old prompt", current.SystemPrompt)
	require.Same(t, old.Generation, current.Generation)
}
