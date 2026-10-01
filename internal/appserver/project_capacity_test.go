//go:build project_agent

package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Protect new admission before user history, remote lease-release progress,
// coordination capacity, durable fencing, and no model work while queued.
func setProjectWorkerLimit(t *testing.T, s *Server, limit int) {
	t.Helper()
	data, err := os.ReadFile(s.rt.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte(`{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`)
	} else if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	a, _ := cfg["agent"].(map[string]any)
	if a == nil {
		a = map[string]any{}
	}
	a["project_max_parallel"] = limit
	cfg["agent"] = a
	data, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func capacityWorker(t *testing.T, s *Server, lead, id, role string) *threadState {
	t.Helper()
	metadata, found, err := session.Find(s.rt.SessionDir, lead)
	if err != nil || !found {
		t.Fatalf("lead: %v", err)
	}
	metadata.ID = id
	metadata.Source = projectSessionSource
	metadata.ParentID = lead
	metadata.ProjectRole = role
	metadata.Title = id
	if _, err = session.CreateInitialized(s.rt.SessionDir, metadata, nil); err != nil {
		t.Fatal(err)
	}
	th, err := s.ensureThreadLoaded(id)
	if err != nil {
		t.Fatal(err)
	}
	return th
}
func queueCapacityInput(t *testing.T, s *Server, id, client string) {
	t.Helper()
	if err := session.EnqueueInbox(s.rt.SessionDir, session.InboxMessage{SessionID: id, ClientID: client, Content: client, Wake: true, Cause: "capacity-test"}); err != nil {
		t.Fatal(err)
	}
	s.drainSessionInbox(id)
}

func TestProjectCapacityRemoteMutationReleaseProgress(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Admission")
	a := capacityWorker(t, srv, lead.ID, "capacity-a", "worker")
	b := capacityWorker(t, srv, lead.ID, "capacity-b", "")
	lease, ok, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, a.ID)
	if err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	defer lease.Release()
	queueCapacityInput(t, srv, b.ID, "capacity-dispatch")
	pending, err := session.PendingInbox(rt.SessionDir, b.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	calls.assertIdle(t)
	if ids := deliveredClientIDs(t, rt, b.ID, "capacity-dispatch"); len(ids) != 0 {
		t.Fatalf("premature input: %v", ids)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	calls.next(t, "capacity-dispatch").response <- providersResponse("done")
	waitForThread(t, srv, b.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != "" })
	if ids := deliveredClientIDs(t, rt, b.ID, "capacity-dispatch"); len(ids) != 1 {
		t.Fatalf("deliveries: %v", ids)
	}
}

func TestProjectCapacityManualAdmissionAndCoordination(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Admission")
	a := capacityWorker(t, srv, lead.ID, "capacity-a", "worker")
	b := capacityWorker(t, srv, lead.ID, "capacity-b", "worker")
	side := capacityWorker(t, srv, lead.ID, "capacity-side", "side")
	queueCapacityInput(t, srv, a.ID, "occupy-capacity")
	held := calls.next(t, "occupy-capacity")
	_, _, err := srv.startThreadUserTurn(context.Background(), b, providers.ChatMessage{Role: "user", Content: "manual blocked", ClientID: "manual-blocked"}, turnRuntimeSnapshot{}, true, turnReadOnlyFail)
	if !errors.Is(err, session.ErrProjectWorkerCapacity) {
		t.Fatalf("manual err=%v", err)
	}
	if ids := deliveredClientIDs(t, rt, b.ID, "manual-blocked"); len(ids) != 0 {
		t.Fatalf("manual input appended: %v", ids)
	}
	queueCapacityInput(t, srv, side.ID, "side-coordination")
	calls.next(t, "side-coordination").response <- providersResponse("side done")
	held.response <- providersResponse("worker done")
	waitForThread(t, srv, a.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
}

func TestProjectInboxRetryStopsWithOnlyInformationalInput(t *testing.T) {
	srv, client, _, rt := newProjectFixture(t)
	lead := startProject(t, client, "Informational")
	target := capacityWorker(t, srv, lead.ID, "info-only", "worker")
	if err := session.EnqueueInbox(rt.SessionDir, session.InboxMessage{SessionID: target.ID, ClientID: "info", Content: "info", Wake: false, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(target.ID)
	srv.projectInboxMu.Lock()
	n := len(srv.projectInboxDrains)
	srv.projectInboxMu.Unlock()
	if n != 0 {
		t.Fatalf("informational inbox retained %d retries", n)
	}
}

func TestProjectInboxCoalescesRetriesAndClose(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Retry lifecycle")
	a := capacityWorker(t, srv, lead.ID, "retry-holder", "worker")
	b := capacityWorker(t, srv, lead.ID, "retry-pending", "worker")
	lease, ok, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, a.ID)
	if err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	defer lease.Release()
	type timer struct {
		delay   time.Duration
		fire    func()
		stopped *atomic.Bool
	}
	timers := make(chan timer, 10)
	srv.projectInboxAfterFunc = func(d time.Duration, f func()) func() {
		stopped := &atomic.Bool{}
		timers <- timer{d, f, stopped}
		return func() { stopped.Store(true) }
	}
	queueCapacityInput(t, srv, b.ID, "retry-pending-input")
	first := <-timers
	if first.delay != threadExecutionLeaseRetryDelay {
		t.Fatalf("initial timer=%v", first.delay)
	}
	for i := 0; i < 20; i++ {
		srv.drainSessionInbox(b.ID)
	}
	select {
	case extra := <-timers:
		t.Fatalf("duplicate timer: %v", extra.delay)
	default:
	}
	first.fire()
	var next timer
	select {
	case next = <-timers:
	case <-time.After(gatedProviderTimeout):
		t.Fatal("retry did not rearm")
	}
	if next.delay <= first.delay || next.delay > projectInboxMaxRetryDelay {
		t.Fatalf("backoff=%v", next.delay)
	}
	calls.assertIdle(t)
	srv.Close()
	if !next.stopped.Load() {
		t.Fatal("Close did not stop pending timer")
	}
	next.fire()
	select {
	case extra := <-timers:
		t.Fatalf("closed scheduler rearmed: %v", extra.delay)
	default:
	}
	srv.projectInboxMu.Lock()
	n := len(srv.projectInboxDrains)
	srv.projectInboxMu.Unlock()
	if n != 0 {
		t.Fatalf("Close retained %d drains", n)
	}
}

func TestProjectCapacityQueuedTurnUsesProjectRetry(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Queued capacity")
	a := capacityWorker(t, srv, lead.ID, "queued-holder", "worker")
	b := capacityWorker(t, srv, lead.ID, "queued-target", "worker")
	lease, ok, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, a.ID)
	if err != nil || !ok {
		t.Fatalf("lease %v %v", ok, err)
	}
	defer lease.Release()
	if !srv.enqueueQueuedUserTurn(b.ID, queuedTurn{id: "queued-capacity", msg: providers.ChatMessage{Role: "user", Content: "queued-capacity", ClientID: "queued-capacity"}}) {
		t.Fatal("enqueue")
	}
	srv.tryDrainQueuedTurns(b.ID, true)
	if !srv.hasQueuedUserTurns(b.ID) {
		t.Fatal("capacity discarded queue")
	}
	srv.projectInboxMu.Lock()
	n := len(srv.projectCapacityRetries[lead.ID])
	srv.projectInboxMu.Unlock()
	if n != 1 {
		t.Fatalf("capacity retries=%d", n)
	}
	calls.assertIdle(t)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	calls.next(t, "queued-capacity").response <- providersResponse("done")
	waitForThread(t, srv, b.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != "" })
	if ids := deliveredClientIDs(t, rt, b.ID, "queued-capacity"); len(ids) != 1 {
		t.Fatalf("deliveries=%v", ids)
	}
}

func TestProjectCapacityPrelaunchHookRollback(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Prelaunch capacity")
	a := capacityWorker(t, srv, lead.ID, "hook-holder", "worker")
	b := capacityWorker(t, srv, lead.ID, "hook-pending", "worker")
	entered, released := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-released:
			_, _ = w.Write([]byte("2"))
		case <-r.Context().Done():
		}
	}))
	defer gate.Close()
	defer release()
	rt.HookDispatcher.Replace(hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.UserPromptSubmit: {{Command: fmt.Sprintf(`node -e "fetch('%s').then(r=>r.text()).then(t=>process.exit(Number(t)))"`, gate.URL)}},
	})))
	rejected := make(chan *ResponseError, 1)
	go func() {
		rejected <- client.call(t, MethodTurnStart, TurnStartParams{ThreadID: a.ID, Prompt: "rejected reservation"}, nil)
	}()
	select {
	case <-entered:
	case <-time.After(gatedProviderTimeout):
		t.Fatal("hook did not reserve slot")
	}
	queueCapacityInput(t, srv, b.ID, "after-hook-rollback")
	calls.assertIdle(t)
	if ids := deliveredClientIDs(t, rt, b.ID, "after-hook-rollback"); len(ids) != 0 {
		t.Fatal("pending worker appended early")
	}
	rt.HookDispatcher.Replace(hooks.NewDispatcher(nil))
	release()
	if failure := <-rejected; failure == nil {
		t.Fatal("hook rejection not reported")
	}
	history, err := loadChatMessages(rt.SessionDir, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range history {
		if m.Role == "user" {
			t.Fatal("rejected hook appended user history")
		}
	}
	calls.next(t, "after-hook-rollback").response <- providersResponse("done")
	waitForThread(t, srv, b.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != "" })
}

