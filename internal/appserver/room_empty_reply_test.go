package appserver

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRoomNativeCompletionDoesNotFabricateReplies(t *testing.T) {
	for _, resolution := range []string{"reply", "empty", "unknown_stop"} {
		t.Run(resolution, func(t *testing.T) {
			fixture, provider := newCollaborationFlowFixture(t)
			fixture.room = createPeerRoom(t, fixture, "Native completion", fixture.identity)
			sendRoomReplyObjective(t, fixture, "Review the delivery and act if necessary")
			first := provider.next(t)
			if toolDefinitionNames(first.request.Tools)["yield_turn"] {
				t.Fatal("room still exposes the removed completion tool")
			}
			response := providers.ChatResponse{StopReason: "completed"}
			switch resolution {
			case "reply":
				response.Content = "The requested review is complete."
			case "unknown_stop":
				response.StopReason = "unexpected_stop"
			}
			first.response <- response
			fixture.waitForCompletion(t)
			room := readRoomReplies(t, fixture, fixture.room.ID)
			switch resolution {
			case "reply":
				if len(room.Messages) != 2 || room.Messages[1].Body != response.Content || len(room.Responses) != 0 {
					t.Fatalf("reply = %+v", room)
				}
			case "empty":
				if len(room.Messages) != 1 || len(room.Responses) != 0 {
					t.Fatalf("normal completion posted an acknowledgement or left a failure: %+v", room)
				}
			case "unknown_stop":
				if len(room.Messages) != 1 || len(room.Responses) != 1 || room.Responses[0].State != "failed" || !strings.Contains(room.Responses[0].Error, "empty answer") {
					t.Fatalf("abnormal completion must remain failed: %+v", room)
				}
			}
			select {
			case <-provider.calls:
				t.Fatal("completion caused another model call")
			default:
			}
		})
	}
}
