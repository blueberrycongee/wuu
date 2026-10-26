package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/execution"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/stretchr/testify/require"
)

func TestForceArchiveSettlesRetainedExecutionAfterAuthorityReplacement(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		name := "same_conversation"
		if unrelated {
			name = "unrelated_same_id"
		}
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			const id = "archive-retained-owner"
			const turnID = "archive-turn"
			_, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir)
			require.NoError(t, err)
			_, err = appendChatMessage(rt.Sessions, id, providers.ChatMessage{Role: "user", Content: "work", ClientID: turnID})
			require.NoError(t, err)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			binding := testRuntimeState(t, rt)
			th, err := srv.ensureThreadLoaded(binding, id)
			require.NoError(t, err)
			journal, err := binding.Sessions.NewInferenceJournalRuntime("archive-fixture")
			require.NoError(t, err)
			t.Cleanup(func() { srv.Close(); _ = journal.Close() })
			store, err := execution.NewStore(binding.Documents)
			require.NoError(t, err)
			run, err := store.Create(context.Background(), execution.CreateParams{RuntimeID: journal.RuntimeID(), ThreadID: id, Request: execution.Request{Mode: execution.ModeStart}, Runtime: execution.RuntimeManifest{Resolved: execution.Selection{Provider: "test", Model: "test"}, ProtocolVersion: ProtocolVersion}, Workspace: execution.WorkspaceRef{Root: rt.RootDir}})
			require.NoError(t, err)
			run, err = store.AttachTurn(context.Background(), run.ID, id, turnID, time.Now().UTC())
			require.NoError(t, err)
			srv.registerExecutionRun(binding, store, run, nil)
			var cancellations atomic.Int32
			th.mu.Lock()
			acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
			if err == nil && acquired {
				th.startTurnLocked(turnID, providers.ChatMessage{}, time.Now().UTC())
				th.currentExecutionRunID = run.ID
				th.cancel = func() { cancellations.Add(1) }
			}
			th.mu.Unlock()
			require.NoError(t, err)
			require.True(t, acquired)
			t.Cleanup(func() {
				th.mu.Lock()
				th.releaseThreadExecutionLeaseLocked()
				th.running = false
				th.currentTurn = ""
				th.cancel = nil
				th.mu.Unlock()
			})

			successor := newTestRuntime(t, &fakeClient{})
			successor.RootDir, successor.SessionDir = rt.RootDir, rt.SessionDir
			successor.ActivityRegistry = rt.ActivityRegistry
			if unrelated {
				_, err = successor.Sessions.CreateWithMetadata(id, rt.RootDir)
				require.NoError(t, err)
			} else {
				successor.Documents = rt.Documents
				successor.Sessions = rt.Sessions.WithContext(context.Background())
			}
			srv.rt = successor
			require.NotSame(t, binding.Sessions, successor.Sessions)
			dispatchPayload(t, srv, "ordinary", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true})
			require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "ordinary")["error"])
			if !unrelated {
				require.Contains(t, responseByID(t, parseOutput(t, out.String()), "ordinary")["error"].(map[string]any)["message"], "cannot archive a running thread")
			}
			require.Zero(t, cancellations.Load())

			dispatchPayload(t, srv, "forced", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
			response := responseByID(t, parseOutput(t, out.String()), "forced")
			if unrelated {
				require.NotNil(t, response["error"], "a colliding ID must not cancel another authority's execution")
				require.Zero(t, cancellations.Load())
				metadata, found, err := successor.Sessions.Find(id)
				require.NoError(t, err)
				require.True(t, found)
				require.Nil(t, metadata.ArchivedAt)
				retained, err := store.Get(context.Background(), run.ID)
				require.NoError(t, err)
				require.Equal(t, execution.StatusRunning, retained.Status)
				require.True(t, srv.executionRunAttached(run.ID))
				return
			}
			require.Nil(t, response["error"], "%+v", response)
			require.EqualValues(t, 1, cancellations.Load())
			metadata, found, err := successor.Sessions.Find(id)
			require.NoError(t, err)
			require.True(t, found)
			require.NotNil(t, metadata.ArchivedAt)
			stopped, err := store.Get(context.Background(), run.ID)
			require.NoError(t, err)
			require.Equal(t, execution.StatusInterrupted, stopped.Status)
			require.False(t, srv.executionRunAttached(run.ID))
			th.mu.Lock()
			running, leaseHeld := th.running, th.executionLease != nil
			th.mu.Unlock()
			require.False(t, running)
			require.False(t, leaseHeld)

			srv.retireIdlePluginRuntimes()
			dispatchPayload(t, srv, "retry", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
			require.Nil(t, responseByID(t, parseOutput(t, out.String()), "retry")["error"])
			require.EqualValues(t, 1, cancellations.Load(), "confirmation retry must not cancel twice")
			records, err := binding.Sessions.LoadHistoryRecords(id, true)
			require.NoError(t, err)
			terminals := 0
			for _, record := range records {
				if record.Role == "meta" && record.Content == turnTerminalHistoryRecord && record.ClientID == turnID {
					terminals++
					require.Equal(t, string(TurnStatusInterrupted), record.StopReason)
				}
			}
			require.Equal(t, 1, terminals, "forced stop has one durable terminal even after retry")
		})
	}
}

