package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A retry can enter the queue before another sender commits the same input.
// Draining that retry must preserve the original terminal receipt, release its
// captured generation, and neither invoke inference nor leave a retry behind.
func TestQueuedPluginDuplicatePreservesDurableTerminal(t *testing.T) {
	for _, state := range []string{pluginhost.TurnLifecycleCompleted, pluginhost.TurnLifecycleFailed} {
		t.Run(state, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			writeTargetedUpdateConfig(t, rt)
			bindConfigSourceForTest(t, rt)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			created, err := srv.createPluginSession(pluginInvocationContext(t, rt, context.Background()), "messenger", pluginhost.SessionCreateParams{RequestID: "target", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			const request = "original-input"
			clientID := pluginSessionRequestClientID("messenger", request)
			if err := rt.Sessions.AppendHistoryRecord(created.SessionID, session.HistoryRecord{Role: "user", Content: "original accepted input", ClientID: clientID, Origin: sessionInputPlugin, OriginID: "messenger"}); err != nil {
				t.Fatal(err)
			}
			finished := time.Now().UTC()
			original, err := json.Marshal(pluginhost.AgentTurnLifecycleInput{RequestID: request, State: state, ThreadID: created.SessionID, TurnID: "original-turn", QueueID: "original-queue", CompletedAt: &finished, FinalOutput: "original durable outcome"})
			if err != nil {
				t.Fatal(err)
			}
			if err := rt.Sessions.PutPluginTurnLifecycleOutbox("messenger", request, original); err != nil {
				t.Fatal(err)
			}
			before, found, err := rt.Sessions.FindPluginTurnLifecycle("messenger", request, created.SessionID, "")
			if err != nil || !found {
				t.Fatalf("original receipt missing: %v %v", found, err)
			}
			origin, err := rt.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { origin.Close() })
			entry := queuedTurn{id: "duplicate-queue", msg: providers.ChatMessage{Role: "user", Content: "original accepted input", ClientID: clientID, Origin: sessionInputPlugin, OriginID: "messenger"}, snapshot: turnRuntimeSnapshot{PluginTurn: &pluginTurnReference{PluginID: "messenger", RequestID: request, QueueID: "duplicate-queue", Origin: origin}}}
			if !srv.enqueueQueuedUserTurn(created.SessionID, entry) {
				t.Fatal("retry queue rejected")
			}
			srv.drainQueuedTurns(testRuntimeState(t, rt), created.SessionID)
			after, found, err := rt.Sessions.FindPluginTurnLifecycle("messenger", request, created.SessionID, "")
			if err != nil || !found {
				t.Fatalf("receipt lost: %v %v", found, err)
			}
			if string(after.Payload) != string(before.Payload) || after.TurnID != before.TurnID || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("duplicate replaced terminal receipt: before=%s after=%s", before.Payload, after.Payload)
			}
			if retained := origin.Retain(); retained != nil {
				retained.Close()
				t.Fatal("duplicate queue leaked its captured generation")
			}
			srv.queuedTurnMu.Lock()
			pending := len(srv.pendingQueuedTurns[created.SessionID])
			claims := len(srv.claimedQueuedTurns)
			draining := srv.drainingQueuedTurns[created.SessionID]
			srv.queuedTurnMu.Unlock()
			if pending != 0 || claims != 0 || draining {
				t.Fatalf("duplicate queue remains: pending=%d claims=%d draining=%v", pending, claims, draining)
			}
			assertFakeClientRequestCount(t, client, 0)
			history, err := rt.Sessions.LoadHistoryRecords(created.SessionID, true)
			if err != nil {
				t.Fatal(err)
			}
			inputs := 0
			for _, record := range history {
				if record.ClientID == clientID {
					inputs++
				}
				if record.Role == "tool" || record.Role == "assistant" {
					t.Fatalf("duplicate produced model/effect history: %+v", record)
				}
			}
			if inputs != 1 {
				t.Fatalf("original input repeated %d times", inputs)
			}
		})
	}
}
