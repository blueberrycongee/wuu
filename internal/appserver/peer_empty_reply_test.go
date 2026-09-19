package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestPeerReplyInOrdinarySessionRequiresExplicitCompletion(t *testing.T) {
	for _, resolution := range []string{"direct_yield", "recovered_yield", "recovered_reply", "repeated_empty"} {
		t.Run(resolution, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			kit, err := tools.New(rt.RootDir)
			if err != nil {
				t.Fatal(err)
			}
			rt.Toolkit = kit
			provider := &collaborationFlowProvider{calls: make(chan *collaborationFlowCall, 4)}
			rt.StreamRunner.Client = providers.AdaptStreamClient(provider)
			owner := &pluginTurnLifecycleClient{id: "peers", calls: make(chan pluginhost.AgentTurnLifecycleInput, 4)}
			rt.PluginHost = pluginhost.New(owner)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)

			// Use an ordinary user-created session, without a ChatAgent backend.
			if err := srv.handleLine(context.Background(), []byte(`{"id":"source","method":"thread/start"}`)); err != nil {
				t.Fatal(err)
			}
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "source")["result"]).Thread.ID
			sent, err := srv.sendPluginSession(context.Background(), owner.id, pluginhost.SessionSendParams{
				RequestID: "peer.reply:test", SessionID: threadID, Cause: "peer.reply",
				Input: pluginhost.SessionInput{Prompt: "The peer confirms the result already reported to the user."},
			})
			if err != nil {
				t.Fatal(err)
			}
			call := provider.next(t)
			if !toolDefinitionNames(call.request.Tools)["yield_turn"] {
				t.Fatal("ordinary peer follow-up has no explicit completion tool")
			}
			if resolution != "direct_yield" {
				call.response <- providers.ChatResponse{StopReason: "completed"}
				call = provider.next(t)
			}
			response := providers.ChatResponse{StopReason: "completed"}
			switch resolution {
			case "direct_yield", "recovered_yield":
				response.ToolCalls = []providers.ToolCall{{ID: "yield-receipt", Name: "yield_turn", Arguments: `{"reason":"The confirmed result is already handled."}`}}
			case "recovered_reply":
				response.Content = "The peer found an additional issue that needs attention."
			}
			call.response <- response

			select {
			case lifecycle := <-owner.calls:
				if lifecycle.RequestID != "peer.reply:test" || lifecycle.TurnID != sent.TurnID {
					t.Fatalf("completion lost request correlation: %+v", lifecycle)
				}
				if resolution == "repeated_empty" {
					if lifecycle.State != pluginhost.TurnLifecycleFailed || !strings.Contains(lifecycle.Error, "empty answer") {
						t.Fatalf("repeated empty answer was accepted: %+v", lifecycle)
					}
				} else if lifecycle.State != pluginhost.TurnLifecycleCompleted || lifecycle.Error != "" || lifecycle.FinalOutput != response.Content {
					t.Fatalf("explicit completion failed or fabricated a reply: %+v", lifecycle)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("peer follow-up did not settle")
			}
			if resolution == "repeated_empty" {
				messages := waitForMethod(t, out, NotificationTurnError)
				failed := remarshal[TurnErrorNotification](t, notificationByMethod(t, messages, NotificationTurnError)["params"])
				if failed.Turn.Status != TurnStatusFailed || !strings.Contains(failed.Error, "empty answer") {
					t.Fatalf("client lost the empty-answer failure: %+v", failed)
				}
			} else {
				messages := waitForTurnCompletedForThread(t, out, threadID)
				completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
				if completed.Turn.Status != TurnStatusCompleted || completed.Content != response.Content {
					t.Fatalf("client completion differs from lifecycle: %+v", completed)
				}
			}
			select {
			case <-provider.calls:
				t.Fatal("completion or empty-response recovery caused another model call")
			default:
			}
		})
	}
}