func TestForceArchivePreservesOtherServerExecutionLease(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	sess, err := rt.Sessions.CreateWithMetadata("archive-other-owner", rt.RootDir)
	require.NoError(t, err)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	lease := acquireExternalThreadLease(t, rt.Sessions, rt.SessionDir, sess.ID)
	defer lease.Release()
	dispatchPayload(t, srv, "forced", MethodThreadArchive, ThreadArchiveParams{ThreadID: sess.ID, Archived: true, Force: true})
	assertCrossServerBusyResponse(t, out, "forced")
	metadata, found, err := rt.Sessions.Find(sess.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, metadata.ArchivedAt)
}

func TestForceArchivePreservesLivePartialHistory(t *testing.T) {
	client := newPartialBlockingStreamClient("partial answer before archive")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	attachTestModelGeneration(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	started := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"])
	id := started.Thread.ID
	dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "work"})
	waitForMethod(t, out, NotificationAgentMessageDelta)
	dispatchPayload(t, srv, "queued", MethodTurnQueue, TurnQueueParams{ThreadID: id, Prompt: "keep this queued input", ClientID: "queued-before-archive"})
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "queued")["error"])
	successor := newTestRuntime(t, &fakeClient{})
	successor.RootDir, successor.SessionDir = rt.RootDir, rt.SessionDir
	successor.ActivityRegistry = rt.ActivityRegistry
	successor.Documents, successor.Sessions = rt.Documents, rt.Sessions.WithContext(context.Background())
	srv.rt = successor
	dispatchPayload(t, srv, "archive", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "archive")["error"])
	messages, err := loadChatMessages(successor.Sessions, id)
	require.NoError(t, err)
	found := false
	for _, message := range messages {
		if message.Role == "assistant" && message.Content == client.content {
			found = true
		}
	}
	require.True(t, found, "archive must not discard already-visible partial output: %+v", messages)
	held, err := successor.Sessions.LoadHeldUserWork(id)
	require.NoError(t, err)
	require.Len(t, held, 1)
	require.Equal(t, "queued-before-archive", held[0].ID)
	require.False(t, held[0].DeliveryUnconfirmed, "queued input must remain pending, not dispatching")
	var queued providers.ChatMessage
	require.NoError(t, json.Unmarshal(held[0].MessageJSON, &queued))
	require.Equal(t, "keep this queued input", queued.Content)
	starts := 0
	for _, envelope := range parseOutput(t, out.String()) {
		if envelope["method"] == NotificationTurnStarted {
			starts++
		}
		if envelope["method"] == NotificationTurnHeld {
			heldNotice := remarshal[TurnHeldNotification](t, envelope["params"])
			require.Empty(t, heldNotice.Error, "confirmed cancellation is not a failed held-input delivery")
		}
	}
	require.Equal(t, 1, starts, "force archive must not auto-start queued input")
	records, err := successor.Sessions.LoadHistoryRecords(id, true)
	require.NoError(t, err)
	terminals := 0
	for _, record := range records {
		if record.Role == "meta" && record.Content == turnTerminalHistoryRecord {
			terminals++
			require.Equal(t, string(TurnStatusInterrupted), record.StopReason)
		}
	}
	require.Equal(t, 1, terminals)
	metadata, ok, err := successor.Sessions.Find(id)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, metadata.ArchivedAt)
}

