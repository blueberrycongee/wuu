package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/stretchr/testify/require"
)

func TestModelCandidateRejectsSupersededPublication(t *testing.T) {
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", APIKey: "fixture", Model: "old"}}}
	rt, err := NewSession(Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, NonInteractive: true})
	require.NoError(t, err)
	defer rt.Cleanup()
	basis, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	prep, err := rt.preparePluginCatalog(cfg, discoverPlugins(rt.RootDir, rt.WuuHome), nil, nil, startPluginClient)
	require.NoError(t, err)
	prep.modelSelection = &generationModelSelection{provider: "fixture", model: "new", basisVersion: basis.version}
	candidate, err := prep.Build(context.Background())
	require.NoError(t, err)
	_, err = rt.PublishAdvancedRuntime(basis, cfg.Agent, modelroles.Set{}, modelbudget.Budget{}, modelbudget.Budget{})
	require.NoError(t, err)
	err = rt.ActivatePluginGeneration(candidate, nil)
	require.ErrorIs(t, err, ErrStaleModelPublication)
	require.Equal(t, "old", rt.Model)
}

func TestModelCandidateDoesNotWaitOnReentrantTransition(t *testing.T) {
	old := testPluginGeneration("old", &generationClient{id: "old"})
	rt := testGenerationSession(t, old)
	defer rt.Cleanup()
	client := &generationClient{id: "next"}
	candidate := testPluginGeneration("next", client)
	candidate.modelSelection = &generationModelSelection{provider: "fixture", model: "next", basisVersion: rt.runtimePublicationVersion.Load()}
	rt.pluginTransitionMu.Lock()
	done := make(chan error, 1)
	go func() { done <- rt.ActivatePluginGeneration(candidate, nil) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrStaleModelPublication)
		require.True(t, client.closed)
	case <-time.After(time.Second):
		rt.pluginTransitionMu.Unlock()
		<-done
		t.Fatal("reentrant model publication blocked on active transition")
	}
	rt.pluginTransitionMu.Unlock()
}
