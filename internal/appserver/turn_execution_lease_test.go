package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

type terminalBlockingWriter struct {
	out     lockedBuffer
	started chan struct{}
	release chan struct{}
	once    sync.Once
	relOnce sync.Once
}

func newTerminalBlockingWriter() *terminalBlockingWriter {
	return &terminalBlockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *terminalBlockingWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"method":"turn/completed"`)) || bytes.Contains(p, []byte(`"method":"turn/error"`)) {
		w.once.Do(func() { close(w.started) })
		<-w.release
	}
	return w.out.Write(p)
}

func (w *terminalBlockingWriter) String() string {
	return w.out.String()
}

func (w *terminalBlockingWriter) unblock() {
	w.relOnce.Do(func() { close(w.release) })
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestThreadRuntimeGenerationInvalidatedByRuntimeRecovery(t *testing.T) {
	srv := &Server{}
	srv.pluginGenerationEpoch.Store(7)
	srv.pluginRuntimeRevision.Store(3)
	th := &threadState{runtimePluginEpoch: 7, runtimePluginRevision: 3}
	if !srv.threadRuntimePluginGenerationMatches(th) {
		t.Fatal("fresh thread runtime did not match the active plugin generation")
	}
	srv.pluginRuntimeRevision.Add(1)
	if srv.threadRuntimePluginGenerationMatches(th) {
		t.Fatal("thread runtime retained a retired plugin generation after recovery")
	}
}

func TestServerThreadExecutionLeaseReleasesBeforeTerminalNotification(t *testing.T) {
	holderClient := &fakeClient{response: providers.ChatResponse{Content: "holder finished"}}
	holderRuntime := newTestRuntime(t, holderClient)
	holderOut := newTerminalBlockingWriter()
	holder := New(holderRuntime, holderOut)
	t.Cleanup(holder.Close)
	t.Cleanup(holderOut.unblock)

	if err := holder.handleLine(context.Background(), []byte(`{"id":"thread","method":"thread/start"}`)); err != nil {
		t.Fatalf("create holder thread: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, holderOut.String()), "thread")["result"]).Thread.ID

	contenderClient := &fakeClient{response: providers.ChatResponse{Content: "contender finished"}}
	contenderRuntime := newTestRuntime(t, contenderClient)
	contenderRuntime.SessionDir = holderRuntime.SessionDir
	contenderOut := &lockedBuffer{}
	contender := New(contenderRuntime, contenderOut)
	t.Cleanup(contender.Close)
	resume := fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, threadID)
	if err := contender.handleLine(context.Background(), []byte(resume)); err != nil {
		t.Fatalf("resume contender thread: %v", err)
	}

	holderStart := fmt.Sprintf(`{"id":"holder-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"holder prompt"}}`, threadID)
	if err := holder.handleLine(context.Background(), []byte(holderStart)); err != nil {
		t.Fatalf("start holder turn: %v", err)
	}
	select {
	case <-holderOut.started:
	case <-time.After(2 * time.Second):
		t.Fatal("holder did not reach its terminal notification")
	}

	contenderStart := fmt.Sprintf(`{"id":"contender-turn","method":"turn/start","params":{"thread_id":%q,"prompt":"contender prompt"}}`, threadID)
	if err := contender.handleLine(context.Background(), []byte(contenderStart)); err != nil {
		t.Fatalf("contender turn request: %v", err)
	}
	response := responseByID(t, parseOutput(t, contenderOut.String()), "contender-turn")
	if response["error"] != nil {
		t.Fatalf("contender was rejected after durable finalization: %+v", response)
	}
	waitForMethod(t, contenderOut, NotificationTurnCompleted)
	assertFakeClientRequestCount(t, contenderClient, 1)
	persisted, err := loadChatMessages(holderRuntime.SessionDir, threadID)
	if err != nil {
		t.Fatalf("load history after contender turn: %v", err)
	}
	var sawHolderResult bool
	for _, msg := range persisted {
		if msg.Role == "assistant" && msg.Content == "holder finished" {
			sawHolderResult = true
			break
		}
	}
	if !sawHolderResult {
		t.Fatalf("contender did not preserve the holder result: %+v", persisted)
	}

	holderOut.unblock()
	waitForMethod(t, &holderOut.out, NotificationTurnCompleted)

	contenderClient.mu.Lock()
	requests := append([]providers.ChatRequest(nil), contenderClient.requests...)
	contenderClient.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("contender provider requests = %d, want 1", len(requests))
	}
	var sawHolderInRequest bool
	for _, msg := range requests[0].Messages {
		if msg.Role == "assistant" && msg.Content == "holder finished" {
			sawHolderInRequest = true
			break
		}
	}
	if !sawHolderInRequest {
		t.Fatalf("contender did not refresh the holder result: %+v", requests[0].Messages)
	}
}

