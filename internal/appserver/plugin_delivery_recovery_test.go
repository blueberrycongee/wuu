package appserver

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
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
