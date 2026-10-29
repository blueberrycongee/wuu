package appserver

import (
	"context"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/stretchr/testify/require"
)

// Retaining a state concurrently with a committed model update must not read
// fields while the workspace runtime publishes them.
func TestModelPublicationAndStateRetentionAreSynchronized(t *testing.T) {
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", APIKey: "fixture", Model: "fixture"}}}
	rt := newEngineAdminRuntime(t, cfg)
	services, release, err := rt.RetainStateServices()
	require.NoError(t, err)
	view, err := services.Config.Read(context.Background())
	release()
	require.NoError(t, err)
	cfg = view.Config
	provider, name, err := cfg.ResolveProvider("fixture")
	require.NoError(t, err)
	selection := modelvariant.ResolveForProvider(name, provider, "fixture", cfg.Agent.Variant, cfg.Agent.Effort)
	roles, err := modelroles.Resolve(cfg, modelroles.ResolveOptions{ProviderName: name, ProviderConfig: provider, Model: "fixture", Effort: selection.LegacyEffort, Variant: selection.Variant})
	require.NoError(t, err)
	srv := New(rt, &lockedBuffer{})
	defer srv.Close()
	start, done := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		state, err := rt.RetainState()
		if err == nil {
			_ = state.Close()
		}
		close(start)
		for {
			select {
			case <-done:
				return
			default:
			}
			state, err := rt.RetainState()
			if err == nil {
				_ = state.Close()
			}
		}
	}()
	<-start
	var applyErr error
	for i := 0; i < 4; i++ {
		err := srv.applyModelSelectionToRuntime(cfg, "fixture", "fixture", name, provider, selection, roles, false, true)
		if err != nil {
			applyErr = err
			break
		}
	}
	close(done)
	wg.Wait()
	require.NoError(t, applyErr)
}
