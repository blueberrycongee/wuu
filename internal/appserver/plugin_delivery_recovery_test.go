package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestPluginQueuedSendRecoveryDistinguishesShutdownFromCancellation(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "cancelled"
		if shutdown {
			name = "shutdown"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			rt := newTestRuntime(t, &fakeClient{})
			rt.PluginHost = pluginhost.New()
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(func() { srv.Close() })
			created, err := srv.createPluginSession(ctx, "messenger", pluginhost.SessionCreateParams{
				RequestID: "target", Visibility: "user", ContextSource: "fresh",
			})
			if err != nil {
				t.Fatal(err)
			}
			th := srv.thread(created.SessionID)
			// Reserve the current turn without involving provider timing. No lease
			// is acquired, so shutdown can exercise only the pending input queue.
			th.mu.Lock()
			th.running = true
			th.mu.Unlock()
			params := pluginhost.SessionSendParams{RequestID: "reply", SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: "result"}}
			queued, err := srv.sendPluginSession(ctx, "messenger", params)
			if err != nil || queued.State != "queued" {
				t.Fatalf("queue: %+v, %v", queued, err)
			}
			if shutdown {
				th.mu.Lock()
				th.running = false
				th.mu.Unlock()
				srv.Close()
				srv = New(rt, &lockedBuffer{})
				th, err = srv.ensureThreadLoaded(created.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				th.running = true
				th.mu.Unlock()
			} else {
				if _, err := srv.cancelPluginSession(ctx, "messenger", pluginhost.SessionCancelParams{SessionID: created.SessionID, QueueID: queued.QueueID}); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() { th.mu.Lock(); th.running = false; th.mu.Unlock() })
			inspect := pluginhost.SessionInspectParams{SessionID: created.SessionID, RequestID: params.RequestID}
			result, err := srv.inspectPluginSession(ctx, "messenger", inspect)
			if err != nil || result.Turn == nil || result.Turn.State != "discarded" || result.Turn.Retryable != shutdown {
				t.Fatalf("discard receipt: %+v, %v", result.Turn, err)
			}
			retried, err := srv.sendPluginSession(ctx, "messenger", params)
			if err != nil {
				t.Fatal(err)
			}
			if !shutdown {
				if retried.State != "discarded" || srv.hasQueuedUserTurns(created.SessionID) {
					t.Fatalf("retry revived a cancelled input: %+v", retried)
				}
				return
			}
			if retried.State != "queued" {
				t.Fatalf("shutdown discard was not retried: %+v", retried)
			}
			result, err = srv.inspectPluginSession(ctx, "messenger", inspect)
			if err != nil || result.Turn == nil || result.Turn.State != "queued" || result.Turn.QueueID != retried.QueueID {
				t.Fatalf("stale discard hid the retry: %+v, %v", result.Turn, err)
			}
			duplicate, err := srv.sendPluginSession(ctx, "messenger", params)
			if err != nil || duplicate.QueueID != retried.QueueID {
				t.Fatalf("retry enqueued twice: %+v, %v", duplicate, err)
			}
		})
	}
}

func TestClaimedPluginDeliveryShutdownRespectsDurableAdmission(t *testing.T) {
	for _, name := range []string{"before_admission", "after_admission"} {
		admitted := name == "after_admission"
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			rt.PluginHost = pluginhost.New()
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			created, err := srv.createPluginSession(context.Background(), "messenger", pluginhost.SessionCreateParams{RequestID: "target", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			// New ran without WuuHome, so no watcher races this deterministic admission
			// boundary. The existing refresh seam closes admission after the drain has
			// claimed its queue item, but before any user message is appended.
			rt.WuuHome = t.TempDir()
			advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
			srv.refreshExtensionsForTest = func(config.Config) error {
				if !admitted {
					srv.Close()
				}
				return nil
			}
			if admitted {
				srv.beforeQueuedTurnBackgroundForTest = func() { srv.closed.Store(true) }
			}
			const requestID = "reply"
			entry := queuedTurn{id: "claimed-reply", msg: providers.ChatMessage{Role: "user", Content: "result", ClientID: pluginSessionRequestClientID("messenger", requestID)}, snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{PluginID: "messenger", RequestID: requestID, QueueID: "claimed-reply"}}}
			if !srv.enqueueQueuedUserTurn(created.SessionID, entry) {
				t.Fatal("queue rejected")
			}
			srv.drainQueuedTurns(created.SessionID)
			receipt, ok, err := session.FindPluginTurnLifecycle(rt.SessionDir, "messenger", requestID, created.SessionID, "")
			if err != nil || !ok {
				t.Fatalf("receipt: %v %v", ok, err)
			}
			var lifecycle pluginhost.AgentTurnLifecycleInput
			if err := json.Unmarshal(receipt.Payload, &lifecycle); err != nil {
				t.Fatal(err)
			}
			want := pluginhost.TurnLifecycleDiscarded
			if admitted {
				want = pluginhost.TurnLifecycleFailed
			}
			if lifecycle.State != want || lifecycle.Retryable == admitted {
				t.Fatalf("shutdown receipt = %+v; admitted=%v", lifecycle, admitted)
			}
			assertFakeClientRequestCount(t, client, 0)
		})
	}
}

func TestPluginQueueThreadLookupShutdownKeepsReplayPolicy(t *testing.T) {
	for _, test := range []struct {
		name     string
		lookup   int
		shutdown bool
		state    string
	}{
		{"pending_shutdown", 1, true, pluginhost.TurnLifecycleDiscarded},
		{"claimed_shutdown", 2, true, pluginhost.TurnLifecycleDiscarded},
		{"pending_deleted", 1, false, pluginhost.TurnLifecycleDiscarded},
		{"claimed_deleted", 2, false, pluginhost.TurnLifecycleFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.PluginHost = pluginhost.New()
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			created, err := srv.createPluginSession(context.Background(), "messenger", pluginhost.SessionCreateParams{RequestID: "target", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			lookups := 0
			srv.beforeQueuedTurnThreadLookupForTest = func() {
				lookups++
				if lookups != test.lookup {
					return
				}
				// Model Close's thread-table removal separately from its later pending
				// queue sweep, or an ordinary deletion while admission remains open.
				srv.closed.Store(test.shutdown)
				srv.mu.Lock()
				delete(srv.threads, created.SessionID)
				srv.mu.Unlock()
			}
			entry := queuedTurn{id: "lookup-reply", msg: providers.ChatMessage{Role: "user", Content: "result", ClientID: pluginSessionRequestClientID("messenger", "reply")}, snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{PluginID: "messenger", RequestID: "reply", QueueID: "lookup-reply"}}}
			if !srv.enqueueQueuedUserTurn(created.SessionID, entry) {
				t.Fatal("queue rejected")
			}
			srv.drainQueuedTurns(created.SessionID)
			receipt, ok, err := session.FindPluginTurnLifecycle(rt.SessionDir, "messenger", "reply", created.SessionID, "")
			if err != nil || !ok {
				t.Fatalf("receipt: %v %v", ok, err)
			}
			var lifecycle pluginhost.AgentTurnLifecycleInput
			if err := json.Unmarshal(receipt.Payload, &lifecycle); err != nil {
				t.Fatal(err)
			}
			if lifecycle.State != test.state || lifecycle.Retryable != test.shutdown {
				t.Fatalf("lookup receipt = %+v", lifecycle)
			}
			srv.queuedTurnMu.Lock()
			claims := len(srv.claimedQueuedTurns)
			srv.queuedTurnMu.Unlock()
			if claims != 0 {
				t.Fatalf("unsettled claims = %d", claims)
			}
		})
	}
}
