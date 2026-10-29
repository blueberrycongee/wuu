package appserver

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/stretchr/testify/require"
)

func publicationTestServer(t *testing.T) (*runtime.Session, *Server, *lockedBuffer) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	require.NoError(t, os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`), 0600))
	rt = newSettingsRefreshRuntime(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	return rt, srv, out
}

// The reader has entered before updates begin and continues until they finish;
// a captured state must consist of one complete advanced-settings publication.
func TestAdvancedConfigPublicationAndStateRetentionAreSynchronized(t *testing.T) {
	rt, srv, out := publicationTestServer(t)
	update := func(id string, value string) error {
		req := fmt.Sprintf(`{"id":%q,"method":"config/advanced/update","params":{"temperature":%s,"compact_threshold_pct":%s}}`, id, value, value)
		return srv.handleLine(context.Background(), []byte(req))
	}
	require.NoError(t, update("seed", "0.2"))
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "seed")["error"])
	ready, done := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	var snapshots []string
	wg.Add(1)
	go func() {
		defer wg.Done()
		state, err := rt.RetainState()
		if err == nil {
			_, _ = state.Models()
			_ = state.Close()
		}
		close(ready)
		for {
			select {
			case <-done:
				return
			default:
			}
			state, err := rt.RetainState()
			if err != nil {
				continue
			}
			models, err := state.Models()
			_ = state.Close()
			if err == nil && models.Temperature != models.CompactThresholdPct && len(snapshots) < 4 {
				snapshots = append(snapshots, fmt.Sprintf("temperature=%v compact=%v", models.Temperature, models.CompactThresholdPct))
			}
		}
	}()
	<-ready
	var updateErr error
	for i := 0; i < 6; i++ {
		value := "0.2"
		if i%2 == 0 {
			value = "0.8"
		}
		if updateErr = update(fmt.Sprintf("change-%d", i), value); updateErr != nil {
			break
		}
	}
	close(done)
	wg.Wait()
	require.NoError(t, updateErr)
	for i := 0; i < 6; i++ {
		require.Nil(t, responseByID(t, parseOutput(t, out.String()), fmt.Sprintf("change-%d", i))["error"])
	}
	require.Empty(t, snapshots, "advanced settings must not be partially captured")
}

// Permission mode is captured by State.Models and must be published under the
// same synchronization as concurrent model configuration updates.
func TestPermissionModePublicationAndStateRetentionAreSynchronized(t *testing.T) {
	rt, srv, out := publicationTestServer(t)
	ready, done := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		state, err := rt.RetainState()
		if err == nil {
			_, _ = state.Models()
			_ = state.Close()
		}
		close(ready)
		for {
			select {
			case <-done:
				return
			default:
			}
			state, err := rt.RetainState()
			if err == nil {
				_, _ = state.Models()
				_ = state.Close()
			}
		}
	}()
	<-ready
	var updateErr error
	for i := 0; i < 6; i++ {
		mode := "read_only"
		if i%2 == 0 {
			mode = "unconfined"
		}
		req := fmt.Sprintf(`{"id":%q,"method":"config/model/update","params":{"model":"fake-model","permission_mode":%q}}`, fmt.Sprintf("mode-%d", i), mode)
		if updateErr = srv.handleLine(context.Background(), []byte(req)); updateErr != nil {
			break
		}
	}
	close(done)
	wg.Wait()
	require.NoError(t, updateErr)
	for i := 0; i < 6; i++ {
		require.Nil(t, responseByID(t, parseOutput(t, out.String()), fmt.Sprintf("mode-%d", i))["error"])
	}
	state, err := rt.RetainState()
	require.NoError(t, err)
	defer state.Close()
	models, err := state.Models()
	require.NoError(t, err)
	require.Equal(t, "read_only", models.PermissionMode)
}

// Even with an unchanged model and generation, an older resolved advanced
// update cannot overwrite the newer publication (including an ABA update).
func TestAdvancedRuntimeRejectsStalePublication(t *testing.T) {
	rt, _, _ := publicationTestServer(t)
	old, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	state, err := rt.RetainState()
	require.NoError(t, err)
	models, err := state.Models()
	require.NoError(t, err)
	require.NoError(t, state.Close())
	budget := modelbudget.Budget{ContextWindowTokens: 42}
	first := config.AgentConfig{Temperature: 0.8, CompactThresholdPct: 0.8}
	_, err = rt.PublishAdvancedRuntime(old, first, models.Roles, budget, budget)
	require.NoError(t, err)
	second := config.AgentConfig{Temperature: 0.2, CompactThresholdPct: 0.2}
	_, err = rt.PublishAdvancedRuntime(old, second, modelroles.Set{}, modelbudget.Budget{}, modelbudget.Budget{})
	require.ErrorIs(t, err, runtime.ErrStaleAdvancedRuntime)
	state, err = rt.RetainState()
	require.NoError(t, err)
	defer state.Close()
	models, err = state.Models()
	require.NoError(t, err)
	require.Equal(t, 0.8, models.Temperature)
	require.Equal(t, 0.8, models.CompactThresholdPct)
}
