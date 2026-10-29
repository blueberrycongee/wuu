package appserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAdvancedSettingsSavedDuringAdmittedTurnApplyOnNextTurn(t *testing.T) {
	stream := newBlockingStreamClient("finished normally")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = stream
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	kit, err := tools.New(rt.RootDir)
	require.NoError(t, err)
	rt.Toolkit = kit
	require.NoError(t, os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`), 0600))
	entered, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	barrier := &advancedCommitBarrier{Service: config.NewService(config.Module, rt.ConfigPath, ""), armed: &armed, entered: entered, release: release}
	credentials := credentialstore.NewService("test.selected-credentials", func() (credentialstore.Store, error) { return nativeCredentialFixture(t, os.Getenv("HOME")), nil })
	attachTestModelGeneration(t, rt, barrier, credentials, &configSourceConsumer{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		select {
		case <-stream.release:
		default:
			close(stream.release)
		}
		srv.Close()
	})
	require.NoError(t, srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)))
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	th := srv.thread(threadID)
	old, err := srv.ensureThreadRuntime(th)
	require.NoError(t, err)
	oldTemperature := old.StreamRunner.Temperature
	armed.Store(true)
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- srv.handleLine(context.Background(), []byte(`{"id":"settings","method":"config/advanced/update","params":{"temperature":0.8}}`))
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("settings did not reach the commit barrier")
	}
	require.NoError(t, srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"hello"}}`, threadID))))
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "turn")["error"])
	select {
	case <-stream.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach the model")
	}
	th.mu.Lock()
	idle := th.addIdleWaiterLocked()
	require.True(t, th.running)
	require.Same(t, old, th.execRuntime)
	th.mu.Unlock()
	unblock()
	select {
	case err := <-updateDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("settings did not finish")
	}
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "settings")["error"])
	th.mu.Lock()
	pending, same := th.pendingRuntimeReset, th.execRuntime == old
	th.mu.Unlock()
	require.True(t, same)
	require.Equal(t, oldTemperature, old.StreamRunner.Temperature)
	require.True(t, pending, "a running turn must defer its saved settings to the next admission")
	close(stream.release)
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not settle")
	}
	admission, selected, err := srv.retainThreadAdmissionState(threadID)
	require.NoError(t, err)
	defer admission.Close()
	require.NotNil(t, selected)
	selected.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(selected, 0)
	selected.mu.Unlock()
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { selected.mu.Lock(); selected.releaseThreadExecutionLeaseLocked(); selected.mu.Unlock() }()
	next, err := srv.ensureThreadRuntimeAfterAdmission(selected)
	require.NoError(t, err)
	require.NotSame(t, old, next)
	require.Equal(t, 0.8, next.StreamRunner.Temperature)
}

func TestAdvancedDerivationFinishingDuringAdmittedTurnDefersUpdate(t *testing.T) {
	stream := newBlockingStreamClient("finished normally")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = stream
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	kit, err := tools.New(rt.RootDir)
	require.NoError(t, err)
	rt.Toolkit = kit
	require.NoError(t, os.WriteFile(rt.ConfigPath, []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`), 0600))
	bindConfigSourceForTest(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		select {
		case <-stream.release:
		default:
			close(stream.release)
		}
		srv.Close()
	})
	require.NoError(t, srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)))
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	th := srv.thread(threadID)
	old, err := srv.ensureThreadRuntime(th)
	require.NoError(t, err)
	oldTemperature := old.StreamRunner.Temperature
	services, releaseServices, err := rt.RetainStateServices()
	require.NoError(t, err)
	view, err := services.Config.Read(context.Background())
	require.NoError(t, err)
	value := 0.8
	source, err := config.UpdateAdvancedRuntimeSource(view.Source, "fake-provider", config.AdvancedRuntimeUpdate{Temperature: &value})
	require.NoError(t, err)
	view.Config.Agent.Temperature = value
	state, err := rt.RetainState()
	require.NoError(t, err)
	models, err := state.Models()
	require.NoError(t, err)
	require.NoError(t, state.Close())
	basis, err := rt.CaptureAdvancedRuntime()
	require.NoError(t, err)
	token, err := rt.PublishAdvancedRuntime(basis, view.Config.Agent, models.Roles, rt.ModelBudget, rt.WorkerModelBudget)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.propagateIdleThreadAdvancedRuntime(view.Config, token, func(b runtime.AdvancedRuntimeBasis, cfg config.Config, selection runtime.ThreadModelSelection) (runtime.ThreadModelDerivation, error) {
			result, err := rt.PrepareAdvancedThreadDerivation(b, cfg, selection)
			close(entered)
			<-release
			return result, err
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("derivation barrier not reached")
	}
	require.NoError(t, srv.handleLine(context.Background(), []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"hello"}}`, threadID))))
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "turn")["error"])
	select {
	case <-stream.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach the model")
	}
	_, err = services.Config.CompareExchange(context.Background(), view.Revision, source)
	releaseServices()
	require.NoError(t, err)
	th.mu.Lock()
	idle := th.addIdleWaiterLocked()
	require.True(t, th.running)
	require.Same(t, old, th.execRuntime)
	th.mu.Unlock()
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("propagation did not finish")
	}
	th.mu.Lock()
	pending := th.pendingRuntimeReset
	th.mu.Unlock()
	require.True(t, pending, "turn admitted during derivation must defer saved settings")
	require.Equal(t, oldTemperature, old.StreamRunner.Temperature)
	close(stream.release)
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	admission, selected, err := srv.retainThreadAdmissionState(threadID)
	require.NoError(t, err)
	defer admission.Close()
	selected.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(selected, 0)
	selected.mu.Unlock()
	require.NoError(t, err)
	require.True(t, acquired)
	defer func() { selected.mu.Lock(); selected.releaseThreadExecutionLeaseLocked(); selected.mu.Unlock() }()
	next, err := srv.ensureThreadRuntimeAfterAdmission(selected)
	require.NoError(t, err)
	require.NotSame(t, old, next)
	require.Equal(t, value, next.StreamRunner.Temperature)
}