func TestServerAppliesCrossProcessThreadExecutionReset(t *testing.T) {
	client := newBlockingStreamClient("must not complete")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	})

	if err := srv.handleLine(context.Background(), []byte(`{"id":"thread","method":"thread/start"}`)); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "thread")["result"]).Thread.ID
	start := fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"block until reset"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(start)); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}

	requested, err := session.RequestThreadExecutionReset(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if !requested {
		t.Fatal("running turn was reported idle")
	}
	waitForMethod(t, out, NotificationTurnError)

	lease, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, threadID)
	if err != nil {
		t.Fatalf("acquire after reset: %v", err)
	}
	if !acquired || lease == nil {
		t.Fatal("reset did not release thread execution ownership")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release verification lease: %v", err)
	}
}

func TestServerThreadExecutionLeaseGuardsUserAndCompactTurnEntrypoints(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "must not run"}}
	rt := newTestRuntime(t, client)
	sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-entrypoints", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(sess.ID)
	if err != nil {
		t.Fatalf("load thread: %v", err)
	}

	external, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("acquire external lease: %v", err)
	}
	if !acquired || external == nil {
		t.Fatal("expected external lease acquisition")
	}
	defer external.Release()

	if _, started, err := srv.startThreadUserTurn(
		context.Background(),
		th,
		providers.ChatMessage{Role: "user", Content: "blocked user turn"},
		turnRuntimeSnapshot{},
		true,
		turnReadOnlyIgnore,
	); err == nil || started || !strings.Contains(err.Error(), "another app-server") {
		t.Fatalf("user turn started=%t err=%v, want cross-server busy", started, err)
	}

	compactReq := Request{ID: json.RawMessage(`"compact"`), Method: MethodThreadCompactStart}
	if err := srv.startThreadCompactTurn(context.Background(), compactReq, th, ""); err != nil {
		t.Fatalf("compact handler transport error: %v", err)
	}
	compactResponse := responseByID(t, parseOutput(t, out.String()), "compact")
	if compactResponse["error"] == nil || !strings.Contains(fmt.Sprint(compactResponse["error"]), "another app-server") {
		t.Fatalf("expected compact cross-server busy error, got %+v", compactResponse)
	}

	assertFakeClientRequestCount(t, client, 0)
	records, err := session.LoadHistoryRecords(rt.SessionDir, sess.ID, true)
	if err != nil {
		t.Fatalf("load blocked-entrypoint history: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("blocked entrypoints mutated durable history: %+v", records)
	}
}

