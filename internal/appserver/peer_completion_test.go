package appserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func newPeerCompletionFixture(t *testing.T) (*Server, *collaborationFlowProvider, *pluginTurnLifecycleClient, *lockedBuffer, string) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	provider := &collaborationFlowProvider{calls: make(chan *collaborationFlowCall, 4)}
	rt.StreamRunner.Client = providers.AdaptStreamClient(provider)
	owner := &pluginTurnLifecycleClient{id: "peers", calls: make(chan pluginhost.AgentTurnLifecycleInput, 8)}
	rt.PluginHost = pluginhost.New(owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	// An ordinary user-created session has no ChatAgent backend.
	if err := srv.handleLine(context.Background(), []byte(`{"id":"source","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "source")["result"]).Thread.ID
	return srv, provider, owner, out, threadID
}

func TestPeerReplyUsesNativeCompletion(t *testing.T) {
	for _, resolution := range []string{"empty", "reply", "unknown_stop", "provider_failure"} {
		t.Run(resolution, func(t *testing.T) {
			srv, provider, owner, out, threadID := newPeerCompletionFixture(t)
			sent, err := srv.sendPluginSession(context.Background(), owner.id, pluginhost.SessionSendParams{
				RequestID: "peer.reply:test", SessionID: threadID, Cause: "peer.reply",
				Input: pluginhost.SessionInput{Prompt: "The peer confirms the result already reported to the user."},
			})
			if err != nil {
				t.Fatal(err)
			}
			call := provider.next(t)
			if toolDefinitionNames(call.request.Tools)["yield_turn"] {
				t.Fatal("ordinary session still exposes the removed completion tool")
			}
			response := providers.ChatResponse{StopReason: "completed"}
			switch resolution {
			case "reply":
				response.Content = "The peer found an additional issue that needs attention."
			case "unknown_stop":
				response.StopReason = "unexpected_stop"
			}
			if resolution == "provider_failure" {
				call.failure <- errors.New("permanent provider rejection")
			} else {
				call.response <- response
			}
			failed := resolution == "unknown_stop" || resolution == "provider_failure"
			select {
			case lifecycle := <-owner.calls:
				if lifecycle.RequestID != "peer.reply:test" || lifecycle.TurnID != sent.TurnID {
					t.Fatalf("completion lost request correlation: %+v", lifecycle)
				}
				if failed {
					if lifecycle.State != pluginhost.TurnLifecycleFailed || lifecycle.Error == "" {
						t.Fatalf("abnormal completion was accepted: %+v", lifecycle)
					}
				} else if lifecycle.State != pluginhost.TurnLifecycleCompleted || lifecycle.Error != "" || lifecycle.FinalOutput != response.Content {
					t.Fatalf("completion failed or fabricated a reply: %+v", lifecycle)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("peer follow-up did not settle")
			}
			if failed {
				waitForMethod(t, out, NotificationTurnError)
			} else {
				messages := waitForTurnCompletedForThread(t, out, threadID)
				completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
				if completed.Turn.Status != TurnStatusCompleted || completed.Content != response.Content {
					t.Fatalf("client completion differs from lifecycle: %+v", completed)
				}
			}
			select {
			case <-provider.calls:
				t.Fatal("native completion caused a recovery model call")
			default:
			}
		})
	}
}

func TestPeerReceiptCancellationPreventsRedelivery(t *testing.T) {
	for _, mode := range []string{pluginhost.SessionIfRunningQueue, pluginhost.SessionIfRunningSteer} {
		t.Run(mode, func(t *testing.T) {
			srv, provider, owner, _, threadID := newPeerCompletionFixture(t)
			ctx := context.Background()
			initial, err := srv.sendPluginSession(ctx, owner.id, pluginhost.SessionSendParams{
				RequestID: "work", SessionID: threadID, Input: pluginhost.SessionInput{Prompt: "Review the files"},
			})
			if err != nil {
				t.Fatal(err)
			}
			provider.next(t) // Hold the model before it can consume the receipt.
			params := pluginhost.SessionSendParams{
				RequestID: "receipt", SessionID: threadID, IfRunning: mode,
				Input: pluginhost.SessionInput{Prompt: "Review result"},
			}
			if _, err := srv.sendPluginSession(ctx, owner.id, params); err != nil {
				t.Fatal(err)
			}
			if interrupted, err := srv.interruptThreadExecution(threadID, "", initial.TurnID); err != nil || !interrupted {
				t.Fatalf("interrupt: %v, %v", interrupted, err)
			}
			inspected, err := srv.inspectPluginSession(ctx, owner.id, pluginhost.SessionInspectParams{SessionID: threadID, RequestID: params.RequestID})
			if err != nil || inspected.Turn == nil || inspected.Turn.State != pluginhost.TurnLifecycleDiscarded || inspected.Turn.Retryable {
				t.Fatalf("user cancellation lost its durable discard receipt: %+v, %v", inspected, err)
			}
			waitForThreadLeaseRelease(t, srv.rt.SessionDir, threadID)
			retried, err := srv.sendPluginSession(ctx, owner.id, params)
			if err != nil || retried.State != pluginhost.TurnLifecycleDiscarded {
				t.Fatalf("cancelled receipt was admitted again: %+v, %v", retried, err)
			}
			select {
			case <-provider.calls:
				t.Fatal("cancelled receipt woke another model turn")
			default:
			}
		})
	}
}

func TestPeerReceiptsJoinActiveWorkOrOneLateFollowup(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "during_tool_step"
		if late {
			name = "during_final_step"
		}
		t.Run(name, func(t *testing.T) {
			srv, provider, owner, _, threadID := newPeerCompletionFixture(t)
			initial, err := srv.sendPluginSession(context.Background(), owner.id, pluginhost.SessionSendParams{
				RequestID: "work", SessionID: threadID, Input: pluginhost.SessionInput{Prompt: "Review the files"},
			})
			if err != nil {
				t.Fatal(err)
			}
			first := provider.next(t)
			for _, id := range []string{"receipt-one", "receipt-one", "receipt-two"} {
				sent, err := srv.sendPluginSession(context.Background(), owner.id, pluginhost.SessionSendParams{
					RequestID: id, SessionID: threadID, Cause: "peer.reply", IfRunning: pluginhost.SessionIfRunningSteer,
					Input: pluginhost.SessionInput{Prompt: id},
				})
				if err != nil || !sent.Steered || sent.TurnID != initial.TurnID || sent.QueueID != "" {
					t.Fatalf("receipt did not join active turn: %+v, %v", sent, err)
				}
			}
			if late {
				first.response <- providers.ChatResponse{Content: "Review done", StopReason: "completed"}
			} else {
				first.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "inspect", Name: "list_files", Arguments: `{}`}}}
			}
			next := provider.next(t)
			for _, id := range []string{"receipt-one", "receipt-two"} {
				count := 0
				for _, message := range next.request.Messages {
					if message.Role == "user" && message.Content == id {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("receipt lost or duplicated: %s appears %d times", id, count)
				}
			}
			next.response <- providers.ChatResponse{StopReason: "completed"}
			wantTurns := 1
			if late {
				wantTurns = 2
			}
			completedTurns := make(map[string]bool)
			deadline := time.After(10 * time.Second)
			for len(completedTurns) < wantTurns {
				select {
				case lifecycle := <-owner.calls:
					if lifecycle.State == pluginhost.TurnLifecycleRunning {
						continue
					}
					if lifecycle.State != pluginhost.TurnLifecycleCompleted || strings.TrimSpace(lifecycle.Error) != "" {
						t.Fatalf("receipt processing failed: %+v", lifecycle)
					}
					completedTurns[lifecycle.TurnID] = true
				case <-deadline:
					t.Fatal("receipt turn did not settle")
				}
			}
			thread := srv.thread(threadID)
			thread.mu.Lock()
			turnCount := len(thread.Turns)
			thread.mu.Unlock()
			if turnCount != wantTurns {
				t.Fatalf("receipt backlog created %d turns, want %d", turnCount, wantTurns)
			}
			select {
			case <-provider.calls:
				t.Fatal("receipts triggered another model call")
			default:
			}
		})
	}
}
