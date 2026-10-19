package appserver

import (
	"errors"
	"os"
	"path/filepath"
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
	// Unrelated home-directory notifications and turn admission must not bypass
	// the retry window for exactly the same failed candidate.
	for range 5 {
		if err := os.WriteFile(filepath.Join(rt.WuuHome, "unrelated"), []byte("noise"), 0600); err != nil {
			t.Fatal(err)
		}
		_ = srv.refreshPluginGenerationIfChanged()
	}
	time.Sleep(2 * pluginGenerationRetryInterval)
	if got := refreshCalls.Load(); got != 2 {
		t.Fatalf("unchanged failed generation rebuilt during backoff: %d attempts", got-1)
	}
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

	failRefresh.Store(true)
	advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	waitPluginGenerationWatchTest(t, func() bool { return refreshCalls.Load() == 4 })
	// A new publication bypasses the old candidate's wait immediately.
	failRefresh.Store(false)
	repairedEpoch := advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	waitPluginGenerationWatchTest(t, func() bool { return srv.pluginGenerationEpoch.Load() == repairedEpoch })
	if got := refreshCalls.Load(); got != 5 {
		t.Fatalf("new publication attempts = %d, want 5", got)
	}
	failRefresh.Store(true)
	advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	waitPluginGenerationWatchTest(t, func() bool { return refreshCalls.Load() == 6 })
	srv.Close()
	_ = srv.refreshPluginGenerationIfChanged()
	if got := refreshCalls.Load(); got != 6 {
		t.Fatalf("closed server retried refresh: %d calls", got)
	}
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
		release func() error
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

func TestPluginRefreshBackoffTracksCandidateInputsAndPreservesBusyRetry(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	calls := 0
	failure := errors.New("candidate initialization failed")
	srv := &Server{rt: rt, out: &lockedBuffer{}, threads: map[string]*threadState{},
		refreshExtensionsForTest: func(config.Config) error { calls++; return failure },
	}
	advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	for attempt := 0; attempt < 8; attempt++ {
		if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, failure) {
			t.Fatalf("attempt %d lost failure: %v", attempt, err)
		}
		before := calls
		for range 3 {
			if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, errPluginGenerationRefreshDeferred) || !errors.Is(err, failure) {
				t.Fatalf("deferred refresh lost pending state or cause: %v", err)
			}
		}
		if calls != before {
			t.Fatalf("same candidate rebuilt inside retry window: %d -> %d", before, calls)
		}
		want := min(pluginGenerationFailureRetryInterval<<attempt, pluginGenerationFailureRetryMax)
		if got := srv.pluginRefreshFailure.delay; got != want {
			t.Fatalf("attempt %d delay = %s, want %s", attempt, got, want)
		}
		// Advance the deadline deterministically, without spending a minute
		// waiting for the cap or substituting the actual refresh operation.
		srv.pluginRefreshFailure.retryAt = time.Time{}
	}
	before := calls
	lease, acquired, err := session.TryAcquirePluginCatalogMutationLease(rt.WuuHome)
	if err != nil || !acquired {
		t.Fatalf("acquire catalog mutation: %t, %v", acquired, err)
	}
	for range 3 {
		if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, errPluginGenerationRefreshBusy) {
			t.Fatalf("catalog contention reported as candidate failure: %v", err)
		}
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if calls != before {
		t.Fatal("catalog contention attempted candidate initialization")
	}
	if err := srv.refreshPluginGenerationIfChanged(); !errors.Is(err, failure) || calls != before+1 {
		t.Fatalf("released catalog did not retry immediately: calls=%d err=%v", calls, err)
	}
	for _, change := range []struct {
		name  string
		apply func()
	}{
		{"config", func() { writePluginPackageFile(t, rt.ConfigPath, `{"agent":{"effort":"high"}}`) }},
		{"package", func() {
			writePluginPackageFile(t, filepath.Join(rt.WuuHome, "plugins", "candidate", "plugin.json"), `{"id":"candidate","description":"repaired input"}`)
		}},
		{"epoch", func() { advancePluginGenerationWatchTestEpoch(t, rt.WuuHome) }},
	} {
		before := calls
		change.apply()
		var err error
		if change.name == "package" {
			// Nested package changes are observed at the shared polling cadence,
			// while epoch and config changes bypass that observation limit.
			waitPluginGenerationWatchTest(t, func() bool {
				err = srv.refreshPluginGenerationIfChanged()
				return calls > before
			})
		} else {
			err = srv.refreshPluginGenerationIfChanged()
		}
		if !errors.Is(err, failure) || calls != before+1 {
			t.Fatalf("%s change did not bypass old candidate backoff: calls=%d err=%v", change.name, calls, err)
		}
		if got := srv.pluginRefreshFailure.delay; got != pluginGenerationFailureRetryInterval {
			t.Fatalf("%s change inherited previous failure delay: %s", change.name, got)
		}
	}
	srv.refreshExtensionsForTest = func(config.Config) error { calls++; return nil }
	srv.pluginRefreshFailure.retryAt = time.Time{}
	if err := srv.refreshPluginGenerationIfChanged(); err != nil {
		t.Fatal(err)
	}
	if !srv.pluginRefreshFailure.retryAt.IsZero() {
		t.Fatal("successful refresh retained the failed candidate's retry window")
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
