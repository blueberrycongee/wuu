package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func coordinatorModelTool(call *collaborationFlowCall, id, name string, args map[string]any) {
	raw, _ := json.Marshal(args)
	call.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: id, Name: name, Arguments: string(raw)}}}
}

func splitCoordinatorCalls(t *testing.T, provider *collaborationFlowProvider, toolID string) (continuation, worker *collaborationFlowCall) {
	t.Helper()
	for range 2 {
		call := provider.next(t)
		isContinuation := false
		for _, message := range call.request.Messages {
			if message.Role == "tool" && message.ToolCallID == toolID {
				isContinuation = true
				var result struct {
					Error string `json:"error"`
				}
				if json.Unmarshal([]byte(message.Content), &result) == nil && result.Error != "" {
					t.Fatalf("%s failed: %s", toolID, result.Error)
				}
			}
		}
		if isContinuation {
			continuation = call
		} else {
			worker = call
		}
	}
	if continuation == nil || worker == nil {
		t.Fatal("delegation did not produce a worker and a coordinator continuation")
	}
	return
}

func TestRoomCoordinatorStatusDoesNotExposeAWorkerInEmptyRoomsOrDMs(t *testing.T) {
	fixture, _ := newCollaborationFlowFixture(t)
	ctx := context.Background()
	empty := createPeerRoom(t, fixture, "Unstaffed")
	status, err := fixture.server.channelCoordinatorStatus(ctx, empty.ID)
	if err != nil || status == nil || status.State != "needs_members" || status.SessionRef != "" {
		t.Fatalf("empty room status: %#v %v", status, err)
	}
	dm, err := fixture.server.channelService.OpenDirectMessage(ctx, "human-1", fixture.identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err = fixture.server.channelCoordinatorStatus(ctx, dm.ID)
	if err != nil || status != nil {
		t.Fatalf("DM exposed a coordinator: %#v %v", status, err)
	}
}