func TestProjectCapacityReductionPreservesRunningWorkers(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 2)
	lead := startProject(t, client, "Reduced capacity")
	a := capacityWorker(t, srv, lead.ID, "reduce-a", "worker")
	b := capacityWorker(t, srv, lead.ID, "reduce-b", "worker")
	c := capacityWorker(t, srv, lead.ID, "reduce-c", "worker")
	queueCapacityInput(t, srv, a.ID, "first-running")
	first := calls.next(t, "first-running")
	queueCapacityInput(t, srv, b.ID, "second-running")
	second := calls.next(t, "second-running")
	setProjectWorkerLimit(t, srv, 1)
	queueCapacityInput(t, srv, c.ID, "after-reduction")
	if !threadIsRunning(a) || !threadIsRunning(b) {
		t.Fatal("limit decrease canceled existing work")
	}
	calls.assertIdle(t)
	first.response <- providersResponse("first done")
	waitForThread(t, srv, a.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	// At the lowered bound one surviving worker still occupies all capacity.
	if _, ok, err := session.TryAcquireThreadExecutionLeaseWithProjectLimit(rt.SessionDir, c.ID, 1); ok || !errors.Is(err, session.ErrProjectWorkerCapacity) {
		t.Fatalf("decreased bound not applied: %v %v", ok, err)
	}
	second.response <- providersResponse("second done")
	calls.next(t, "after-reduction").response <- providersResponse("third done")
	waitForThread(t, srv, c.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != "" })
}