func TestForceArchiveDoesNotReleaseUnsettledOrSuccessorRunner(t *testing.T) {
	for _, name := range []string{"unresponsive", "successor_turn", "completed_successor"} {
		successor := name != "unresponsive"
		completed := name == "completed_successor"
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			const id = "archive-runner-fence"
			_, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir)
			require.NoError(t, err)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), id)
			require.NoError(t, err)
			done := make(chan struct{})
			var cancelCount atomic.Int32
			var admissionErr error
			th.mu.Lock()
			acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
			th.startTurnLocked("owned-turn", providers.ChatMessage{}, time.Now().UTC())
			th.turnCompletion = &threadTurnCompletion{turnID: "owned-turn", done: done}
			th.cancel = func() {
				cancelCount.Add(1)
				if successor {
					th.mu.Lock()
					th.releaseThreadExecutionLeaseLocked()
					var next bool
					next, admissionErr = srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
					if next && admissionErr == nil {
						th.startTurnLocked("successor-turn", providers.ChatMessage{}, time.Now().UTC())
						th.turnCompletion = &threadTurnCompletion{turnID: "successor-turn", done: make(chan struct{})}
						th.cancel = nil
						if completed {
							th.completeTurnLocked("successor-turn", TurnStatusCompleted, nil, time.Now().UTC(), "", "", false)
							close(th.turnCompletion.done)
						}
					}
					close(done)
					th.mu.Unlock()
				}
			}
			th.mu.Unlock()
			require.NoError(t, err)
			require.True(t, acquired)
			t.Cleanup(func() {
				th.mu.Lock()
				th.releaseTurnExecutionLocked(th.currentTurn)
				th.turnCompletion = nil
				th.mu.Unlock()
			})
			dispatchPayload(t, srv, "archive", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
			require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "archive")["error"])
			require.NoError(t, admissionErr)
			require.EqualValues(t, 1, cancelCount.Load())
			metadata, found, err := rt.Sessions.Find(id)
			require.NoError(t, err)
			require.True(t, found)
			require.Nil(t, metadata.ArchivedAt)
			th.mu.Lock()
			running, leaseHeld, current := th.running, th.executionLease != nil, th.currentTurn
			th.mu.Unlock()
			require.Equal(t, !completed, running)
			require.Equal(t, !completed, leaseHeld)
			if completed {
				require.Empty(t, current)
			} else if successor {
				require.Equal(t, "successor-turn", current)
			} else {
				require.Equal(t, "owned-turn", current)
			}
			lease, acquired, err := rt.Sessions.TryAcquireThreadExecutionLease(rt.SessionDir, id)
			require.NoError(t, err)
			if lease != nil {
				defer lease.Release()
			}
			require.Equal(t, completed, acquired, "force must leave any unconfirmed/new owner mutation fence intact")
		})
	}
}

type archiveSettlementFaultDocuments struct {
	persistence.Documents
	armed    atomic.Bool
	terminal bool
	held     bool
}

func (d *archiveSettlementFaultDocuments) Read(ctx context.Context, query persistence.Query) (persistence.View, error) {
	if d.held && d.armed.Load() && query.Namespace == "wuu.held_user_work.v1" {
		return persistence.View{}, errors.New("injected archive held-input read failure")
	}
	return d.Documents.Read(ctx, query)
}

func (d *archiveSettlementFaultDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if d.armed.Load() {
		for _, write := range tx.Writes {
			if d.held && write.Namespace == "wuu.held_user_work.v1" {
				return persistence.Receipt{}, errors.New("injected archive held-input write failure")
			}
			if write.Namespace != "wuu.session_messages.v1" {
				continue
			}
			var record session.HistoryRecord
			if err := json.Unmarshal(write.Value, &record); err != nil {
				return persistence.Receipt{}, err
			}
			if !d.held && !d.terminal && record.Role == "assistant" || d.terminal && record.Role == "meta" && record.Content == turnTerminalHistoryRecord {
				return persistence.Receipt{}, errors.New("injected archive settlement write failure")
			}
		}
	}
	return d.Documents.Commit(ctx, tx)
}

func TestForceArchiveRejectsFailedDurableSettlement(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "partial_history"
		if terminal {
			name = "terminal_record"
		}
		t.Run(name, func(t *testing.T) {
			client := newPartialBlockingStreamClient("partial output needing checkpoint")
			rt := newTestRuntime(t, &fakeClient{})
			healthy := rt.Documents
			fault := &archiveSettlementFaultDocuments{Documents: healthy, terminal: terminal}
			rt.Documents, rt.Sessions = fault, session.NewStore(context.Background(), fault)
			rt.StreamRunner.Client = client
			attachTestModelGeneration(t, rt)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
			id := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
			dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "work"})
			waitForMethod(t, out, NotificationAgentMessageDelta)
			successor := newTestRuntime(t, &fakeClient{})
			successor.RootDir, successor.SessionDir = rt.RootDir, rt.SessionDir
			successor.ActivityRegistry = rt.ActivityRegistry
			successor.Documents, successor.Sessions = healthy, session.NewStore(context.Background(), healthy)
			srv.rt = successor
			fault.armed.Store(true)
			dispatchPayload(t, srv, "archive", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
			require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "archive")["error"], "runner completion is not proof of durable partial/terminal persistence")
			srv.retireIdlePluginRuntimes()
			dispatchPayload(t, srv, "retry", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
			require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "retry")["error"], "retry cannot erase a captured failed settlement")
			dispatchPayload(t, srv, "ordinary-retry", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true})
			require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "ordinary-retry")["error"], "ordinary archive cannot bypass failed durable settlement")
			metadata, found, err := successor.Sessions.Find(id)
			require.NoError(t, err)
			require.True(t, found)
			require.Nil(t, metadata.ArchivedAt)
		})
	}
}