func TestServerQueuedTurnWaitsForExternalThreadExecutionLease(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "queued turn finished"}}
	rt := newTestRuntime(t, client)
	sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-queued-turn", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(sess.ID)
	if err != nil {
		t.Fatalf("load thread: %v", err)
	}

	external, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, sess.ID)
	if err != nil {
		t.Fatalf("acquire external lease: %v", err)
	}
	if !acquired || external == nil {
		t.Fatal("expected external lease acquisition")
	}
	t.Cleanup(func() { _ = external.Release() })
	if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{
		{Role: "user", Content: "remote owner prompt"},
		{Role: "assistant", Content: "remote owner result"},
	}); err != nil {
		t.Fatalf("persist remote owner result: %v", err)
	}

	srv.enqueueQueuedUserTurn(sess.ID, queuedTurn{
		id:  "queued-behind-owner",
		msg: providers.ChatMessage{Role: "user", Content: "run after ownership is free"},
	})
	srv.kickQueuedTurnDrain(sess.ID)
	waitForQueuedTurnRequeue(t, srv, sess.ID)
	th.mu.Lock()
	blockedRuntime := th.execRuntime
	th.mu.Unlock()
	if blockedRuntime != nil {
		t.Fatal("queued turn constructed a runtime before acquiring execution ownership")
	}
	assertFakeClientRequestCount(t, client, 0)

	if err := external.Release(); err != nil {
		t.Fatalf("release external lease: %v", err)
	}
	waitForTurnCompletedForThread(t, out, sess.ID)
	assertFakeClientRequestCount(t, client, 1)
	if srv.hasQueuedUserTurns(sess.ID) {
		t.Fatal("queued turn remained pending after ownership became available")
	}
	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	var sawRemoteResult bool
	for _, msg := range requests[0].Messages {
		if msg.Role == "assistant" && msg.Content == "remote owner result" {
			sawRemoteResult = true
			break
		}
	}
	if !sawRemoteResult {
		t.Fatalf("queued turn ran from stale history: %+v", requests[0].Messages)
	}
}