func TestProjectCapacityBacklogReplayStopAndFailure(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Backlog")
	holder := capacityWorker(t, srv, lead.ID, "backlog-holder", "worker")
	lease, ok, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, holder.ID)
	if err != nil || !ok {
		t.Fatalf("holder: %v %v", ok, err)
	}
	defer lease.Release()
	peer := New(rt, &lockedBuffer{})
	t.Cleanup(peer.Close)
	var ids []string
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("backlog-%d", i)
		ids = append(ids, id)
		capacityWorker(t, srv, lead.ID, id, "worker")
		queueCapacityInput(t, srv, id, id+"-input")
		// A second Server receives the identical dispatch and races recovery.
		queueCapacityInput(t, peer, id, id+"-input")
	}
	stopped := capacityWorker(t, srv, lead.ID, "backlog-stopped", "worker")
	control, err := session.ChangeControl(rt.SessionDir, stopped.ID, lead.ID, session.ControlActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.EnqueueInbox(rt.SessionDir, session.InboxMessage{SessionID: stopped.ID, ClientID: "stopped-input", Content: "never run", Wake: true, Controls: []session.Control{control}}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(stopped.ID)
	if _, err := session.ChangeControl(rt.SessionDir, stopped.ID, lead.ID, session.ControlPaused, control.Revision); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(stopped.ID)
	if messages, err := session.PendingInbox(rt.SessionDir, stopped.ID); err != nil || len(messages) != 0 {
		t.Fatalf("stale work not settled: %v %v", messages, err)
	}
	calls.assertIdle(t)
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for i := 0; i < len(ids); i++ {
		call := calls.provider.next(t)
		message := lastUserRequestMessage(call.request)
		id := message.Content
		if seen[id] {
			t.Fatalf("duplicate provider input: %s", id)
		}
		seen[id] = true
		if i == 0 {
			call.failure <- errors.New("permanent provider rejection")
		} else {
			call.response <- providersResponse("done")
		}
	}
	for _, id := range ids {
		// Any host may own the admitted turn; durable terminal evidence is shared.
		deadline := time.Now().Add(gatedProviderTimeout)
		for {
			active, err := session.ThreadExecutionActive(rt.SessionDir, id)
			if err != nil {
				t.Fatal(err)
			}
			if !active {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("backlog did not finish")
			}
			time.Sleep(time.Millisecond)
		}
		if delivered := deliveredClientIDs(t, rt, id, id+"-input"); len(delivered) != 1 {
			t.Fatalf("%s deliveries=%v", id, delivered)
		}
	}
	srv.recoverProjectInbox()
	peer.recoverProjectInbox()
	calls.assertIdle(t)
	if delivered := deliveredClientIDs(t, rt, stopped.ID, "stopped-input"); len(delivered) != 0 {
		t.Fatal("stopped backlog ran")
	}
}

func TestProjectCapacityAdoptsAlreadyRunningConversation(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Adoption capacity")
	a := capacityWorker(t, srv, lead.ID, "adopt-running-a", "worker")
	b := capacityWorker(t, srv, lead.ID, "adopt-running-b", "worker")
	c := capacityWorker(t, srv, lead.ID, "adopt-running-c", "worker")
	if _, err := session.SetProjectMembership(rt.SessionDir, b.ID, "", "", ""); err != nil {
		t.Fatal(err)
	}
	queueCapacityInput(t, srv, a.ID, "already-managed")
	first := calls.next(t, "already-managed")
	queueCapacityInput(t, srv, b.ID, "ordinary-running")
	ordinary := calls.next(t, "ordinary-running")
	if _, err := session.SetProjectMembership(rt.SessionDir, b.ID, projectSessionSource, lead.ID, ""); err != nil {
		t.Fatal(err)
	}
	queueCapacityInput(t, srv, c.ID, "after-running-adoption")
	if !threadIsRunning(a) || !threadIsRunning(b) {
		t.Fatal("adoption canceled existing work")
	}
	calls.assertIdle(t)
	first.response <- providersResponse("first done")
	waitForThread(t, srv, a.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle })
	if _, ok, err := session.TryAcquireThreadExecutionLeaseWithProjectLimit(rt.SessionDir, c.ID, 1); ok || !errors.Is(err, session.ErrProjectWorkerCapacity) {
		t.Fatalf("adopted execution not counted: %v %v", ok, err)
	}
	ordinary.response <- providersResponse("ordinary done")
	calls.next(t, "after-running-adoption").response <- providersResponse("last done")
	waitForThread(t, srv, c.ID, func(th Thread) bool { return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != "" })
}