func TestForceArchiveRejectsFailedHeldInputFence(t *testing.T) {
	client := newPartialBlockingStreamClient("partial output")
	rt := newTestRuntime(t, &fakeClient{})
	fault := &archiveSettlementFaultDocuments{Documents: rt.Documents, held: true}
	rt.Documents, rt.Sessions = fault, session.NewStore(context.Background(), fault)
	rt.StreamRunner.Client = client
	attachTestModelGeneration(t, rt)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { fault.armed.Store(false) })
	dispatchPayload(t, srv, "start", MethodThreadStart, ThreadStartParams{})
	id := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "start")["result"]).Thread.ID
	dispatchPayload(t, srv, "turn", MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "work"})
	waitForMethod(t, out, NotificationAgentMessageDelta)
	dispatchPayload(t, srv, "queue", MethodTurnQueue, TurnQueueParams{ThreadID: id, Prompt: "must stay held", ClientID: "held-fence-input"})
	require.Nil(t, responseByID(t, parseOutput(t, out.String()), "queue")["error"])
	fault.armed.Store(true)
	dispatchPayload(t, srv, "archive", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
	require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "archive")["error"])
	th := srv.thread(id)
	th.mu.Lock()
	running, leaseHeld := th.running, th.executionLease != nil
	th.mu.Unlock()
	require.True(t, running, "failed queue fence must not bypass user interruption and cancel directly")
	require.True(t, leaseHeld)
	metadata, found, err := rt.Sessions.Find(id)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, metadata.ArchivedAt)
	fault.armed.Store(false)
	held, err := rt.Sessions.LoadHeldUserWork(id)
	require.NoError(t, err)
	require.Len(t, held, 1)
	require.Equal(t, "held-fence-input", held[0].ID)
	require.False(t, held[0].DeliveryUnconfirmed)
}

func TestExpectedTurnCancellationPreservesMixedFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		expected bool
	}{
		{"cancelled", context.Canceled, true},
		{"wrapped", fmt.Errorf("stream request failed: %w", context.Canceled), true},
		{"joined cancellation", errors.Join(context.Canceled, context.Canceled), true},
		{"provider failure", errors.Join(context.Canceled, errors.New("provider response failed")), false},
		{"receipt failure", errors.Join(context.Canceled, agent.ErrModelReceiptLifecycle), false},
		{"deadline", context.DeadlineExceeded, false},
		{"nil", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) { require.Equal(t, test.expected, isExpectedTurnCancellation(test.err)) })
	}
}

func TestForceArchiveRetainsDeadRunnerSettlementFailure(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	fault := &archiveSettlementFaultDocuments{Documents: rt.Documents, terminal: true}
	rt.Documents, rt.Sessions = fault, session.NewStore(context.Background(), fault)
	attachTestModelGeneration(t, rt)
	const id = "dead-runner-archive"
	_, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir)
	require.NoError(t, err)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	th, err := srv.ensureThreadLoaded(testRuntimeState(t, rt), id)
	require.NoError(t, err)
	th.mu.Lock()
	acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th, 0)
	th.startTurnLocked("abandoned-turn", providers.ChatMessage{}, time.Now().UTC())
	th.mu.Unlock()
	require.NoError(t, err)
	require.True(t, acquired)
	t.Cleanup(func() { th.mu.Lock(); th.releaseTurnExecutionLocked(th.currentTurn); th.mu.Unlock() })
	fault.armed.Store(true)
	dispatchPayload(t, srv, "first", MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: true})
	require.NotNil(t, responseByID(t, parseOutput(t, out.String()), "first")["error"])
	// The failed terminal write cannot become success merely because no runner remains.
	for _, force := range []bool{true, false} {
		key := fmt.Sprintf("retry-%v", force)
		dispatchPayload(t, srv, key, MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true, Force: force})
		require.NotNil(t, responseByID(t, parseOutput(t, out.String()), key)["error"])
	}
	metadata, found, err := rt.Sessions.Find(id)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, metadata.ArchivedAt)
}
