package channels

import (
	"context"
	"testing"
)

func TestMemberTaskProjectionUsesItsVisibleInitiator(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	lead, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := lead.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Check", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if task.AuthorType != MemberAgent || task.AuthorID != lead.AgentID() || task.Work == nil || task.Work.LeadNamedAgentID != lead.AgentID() {
		t.Fatalf("task lost visible initiator: %#v", task)
	}
}