func TestServerThreadExecutionLeaseRefreshDropsRemovedDurableTail(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "fresh result"}}
	rt := newTestRuntime(t, client)
	sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-shorter-history", rt.RootDir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	durable := []providers.ChatMessage{
		{Role: "user", Content: "kept prompt"},
		{Role: "assistant", Content: "kept result"},
		{Role: "user", Content: "removed prompt"},
		{Role: "assistant", Content: "removed result"},
	}
	if err := appendChatMessages(rt.SessionDir, sess.ID, durable); err != nil {
		t.Fatalf("persist initial history: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if _, err := srv.ensureThreadLoaded(sess.ID); err != nil {
		t.Fatalf("load stale thread: %v", err)
	}
	if err := rewriteChatHistory(rt.SessionDir, sess.ID, durable[:2]); err != nil {
		t.Fatalf("shorten durable history: %v", err)
	}

	start := fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"fresh prompt"}}`, sess.ID)
	if err := srv.handleLine(context.Background(), []byte(start)); err != nil {
		t.Fatalf("start turn after durable rewrite: %v", err)
	}
	if response := responseByID(t, parseOutput(t, out.String()), "turn"); response["error"] != nil {
		t.Fatalf("turn was rejected: %+v", response)
	}
	waitForMethod(t, out, NotificationTurnCompleted)

	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(requests))
	}
	for _, msg := range requests[0].Messages {
		if msg.Content == "removed prompt" || msg.Content == "removed result" {
			t.Fatalf("removed durable tail was resurrected: %+v", requests[0].Messages)
		}
	}
}

func TestServerDestructiveThreadMutationsRespectExternalExecutionLease(t *testing.T) {
	t.Run("edit history", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-edit-history", rt.RootDir)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		if err := appendChatMessages(rt.SessionDir, sess.ID, []providers.ChatMessage{
			{Role: "user", Content: "editable prompt"},
			{Role: "assistant", Content: "editable result"},
		}); err != nil {
			t.Fatalf("persist history: %v", err)
		}
		out := &lockedBuffer{}
		srv := New(rt, out)
		t.Cleanup(srv.Close)
		dispatchPayload(t, srv, "resume", MethodThreadResume, ThreadResumeParams{SessionID: sess.ID})
		resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, out.String()), "resume")["result"])
		if len(resumed.Thread.Turns) != 1 || len(resumed.Thread.Turns[0].Items) == 0 {
			t.Fatalf("unexpected resumed history: %+v", resumed.Thread.Turns)
		}

		external := acquireExternalThreadLease(t, rt.SessionDir, sess.ID)
		defer external.Release()
		dispatchPayload(t, srv, "edit", MethodThreadEditMessage, ThreadEditMessageParams{
			ThreadID: sess.ID,
			TurnID:   resumed.Thread.Turns[0].ID,
			ItemID:   resumed.Thread.Turns[0].Items[0].ID,
		})
		assertCrossServerBusyResponse(t, out, "edit")
		persisted, err := loadChatMessages(rt.SessionDir, sess.ID)
		if err != nil {
			t.Fatalf("load history after rejected edit: %v", err)
		}
		if len(persisted) != 2 {
			t.Fatalf("rejected edit changed durable history: %+v", persisted)
		}
	})

	t.Run("delete", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-delete", rt.RootDir)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		out := &lockedBuffer{}
		srv := New(rt, out)
		t.Cleanup(srv.Close)
		external := acquireExternalThreadLease(t, rt.SessionDir, sess.ID)
		defer external.Release()
		dispatchPayload(t, srv, "delete", MethodThreadDelete, ThreadDeleteParams{ThreadID: sess.ID})
		assertCrossServerBusyResponse(t, out, "delete")
		if _, ok, err := session.Find(rt.SessionDir, sess.ID); err != nil || !ok {
			t.Fatalf("rejected delete removed session: ok=%t err=%v", ok, err)
		}
	})

	t.Run("archive", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-archive", rt.RootDir)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		out := &lockedBuffer{}
		srv := New(rt, out)
		t.Cleanup(srv.Close)
		external := acquireExternalThreadLease(t, rt.SessionDir, sess.ID)
		defer external.Release()
		dispatchPayload(t, srv, "archive", MethodThreadArchive, ThreadArchiveParams{ThreadID: sess.ID, Archived: true})
		assertCrossServerBusyResponse(t, out, "archive")
		metadata, ok, err := session.Find(rt.SessionDir, sess.ID)
		if err != nil || !ok {
			t.Fatalf("load session after rejected archive: ok=%t err=%v", ok, err)
		}
		if metadata.ArchivedAt != nil {
			t.Fatalf("rejected archive changed metadata: %+v", metadata)
		}
	})
}

func TestServerThreadExecutionLeaseRollsBackPrelaunchFailures(t *testing.T) {
	t.Run("response write", func(t *testing.T) {
		client := &fakeClient{response: providers.ChatResponse{Content: "must not run"}}
		rt := newTestRuntime(t, client)
		sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-write-failure", rt.RootDir)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		writeErr := errors.New("transport unavailable")
		srv := New(rt, errorWriter{err: writeErr})
		t.Cleanup(srv.Close)
		th, err := srv.ensureThreadLoaded(sess.ID)
		if err != nil {
			t.Fatalf("load thread: %v", err)
		}
		req := Request{
			ID:     json.RawMessage(`"turn"`),
			Method: MethodTurnStart,
			Params: mustJSON(TurnStartParams{ThreadID: sess.ID, Prompt: "accepted before write failed"}),
		}
		if err := srv.handleTurnStart(context.Background(), req); !errors.Is(err, writeErr) {
			t.Fatalf("turn start error = %v, want transport error", err)
		}
		assertThreadStartRolledBack(t, th)
		assertThreadLeaseAvailable(t, rt.SessionDir, sess.ID)
		assertFakeClientRequestCount(t, client, 0)
		assertPersistedPrelaunchTurnFailed(t, srv, sess.ID, writeErr.Error())
	})

	t.Run("compact response write", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		sess, err := session.CreateWithMetadata(rt.SessionDir, "lease-compact-write-failure", rt.RootDir)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		writeErr := errors.New("transport unavailable")
		srv := New(rt, errorWriter{err: writeErr})
		t.Cleanup(srv.Close)
		th, err := srv.ensureThreadLoaded(sess.ID)
		if err != nil {
			t.Fatalf("load thread: %v", err)
		}
		req := Request{ID: json.RawMessage(`"compact"`), Method: MethodThreadCompactStart}
		if err := srv.startThreadCompactTurn(context.Background(), req, th, ""); !errors.Is(err, writeErr) {
			t.Fatalf("compact start error = %v, want transport error", err)
		}
		assertThreadStartRolledBack(t, th)
		assertThreadLeaseAvailable(t, rt.SessionDir, sess.ID)
	})

	t.Run("durable append", func(t *testing.T) {
		rt := newTestRuntime(t, &fakeClient{})
		srv := New(rt, io.Discard)
		t.Cleanup(srv.Close)
		threadID := "missing-durable-session"
		th := newThreadState(threadID, nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
		if _, started, err := srv.startThreadUserTurn(
			context.Background(),
			th,
			providers.ChatMessage{Role: "user", Content: "cannot persist"},
			turnRuntimeSnapshot{},
			true,
			turnReadOnlyIgnore,
		); err == nil || started {
			t.Fatalf("missing-session turn started=%t err=%v, want append failure", started, err)
		}
		assertThreadLeaseAvailable(t, rt.SessionDir, threadID)
	})
}

func assertPersistedPrelaunchTurnFailed(t *testing.T, srv *Server, threadID, message string) {
	t.Helper()
	records, _, err := loadProviderPersistedMessages(srv.rt.SessionDir, threadID, true)
	if err != nil {
		t.Fatalf("load persisted prelaunch turn: %v", err)
	}
	turns := turnsFromPersistedHistory(threadID, records, time.Now().UTC(), srv.resolveParticipantSummary)
	if len(turns) != 1 || turns[0].Status != TurnStatusFailed || turns[0].Error == nil || !strings.Contains(turns[0].Error.Message, message) {
		t.Fatalf("persisted prelaunch turn = %+v, want one failed turn containing %q", turns, message)
	}
	if len(turns[0].Items) != 2 || turns[0].Items[1].Type != ThreadItemError || turns[0].Items[1].Status != ThreadItemStatusFailed {
		t.Fatalf("persisted prelaunch error item = %+v", turns[0].Items)
	}
}

func assertThreadStartRolledBack(t *testing.T, th *threadState) {
	t.Helper()
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.running || th.currentTurn != "" || th.cancel != nil || th.executionLease != nil {
		t.Fatalf("prelaunch failure left execution active: running=%t turn=%q cancel=%t lease=%t", th.running, th.currentTurn, th.cancel != nil, th.executionLease != nil)
	}
	if len(th.Turns) == 0 || th.Turns[len(th.Turns)-1].Status != TurnStatusFailed {
		t.Fatalf("prelaunch failure did not terminalize the turn: %+v", th.Turns)
	}
}

func assertThreadLeaseAvailable(t *testing.T, sessDir, threadID string) {
	t.Helper()
	lease, acquired, err := session.TryAcquireThreadExecutionLease(sessDir, threadID)
	if err != nil {
		t.Fatalf("acquire released lease: %v", err)
	}
	if !acquired || lease == nil {
		t.Fatal("expected prelaunch failure to release the execution lease")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release verification lease: %v", err)
	}
}

func acquireExternalThreadLease(t *testing.T, sessDir, threadID string) *session.ThreadExecutionLease {
	t.Helper()
	lease, acquired, err := session.TryAcquireThreadExecutionLease(sessDir, threadID)
	if err != nil {
		t.Fatalf("acquire external execution lease: %v", err)
	}
	if !acquired || lease == nil {
		t.Fatal("expected external execution lease acquisition")
	}
	return lease
}

func assertCrossServerBusyResponse(t *testing.T, out *lockedBuffer, id string) {
	t.Helper()
	response := responseByID(t, parseOutput(t, out.String()), id)
	if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), "another app-server") {
		t.Fatalf("expected cross-server busy error, got %+v", response)
	}
}

func waitForThreadLeaseRelease(t *testing.T, sessDir, threadID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lease, acquired, err := session.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil {
			t.Fatalf("probe released lease: %v", err)
		}
		if acquired {
			if err := lease.Release(); err != nil {
				t.Fatalf("release probe lease: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for thread execution lease release")
}

func waitForQueuedTurnRequeue(t *testing.T, srv *Server, threadID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.queuedTurnMu.Lock()
		requeued := len(srv.pendingQueuedTurns[threadID]) > 0 && !srv.drainingQueuedTurns[threadID]
		srv.queuedTurnMu.Unlock()
		if requeued {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for queued turn to remain pending behind external execution owner")
}
