package channels

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewRoomsCreateNoCoordinator(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	if room.RuntimeID != "" {
		t.Fatal("room created a hidden coordinator")
	}
	first := room.RuntimeID
	bootstrap, err := service.EnsureBootstrap(ctx, "human-1")
	if err != nil || len(bootstrap.Agents) != 1 {
		t.Fatalf("bootstrap: %#v %v", bootstrap, err)
	}
	room, err = service.GetRoom(ctx, room.ID)
	if err != nil || room.RuntimeID != first || len(room.Members) != 2 {
		t.Fatalf("unstable room identity: %#v %v", room, err)
	}
	raw, err := json.Marshal(room)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "runtime-") {
		t.Fatalf("hidden identity leaked: %s", raw)
	}
	dm, err := service.OpenDirectMessage(ctx, "human-1", owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dm.RuntimeID != "" {
		t.Fatal("DM created a coordinator")
	}
}

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
