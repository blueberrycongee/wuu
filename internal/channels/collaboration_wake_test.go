package channels

import (
	"context"
	"testing"
)

func TestIdentityDeliveryReachesRouterDespiteOutstandingWake(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	lead, _ := bindTestRoomLead(t, ctx, service, room.ID)
	params := CollaborationSendParams{RoomID: room.ID, TargetKind: CollaborationTargetNamedAgent, TargetID: owner.Agent.ID, Body: "Review the layout", RequestID: "first-feedback"}
	first, err := lead.SendCollaboration(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != owner.Agent.ID {
		t.Fatalf("initial wake = %v", wakes)
	}
	// No session has accepted the first delivery, leaving an outstanding identity
	// wake. A later request must still reach the router rather than remain silent.
	params.Body, params.RequestID = "Also restore window dragging", "second-feedback"
	second, err := lead.SendCollaboration(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != owner.Agent.ID {
		t.Fatalf("outstanding wake swallowed new delivery: %v", wakes)
	}
	retry, err := lead.SendCollaboration(ctx, params)
	if err != nil || retry.ID != second.ID {
		t.Fatalf("delivery retry = %#v, %v", retry, err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("idempotent retry re-dispatched: %v", wakes)
	}
	inbox, err := service.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil || len(inbox) != 2 || inbox[0].ID != first.ID || inbox[1].ID != second.ID {
		t.Fatalf("durable deliveries = %#v, %v", inbox, err)
	}
}
