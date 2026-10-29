package appserver

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"

	"github.com/stretchr/testify/require"
)

// A stale propagation must not replace the model-specific defaults already
// published by a newer update, even when the conversation pins another model.
func TestAdvancedThreadPropagationRejectsLateOlderConfiguration(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	rt = newSettingsRefreshRuntime(t, rt)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	thread := cachedForeignPinnedThread(t, srv, rt, "pinned-thread")
	built, err := srv.ensureThreadRuntime(thread)
	require.NoError(t, err)
	older, _, err := rt.LoadEffectiveConfig()
	require.NoError(t, err)
	newer := older
	newer.Providers = make(map[string]config.ProviderConfig, len(older.Providers))
	for key, provider := range older.Providers {
		newer.Providers[key] = provider
	}
	provider := newer.Providers["alt-provider"]
	provider.ContextWindow = 200000
	newer.Providers["alt-provider"] = provider

	// Hold the exact token from each successful root publication. The newer
	// propagation finishes first and the earlier callback then arrives late.
	state, err := rt.RetainState()
	require.NoError(t, err)
	models, err := state.Models()
	require.NoError(t, err)
	require.NoError(t, state.Close())
	basis, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	olderToken, err := rt.PublishAdvancedRuntime(basis, older.Agent, models.Roles, rt.ModelBudget, rt.WorkerModelBudget)
	require.NoError(t, err)
	basis, err = rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	newerToken, err := rt.PublishAdvancedRuntime(basis, newer.Agent, models.Roles, rt.ModelBudget, rt.WorkerModelBudget)
	require.NoError(t, err)
	srv.updateIdleThreadAdvancedRuntime(newer, newerToken)
	require.Equal(t, 200000, built.ModelBudget.ContextWindowTokens)
	srv.updateIdleThreadAdvancedRuntime(older, olderToken)
	thread.mu.Lock()
	pendingReset := thread.pendingRuntimeReset
	thread.mu.Unlock()
	require.False(t, pendingReset, "stale propagation must not schedule a reset for a newer thread")
	require.Equal(t, 200000, built.ModelBudget.ContextWindowTokens, "late old propagation overwrote the newer pinned budget")
	require.Equal(t, "alt-provider", built.Selection.Provider)
	require.Equal(t, "alt-model", built.Selection.Model)
}

// An idle pinned thread keeps its old graph until the next admitted use. It
// must never receive a worker client prepared for a different graph.
func TestAdvancedOldGenerationRebuildsAtNextAdmission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	rt = newSettingsRefreshRuntime(t, rt)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	thread := cachedForeignPinnedThread(t, srv, rt, "pinned-rebuild")
	old, err := srv.ensureThreadRuntime(thread)
	require.NoError(t, err)
	oldGeneration := old.PluginGeneration
	require.NotNil(t, oldGeneration)
	require.NotNil(t, old.AgentControl)
	oldWorkerProvider := old.AgentControl.WorkerProviderName()
	oldTemperature := old.StreamRunner.Temperature
	cfg, _, err := rt.LoadEffectiveConfig()
	require.NoError(t, err)
	cfg.Agent.Temperature = 0.83
	require.NoError(t, rt.RefreshModelConfiguration(cfg, "fake-provider", "fake-model"))
	state, err := rt.RetainState()
	require.NoError(t, err)
	models, err := state.Models()
	require.NoError(t, err)
	require.NotSame(t, oldGeneration, state.Generation)
	require.NoError(t, state.Close())
	basis, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	token, err := rt.PublishAdvancedRuntime(basis, cfg.Agent, models.Roles, rt.ModelBudget, rt.WorkerModelBudget)
	require.NoError(t, err)
	srv.updateIdleThreadAdvancedRuntime(cfg, token)
	thread.mu.Lock()
	require.True(t, thread.pendingRuntimeReset)
	require.Same(t, old, thread.execRuntime)
	thread.mu.Unlock()
	require.Equal(t, oldWorkerProvider, old.AgentControl.WorkerProviderName())
	require.Equal(t, oldTemperature, old.StreamRunner.Temperature)

	admission, selected, err := srv.retainThreadAdmissionState(thread.ID)
	require.NoError(t, err)
	defer admission.Close()
	require.NotNil(t, selected)
	selected.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(selected, 0)
	selected.mu.Unlock()
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { selected.mu.Lock(); selected.releaseThreadExecutionLeaseLocked(); selected.mu.Unlock() }()
	rebuilt, err := srv.ensureThreadRuntimeAfterAdmission(selected)
	require.NoError(t, err)
	require.NotSame(t, old, rebuilt)
	require.Same(t, admission.Generation, rebuilt.PluginGeneration)
	require.Equal(t, "alt-provider", rebuilt.Selection.Provider)
	require.Equal(t, "alt-model", rebuilt.Selection.Model)
	require.Equal(t, "alt-provider", rebuilt.AgentControl.WorkerProviderName())
	require.Equal(t, cfg.Agent.Temperature, rebuilt.StreamRunner.Temperature)
}

func TestAdvancedThreadPropagationSurvivesPromptOnlyPublication(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	writeTwoProviderSelectionConfig(t, rt.ConfigPath)
	rt = newSettingsRefreshRuntime(t, rt)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	thread := cachedForeignPinnedThread(t, srv, rt, "pinned-prompt-refresh")
	built, err := srv.ensureThreadRuntime(thread)
	require.NoError(t, err)
	cfg, _, err := rt.LoadEffectiveConfig()
	require.NoError(t, err)
	state, err := rt.RetainState()
	require.NoError(t, err)
	models, err := state.Models()
	require.NoError(t, err)
	require.NoError(t, state.Close())
	older := cfg
	older.Agent.Temperature = 0.31
	basis, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	oldToken, err := rt.PublishAdvancedRuntime(basis, older.Agent, models.Roles, rt.ModelBudget, rt.WorkerModelBudget)
	require.NoError(t, err)
	newer := cfg
	newer.Agent.Temperature = 0.83
	basis, err = rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	newToken, err := rt.PublishAdvancedRuntime(basis, newer.Agent, models.Roles, rt.ModelBudget, rt.WorkerModelBudget)
	require.NoError(t, err)
	_, err = rt.RefreshSystemPrompt("fake-provider", "fake-model")
	require.NoError(t, err)
	srv.updateIdleThreadAdvancedRuntime(newer, newToken)
	require.Equal(t, 0.83, built.StreamRunner.Temperature, "a prompt-only refresh must not discard committed settings propagation")
	srv.updateIdleThreadAdvancedRuntime(older, oldToken)
	require.Equal(t, 0.83, built.StreamRunner.Temperature, "older settings must remain fenced")
}
