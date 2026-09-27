package appserver

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestRunningServerObservesPluginGenerationAndRetriesFailedRefresh(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	if srv.startupErr != nil {
		t.Fatal(srv.startupErr)
	}
	initialEpoch := srv.pluginGenerationEpoch.Load()
	var refreshCalls atomic.Int32
	var failRefresh atomic.Bool
	srv.refreshExtensionsForTest = func(config.Config) error {
		refreshCalls.Add(1)
		if failRefresh.Load() {
			return errors.New("candidate activation failed")
		}
		return nil
	}

	firstEpoch := advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	waitPluginGenerationWatchTest(t, func() bool {
		return srv.pluginGenerationEpoch.Load() == firstEpoch && strings.Count(out.String(), NotificationPluginInventoryChanged) == 1
	})
	if firstEpoch != initialEpoch+1 || refreshCalls.Load() != 1 {
		t.Fatalf("first observed generation: epoch=%d calls=%d", firstEpoch, refreshCalls.Load())
	}

	failRefresh.Store(true)
	failedEpoch := advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	waitPluginGenerationWatchTest(t, func() bool { return refreshCalls.Load() >= 2 })
	time.Sleep(2 * pluginGenerationRetryInterval)
	if got := srv.pluginGenerationEpoch.Load(); got != firstEpoch {
		t.Fatalf("failed refresh advanced active epoch to %d, want %d", got, firstEpoch)
	}
	if got := strings.Count(out.String(), NotificationPluginInventoryChanged); got != 1 {
		t.Fatalf("failed refresh emitted inventory notification: %d", got)
	}

	failRefresh.Store(false)
	waitPluginGenerationWatchTest(t, func() bool {
		return srv.pluginGenerationEpoch.Load() == failedEpoch && strings.Count(out.String(), NotificationPluginInventoryChanged) == 2
	})
}

func TestPluginGenerationMutationWaitsForSameServerRefresh(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	refreshStarted := make(chan struct{})
	allowRefresh := make(chan struct{})
	srv := &Server{
		rt:      rt,
		out:     &lockedBuffer{},
		threads: map[string]*threadState{},
		refreshExtensionsForTest: func(config.Config) error {
			close(refreshStarted)
			<-allowRefresh
			return nil
		},
	}
	advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)

	refreshDone := make(chan error, 1)
	go func() { refreshDone <- srv.refreshPluginGenerationIfChanged() }()
	select {
	case <-refreshStarted:
	case <-time.After(3 * time.Second):
		close(allowRefresh)
		t.Fatal("timed out waiting for generation refresh to start")
	}

	type mutationResult struct {
		release func()
		err     error
	}
	mutationDone := make(chan mutationResult, 1)
	go func() {
		release, err := srv.beginPluginGenerationMutation("change", pluginGenerationMutationExclusive)
		mutationDone <- mutationResult{release: release, err: err}
	}()
	waitPluginGenerationWatchTest(t, srv.pluginGenerationMutation.Load)
	select {
	case result := <-mutationDone:
		close(allowRefresh)
		if result.release != nil {
			result.release()
		}
		t.Fatalf("mutation returned during same-server refresh: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}

	close(allowRefresh)
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-mutationDone:
		if result.err != nil {
			t.Fatalf("mutation failed after refresh: %v", result.err)
		}
		result.release()
	case <-time.After(3 * time.Second):
		t.Fatal("mutation remained blocked after refresh")
	}
}

func advancePluginGenerationWatchTestEpoch(t *testing.T, wuuHome string) uint64 {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lease *session.PluginGenerationLease
	for time.Now().Before(deadline) {
		candidate, acquired, err := session.TryAcquirePluginGenerationMutationLease(wuuHome)
		if err != nil {
			t.Fatal(err)
		}
		if acquired {
			lease = candidate
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lease == nil {
		t.Fatal("timed out acquiring mutation lease")
	}
	epoch, err := lease.Advance()
	if err != nil {
		_ = lease.Release()
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return epoch
}

func waitPluginGenerationWatchTest(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for running app-server plugin generation refresh")
}
