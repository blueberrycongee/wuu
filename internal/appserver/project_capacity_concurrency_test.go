//go:build project_agent

package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The pending worker must restart after a different owner releases a coarse
// lease without a local completion callback, and input must not append early.
func TestProjectCapacityRemoteReleaseResumesInbox(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Independent remote capacity")
	var leases []*session.DocumentThreadExecutionLease
	defer func() {
		for _, l := range leases {
			l.Release()
		}
	}()
	for i := 0; i < config.DefaultAgentMaxParallel; i++ {
		id := fmt.Sprintf("independent-held-%d", i)
		_, err := rt.Sessions.CreateInitialized(session.Session{ID: id, Source: projectSessionSource, ParentID: lead.ID, ProjectRole: "worker", CWD: rt.RootDir, WorkspaceID: rt.WorkspaceID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		l, ok, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, id)
		if err != nil || !ok {
			t.Fatalf("hold: %v %v", ok, err)
		}
		leases = append(leases, l)
	}
	request := tools.ProjectSessionRequest{Action: "create", Prompt: "Independent deferred brief", Workspace: "shared"}
	value, err := srv.projectSessionHandler(lead.ID)(context.Background(), "independent-deferred", request)
	if err != nil {
		t.Fatal(err)
	}
	worker := value.(projectSessionView)
	if worker.State != "queued" {
		t.Fatalf("full pool dispatch = %+v", worker)
	}
	if ids := deliveredClientIDs(t, rt, worker.SessionID, "project:"); len(ids) != 0 {
		t.Fatalf("input appended before capacity: %v", ids)
	}
	calls.assertIdle(t)
	if err := leases[0].Release(); err != nil {
		t.Fatal(err)
	}
	calls.next(t, worker.SessionID, request.Prompt).response <- providersResponse("Independent deferred done")
	waitForThread(t, srv, worker.SessionID, func(th Thread) bool { return th.Status == ThreadStatusIdle && th.LatestCompletedTurnID != "" })
	if ids := deliveredClientIDs(t, rt, worker.SessionID, "project:"); len(ids) != 1 {
		t.Fatalf("deferred delivery count: %v", ids)
	}
}

func TestProjectCapacityUsesDurableMembership(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Independent durable membership")
	var started ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{}, &started)
	// The local cache remains an ordinary conversation throughout adoption.
	th := srv.thread(started.Thread.ID)
	if th.Source != "" {
		t.Fatalf("expected ordinary cached source, got %q", th.Source)
	}
	for i := 0; i < config.DefaultAgentMaxParallel; i++ {
		id := fmt.Sprintf("membership-held-%d", i)
		_, err := rt.Sessions.CreateInitialized(session.Session{ID: id, Source: projectSessionSource, ParentID: lead.ID, ProjectRole: "worker"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		l, ok, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, id)
		if err != nil || !ok {
			t.Fatalf("hold: %v %v", ok, err)
		}
		defer l.Release()
	}
	if _, err := rt.Sessions.SetProjectMembership(th.ID, projectSessionSource, lead.ID, ""); err != nil {
		t.Fatal(err)
	}
	failure := client.call(t, MethodTurnStart, TurnStartParams{ThreadID: th.ID, Prompt: "Must wait for durable capacity"}, nil)
	if failure == nil {
		t.Fatal("stale ordinary cache bypassed project capacity")
	}
	if !strings.Contains(failure.Message, session.ErrProjectWorkerCapacity.Error()) {
		t.Fatalf("unrelated admission rejection: %+v", failure)
	}
	calls.assertIdle(t)
	history, err := loadChatMessages(rt.Sessions, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if message.Role == "user" {
			t.Fatalf("rejected turn appended user input: %+v", message)
		}
	}
}

func TestProjectCapacityCrossServerLastSlot(t *testing.T) {
	owner, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Independent cross-server atomicity")
	for i := 0; i < config.DefaultAgentMaxParallel-1; i++ {
		id := fmt.Sprintf("cross-server-held-%d", i)
		_, err := rt.Sessions.CreateInitialized(session.Session{ID: id, Source: projectSessionSource, ParentID: lead.ID, ProjectRole: "worker"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		l, ok, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, id)
		if err != nil || !ok {
			t.Fatalf("hold: %v %v", ok, err)
		}
		defer l.Release()
	}
	peerOut := &lockedBuffer{}
	peer := newProjectProviderTestServer(t, rt, peerOut)
	t.Cleanup(peer.Close)
	peerClient := &rpcClient{server: peer, out: peerOut}
	ids := []string{}
	for _, c := range []*rpcClient{client, peerClient} {
		var result ThreadStartResult
		c.rpc(t, MethodThreadStart, ThreadStartParams{}, &result)
		ids = append(ids, result.Thread.ID)
		if _, err := rt.Sessions.SetProjectMembership(result.Thread.ID, projectSessionSource, lead.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	type admission struct {
		index   int
		failure *ResponseError
	}
	gate := make(chan struct{})
	results := make(chan admission, 2)
	for index, c := range []*rpcClient{client, peerClient} {
		go func(i int, c *rpcClient) {
			<-gate
			failure := c.call(t, MethodTurnStart, TurnStartParams{ThreadID: ids[i], Prompt: "Independent atomic last slot"}, nil)
			results <- admission{i, failure}
		}(index, c)
	}
	close(gate)
	successes := 0
	admittedID := ""
	for range 2 {
		r := <-results
		if r.failure == nil {
			successes++
			admittedID = ids[r.index]
		} else {
			if !strings.Contains(r.failure.Message, session.ErrProjectWorkerCapacity.Error()) {
				t.Fatalf("unrelated cross-server rejection: %+v", r.failure)
			}
			history, err := loadChatMessages(rt.Sessions, ids[r.index])
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range history {
				if m.Role == "user" {
					t.Fatal("rejected cross-server admission appended user input")
				}
			}
		}
	}
	if successes != 1 {
		t.Fatalf("last slot admitted %d cross-server contenders", successes)
	}
	calls.next(t, admittedID, "Independent atomic last slot").response <- providersResponse("Independent atomic done")
	_ = owner
}

func TestProjectInboxForeignWorkspaceDoesNotPoll(t *testing.T) {
	srv, _, calls, rt := newProjectFixture(t)
	foreign := "independent-foreign-project"
	_, err := rt.Sessions.CreateInitialized(session.Session{ID: foreign, Source: projectSource, CWD: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.EnqueueInbox(session.InboxMessage{SessionID: foreign, ClientID: "foreign-pending", Content: "foreign work", Wake: true}); err != nil {
		t.Fatal(err)
	}
	srv.drainSessionInbox(foreign)
	// Startup recovery can begin another scan after the explicit drain returns.
	// Join only that in-flight pass; a retained retry timer must still fail.
	lifetime := projectFixtureProvider(srv).lifetime
	lifetime.projectInboxMu.Lock()
	var passDone <-chan struct{}
	if pass := lifetime.projectInboxDrains[foreign]; pass != nil && pass.running {
		passDone = pass.done
	}
	n := len(lifetime.projectInboxDrains)
	lifetime.projectInboxMu.Unlock()
	if passDone != nil {
		select {
		case <-passDone:
		case <-time.After(gatedProviderTimeout):
			t.Fatal("foreign workspace recovery pass did not finish")
		}
		lifetime.projectInboxMu.Lock()
		n = len(lifetime.projectInboxDrains)
		lifetime.projectInboxMu.Unlock()
	}
	calls.assertIdle(t)
	if n != 0 {
		t.Fatalf("ineligible foreign workspace retained %d project retry loops", n)
	}
	pending, err := rt.Sessions.PendingInbox(foreign)
	if err != nil || len(pending) != 1 {
		t.Fatalf("foreign work must remain pending for its owner: %v %v", pending, err)
	}
}

func TestProjectCapacityPrelaunchRollback(t *testing.T) {
	srv, client, calls, rt := newProjectFixture(t)
	lead := startProject(t, client, "Independent prelaunch reservation")
	var threads []*threadState
	for i := 0; i < config.DefaultAgentMaxParallel+1; i++ {
		id := fmt.Sprintf("prelaunch-%d", i)
		_, err := rt.Sessions.CreateInitialized(session.Session{ID: id, Source: projectSessionSource, ParentID: lead.ID, ProjectRole: "worker", CWD: rt.RootDir, WorkspaceID: rt.WorkspaceID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		th, err := srv.ensureThreadLoaded(testRuntimeState(t, srv.rt), id)
		if err != nil {
			t.Fatal(err)
		}
		threads = append(threads, th)
		if i < config.DefaultAgentMaxParallel-1 {
			l, ok, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, id)
			if err != nil || !ok {
				t.Fatalf("hold: %v %v", ok, err)
			}
			defer l.Release()
		}
	}
	first, second := threads[len(threads)-2], threads[len(threads)-1]
	entered, release := make(chan struct{}), make(chan struct{})
	rejected := errors.New("independent prelaunch rejection")
	done := make(chan error, 1)
	go func() {
		_, _, err := srv.startThreadUserTurnWithAdmission(context.Background(), first, providers.ChatMessage{Role: "user", Content: "reservation input", ClientID: "reservation-input"}, turnRuntimeSnapshot{}, true, turnReadOnlyFail, turnAdmissionHooks{afterLease: func(*threadState, *providers.ChatMessage) error { close(entered); <-release; return rejected }})
		done <- err
	}()
	<-entered
	_, _, err := srv.startThreadUserTurn(context.Background(), second, providers.ChatMessage{Role: "user", Content: "contending input"}, turnRuntimeSnapshot{}, true, turnReadOnlyFail)
	close(release)
	if !errors.Is(err, session.ErrProjectWorkerCapacity) {
		t.Fatalf("prelaunch reservation bypassed: %v", err)
	}
	if err := <-done; !errors.Is(err, rejected) {
		t.Fatalf("first rejection: %v", err)
	}
	if ids := deliveredClientIDs(t, rt, first.ID, "reservation-input"); len(ids) != 0 {
		t.Fatalf("rejected prelaunch appended: %v", ids)
	}
	_, _, err = srv.startThreadUserTurnWithAdmission(context.Background(), second, providers.ChatMessage{Role: "user", Content: "retry after rollback"}, turnRuntimeSnapshot{}, true, turnReadOnlyFail, turnAdmissionHooks{afterLease: func(*threadState, *providers.ChatMessage) error { return rejected }})
	if !errors.Is(err, rejected) {
		t.Fatalf("rollback failed to restore capacity: %v", err)
	}
	calls.assertIdle(t)
}

func TestProjectInboxCloseDuringTimerLaunch(t *testing.T) {
	srv, client, _, rt := newProjectFixture(t)
	setProjectWorkerLimit(t, srv, 1)
	lead := startProject(t, client, "Independent timer shutdown")
	holder := capacityWorker(t, srv, lead.ID, "close-race-holder", "worker")
	pending := capacityWorker(t, srv, lead.ID, "close-race-pending", "worker")
	lease, ok, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, holder.ID)
	if err != nil || !ok {
		t.Fatalf("holder: %v %v", ok, err)
	}
	defer lease.Release()
	var fire func()
	projectFixtureProvider(srv).lifetime.projectInboxAfterFunc = func(_ time.Duration, callback func()) func() { fire = callback; return func() {} }
	queueCapacityInput(t, srv, pending.ID, "close-race-input")
	if fire == nil {
		t.Fatal("missing capacity timer")
	}
	// Block the provider's background registration, leaving the timer free to
	// publish its running pass and Server.Close free to close admission.
	lifetime := projectFixtureProvider(srv).lifetime
	lifetime.backgroundMu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			lifetime.backgroundMu.Unlock()
		}
	}()
	fired := make(chan struct{})
	go func() { fire(); close(fired) }()
	waitFor := func(ready func() bool) {
		t.Helper()
		deadline := time.After(gatedProviderTimeout)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			if ready() {
				return
			}
			select {
			case <-deadline:
				t.Fatal("shutdown interleave did not reach gate")
			case <-tick.C:
			}
		}
	}
	var passDone <-chan struct{}
	waitFor(func() bool {
		projectFixtureProvider(srv).lifetime.projectInboxMu.Lock()
		defer projectFixtureProvider(srv).lifetime.projectInboxMu.Unlock()
		state := projectFixtureProvider(srv).lifetime.projectInboxDrains[lead.ID]
		if state != nil && state.running {
			passDone = state.done
			return true
		}
		return false
	})
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	waitFor(func() bool { return srv.closed.Load() })
	lifetime.backgroundMu.Unlock()
	unlocked = true
	select {
	case <-fired:
	case <-time.After(gatedProviderTimeout):
		t.Fatal("timer launch did not return after Close")
	}
	select {
	case <-closed:
	case <-time.After(gatedProviderTimeout):
		t.Fatal("Close stalled behind rejected timer launch")
	}
	select {
	case <-passDone:
	default:
		t.Fatal("rejected timer launch stranded synchronous inbox waiters")
	}
}
