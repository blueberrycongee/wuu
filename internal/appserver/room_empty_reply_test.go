package appserver

import (
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestRoomEmptyReplyRequiresRecoveryOrExplicitYield(t *testing.T) {
	for _, resolution := range []string{"reply", "yield", "empty"} {
		t.Run(resolution, func(t *testing.T) {
			fixture, provider := newCollaborationFlowFixture(t)
			fixture.room = createPeerRoom(t, fixture, "Empty reply recovery", fixture.identity)
			sendRoomReplyObjective(t, fixture, "Review the delivery and act if necessary")
			first := provider.next(t)
			first.response <- providers.ChatResponse{StopReason: "completed"}
			recovery := provider.next(t)
			response := providers.ChatResponse{StopReason: "completed"}
			switch resolution {
			case "reply":
				response.Content = "The requested review is complete."
			case "yield":
				response.ToolCalls = []providers.ToolCall{{ID: "yield-receipt", Name: "yield_turn", Arguments: `{"reason":"This delivery only confirms receipt."}`}}
			}
			recovery.response <- response
			fixture.waitForCompletion(t)
			room := readRoomReplies(t, fixture, fixture.room.ID)
			switch resolution {
			case "reply":
				if len(room.Messages) != 2 || room.Messages[1].Body != response.Content || len(room.Responses) != 0 {
					t.Fatalf("recovered reply = %+v", room)
				}
			case "yield":
				if len(room.Messages) != 1 || len(room.Responses) != 0 {
					t.Fatalf("yield posted an acknowledgement or left a failure: %+v", room)
				}
			case "empty":
				if len(room.Messages) != 1 || len(room.Responses) != 1 || room.Responses[0].State != "failed" || !strings.Contains(room.Responses[0].Error, "empty answer") {
					t.Fatalf("unanswered human request must remain failed: %+v", room)
				}
			}
			select {
			case <-provider.calls:
				t.Fatal("recovery or yield caused another model call")
			default:
			}
		})
	}
}
