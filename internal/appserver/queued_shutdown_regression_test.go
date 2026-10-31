package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/defaultpolicy"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func seedRecoveryQueue(t *testing.T, rt *runtime.Session, id string) {
	t.Helper()
	now := time.Now().UTC()
	meta := agentthread.Metadata{ID: "worker_queued", TaskName: "queued", Role: agentcontrol.DefaultSubagentType, ParentID: "root", Path: "/root/worker_queued", CreatedAt: now, UpdatedAt: now, Status: agentthread.StatusPending, Source: agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentPath: agentthread.RootPath}}
	payload, err := json.Marshal(map[string]any{"worker_id": meta.ID, "worker_type": agentcontrol.DefaultSubagentType, "thread_meta": meta, "prompt": "finish the queued task", "isolation": "inplace"})
	if err != nil {
		t.Fatal(err)
	}
	store := harness.NewDocumentStore(rt.Documents, id, filepath.Join(statepath.SessionArtifactDir(rt.StateDir, id), "harness"))
	if err := store.UpsertQueueItem(harness.QueueItem{ID: meta.ID, TaskID: meta.ID, Kind: "agent_spawn", Payload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

// Once Close yields, the old cancellation and a successor must serialize on
// the worker lease; either may win, but the payload must never run twice.
func TestQueuedShutdownAndSuccessorSerializeOnWorkerLease(t *testing.T) {
	var calls atomic.Int32
	client := &fakeClient{response: providersResponse("finished"), onChat: func(_ int, _ providers.ChatRequest) { calls.Add(1) }}
	rt := newTestRuntime(t, client, defaultpolicy.NewService())
	rt.StateDir = t.TempDir()
	var err error
	rt.Toolkit, err = tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := rt.Sessions.CreateWithMetadata("shutdown-successor", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	seedRecoveryQueue(t, rt, sess.ID)
	first := New(rt, &lockedBuffer{})
	first.shutdownDrainTimeoutForTest = 50 * time.Millisecond
	t.Cleanup(first.Close)
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	th, err := first.ensureThreadLoaded(binding, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	})
	first.beforeRuntimeInstallForTest = func(thread *runtime.ThreadRuntime) {
		thread.AgentControl.SetQueuedManagerSpawnHookForTest(func(string) { close(entered); <-resume })
	}
	old, err := first.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("old launch did not reach barrier")
	}
	closed := make(chan struct{})
	go func() { first.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("old server did not close")
	}
	second := New(rt, &lockedBuffer{})
	t.Cleanup(func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
		second.Close()
	})
	next, err := second.ensureThreadLoaded(binding, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	next.mu.Lock()
	acquired, err := second.tryAcquireThreadExecutionLeaseLocked(next, 0)
	next.mu.Unlock()
	if err != nil || !acquired {
		t.Fatalf("successor admission: %v, %v", acquired, err)
	}
	if _, err := second.ensureThreadRuntime(next); err != nil {
		t.Fatal(err)
	}
	close(resume)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := old.AgentControl.WaitOwnedWorkerExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	store := harness.NewDocumentStore(rt.Documents, sess.ID, filepath.Join(statepath.SessionArtifactDir(rt.StateDir, sess.ID), "harness"))
	deadline := time.Now().Add(15 * time.Second)
	for {
		items, err := store.ListQueueItems()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not settle: %+v", items)
		}
		time.Sleep(10 * time.Millisecond)
	}
	next.mu.Lock()
	next.releaseThreadExecutionLeaseLocked()
	next.mu.Unlock()
	second.Close()
	if got := calls.Load(); got > 1 {
		t.Fatalf("recovered worker ran %d times", got)
	}
}

func TestShutdownRejectsQueuedWorkerPausedBeforeSpawn(t *testing.T) {
	modelStarted := make(chan struct{}, 1)
	client := &fakeClient{response: providersResponse("finished"), onChat: func(_ int, _ providers.ChatRequest) { modelStarted <- struct{}{} }}
	rt := newTestRuntime(t, client, defaultpolicy.NewService())
	rt.StateDir = t.TempDir()
	var err error
	rt.Toolkit, err = tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := rt.Sessions.CreateWithMetadata("shutdown-queued-worker", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	seedRecoveryQueue(t, rt, sess.ID)
	documents := rt.Documents
	srv := New(rt, &lockedBuffer{})
	srv.shutdownDrainTimeoutForTest = 50 * time.Millisecond
	t.Cleanup(srv.Close)
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	th, err := srv.ensureThreadLoaded(binding, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Close(); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	})
	srv.beforeRuntimeInstallForTest = func(thread *runtime.ThreadRuntime) {
		thread.AgentControl.SetQueuedManagerSpawnHookForTest(func(string) { close(entered); <-resume })
	}
	thread, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("worker did not reach spawn barrier")
	}
	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("bounded close blocked on queued worker")
	}
	if _, err := rt.Cleanup(); err != nil {
		t.Fatalf("owner cleanup after bounded close: %v", err)
	}
	if _, found, err := th.state.Sessions.Find(sess.ID); err != nil || !found {
		t.Fatalf("thread's retained state unavailable to deferred cancellation: found=%v, err=%v", found, err)
	}
	close(resume)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := thread.AgentControl.WaitOwnedWorkerExecutions(ctx); err != nil {
		t.Fatalf("worker lease not drained: %v", err)
	}
	select {
	case <-modelStarted:
		t.Fatal("worker entered model after shutdown")
	default:
	}
	store := harness.NewDocumentStore(documents, sess.ID, filepath.Join(statepath.SessionArtifactDir(rt.StateDir, sess.ID), "harness"))
	deadline := time.Now().Add(10 * time.Second)
	for {
		items, err := store.ListQueueItems()
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shutdown failed to cancel queued work: %+v", items)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
