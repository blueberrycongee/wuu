package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/stretchr/testify/require"
)

type advancedCommitBarrier struct {
	*config.Service
	armed   *atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (p *advancedCommitBarrier) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	result, err := p.Service.InvokeService(ctx, call)
	if err == nil && call.Method == "compare-exchange" && p.armed.CompareAndSwap(true, false) {
		close(p.entered)
		<-p.release
	}
	return result, err
}

// A settings write has committed when another model publication invalidates
// the original basis. The handler must reread the authority and converge the
// live runtime to that saved update instead of leaving stale in-memory values.
func TestAdvancedSavedConfigReconcilesWhenPublicationBasisChanges(t *testing.T) {
	previous := newTestRuntime(t, &fakeClient{})
	require.NoError(t, os.WriteFile(previous.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`), 0600))
	entered, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	factory := runtime.NativeProviderFactory{ID: config.Module, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		return &advancedCommitBarrier{Service: config.NewService(config.Module, previous.ConfigPath, ""), armed: &armed, entered: entered, release: release}, nil
	}}
	rt := newConfigRefreshRuntime(t, previous, factory)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	armed.Store(true)
	done := make(chan error, 1)
	go func() {
		done <- srv.handleLine(context.Background(), []byte(`{"id":"advanced","method":"config/advanced/update","params":{"temperature":0.8,"compact_threshold_pct":0.8}}`))
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		unblock()
		t.Fatal("saved config barrier was not reached")
	}
	var interferenceErr error
	require.Eventually(t, func() bool {
		basis, err := rt.CaptureAdvancedRuntime()
		if err != nil {
			interferenceErr = err
			return true
		}
		state, err := rt.RetainState()
		if err != nil {
			interferenceErr = err
			return true
		}
		models, err := state.Models()
		_ = state.Close()
		if err != nil {
			interferenceErr = err
			return true
		}
		_, err = rt.PublishAdvancedRuntime(basis, config.AgentConfig{Temperature: 0.1, CompactThresholdPct: 0.1}, models.Roles, modelbudget.Budget{}, modelbudget.Budget{})
		if errors.Is(err, runtime.ErrStaleAdvancedRuntime) {
			return false
		}
		interferenceErr = err
		return true
	}, 5*time.Second, 10*time.Millisecond, "interference publication remained stale")
	require.NoError(t, interferenceErr)
	// A later writer changes the authority after the first save but before its
	// reply; retry must read this newer revision rather than republish 0.8.
	services, releaseServices, err := rt.RetainStateServices()
	require.NoError(t, err)
	view, err := services.Config.Read(context.Background())
	require.NoError(t, err)
	replacement := 0.6
	source, err := config.UpdateAdvancedRuntimeSource(view.Source, "fake-provider", config.AdvancedRuntimeUpdate{Temperature: &replacement, CompactThresholdPct: &replacement})
	require.NoError(t, err)
	_, err = services.Config.CompareExchange(context.Background(), view.Revision, source)
	require.NoError(t, err)
	releaseServices()
	unblock()
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("advanced update did not finish")
	}
	response := responseByID(t, parseOutput(t, out.String()), "advanced")
	require.Nil(t, response["error"], "saved update must reconcile against the current runtime basis")
	state, err := rt.RetainState()
	require.NoError(t, err)
	defer state.Close()
	models, err := state.Models()
	require.NoError(t, err)
	require.Equal(t, 0.6, models.Temperature)
	require.Equal(t, 0.6, models.CompactThresholdPct)
	services, releaseServices, err = rt.RetainStateServices()
	require.NoError(t, err)
	defer releaseServices()
	view, err = services.Config.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0.6, view.Config.Agent.Temperature)
	require.Equal(t, 0.6, view.Config.Agent.CompactThresholdPct)
}

// Each read returns the real selected source, then causes a newer runtime
// publication before the handler can apply its previously captured basis.
type advancingConfigSource struct {
	*config.Service
	owner    *atomic.Pointer[runtime.Session]
	armed    atomic.Bool
	advances atomic.Int32
}

func (p *advancingConfigSource) InvokeService(ctx context.Context, call pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	result, err := p.Service.InvokeService(ctx, call)
	if err != nil {
		return result, err
	}
	if call.Method == "compare-exchange" {
		p.armed.Store(true)
	}
	if p.armed.Load() && (call.Method == "compare-exchange" || call.Method == "read") {
		rt := p.owner.Load()
		if rt == nil {
			return nil, errors.New("fixture runtime unavailable")
		}
		var publishErr error
		for i := 0; i < 30; i++ {
			basis, e := rt.CaptureAdvancedRuntime()
			if e != nil {
				return nil, e
			}
			state, e := rt.RetainState()
			if e != nil {
				return nil, e
			}
			models, e := state.Models()
			_ = state.Close()
			if e != nil {
				return nil, e
			}
			_, publishErr = rt.PublishAdvancedRuntime(basis, config.AgentConfig{Temperature: 0.1, CompactThresholdPct: 0.1}, models.Roles, modelbudget.Budget{}, modelbudget.Budget{})
			if !errors.Is(publishErr, runtime.ErrStaleAdvancedRuntime) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if publishErr != nil {
			return nil, publishErr
		}
		p.advances.Add(1)
	}
	return result, nil
}

func TestAdvancedSavedConfigNeverReportsSuccessAfterRepeatedStale(t *testing.T) {
	previous := newTestRuntime(t, &fakeClient{})
	require.NoError(t, os.WriteFile(previous.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`), 0600))
	var owner atomic.Pointer[runtime.Session]
	var source atomic.Pointer[advancingConfigSource]
	factory := runtime.NativeProviderFactory{ID: config.Module, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
		p := &advancingConfigSource{Service: config.NewService(config.Module, previous.ConfigPath, ""), owner: &owner}
		source.Store(p)
		return p, nil
	}}
	rt := newConfigRefreshRuntime(t, previous, factory)
	owner.Store(rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	require.NoError(t, srv.handleLine(context.Background(), []byte(`{"id":"advanced-stale","method":"config/advanced/update","params":{"temperature":0.8,"compact_threshold_pct":0.8}}`)))
	response := responseByID(t, parseOutput(t, out.String()), "advanced-stale")
	require.NotNil(t, response["error"], "an exhausted publication must not report success")
	require.Contains(t, fmt.Sprint(response["error"]), "saved")
	require.GreaterOrEqual(t, source.Load().advances.Load(), int32(3), "fixture must force every publication attempt stale")
	state, err := rt.RetainState()
	require.NoError(t, err)
	defer state.Close()
	models, err := state.Models()
	require.NoError(t, err)
	require.Equal(t, 0.1, models.Temperature, "failed publication must not be presented as applied")
}
