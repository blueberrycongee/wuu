package channels

import (
	"context"
	"testing"
)

func TestRoomOnboardingSurvivesReopenAndRetries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	room, err := service.OpenDirectMessage(ctx, "human", credential.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := RoomOnboarding{Name: "Ada", ModelPrompt: "Choose", NamePrompt: "Name?", Provider: "provider", Model: "model", Effort: "high", AvatarKey: "avatar"}
	if _, err := service.SaveRoomOnboarding(ctx, room.ID, original); err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Name = "Changed"
	retried, err := service.SaveRoomOnboarding(ctx, room.ID, changed)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Onboarding == nil || *retried.Onboarding != original {
		t.Fatalf("retry rewrote introduction: %+v", retried.Onboarding)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	rooms, err := service.ListRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 || rooms[0].Onboarding == nil || *rooms[0].Onboarding != original {
		t.Fatalf("lost introduction after reopen: %+v", rooms)
	}
	second, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Grace"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.OpenDirectMessage(ctx, "human", second.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.Onboarding != nil {
		t.Fatal("introduction leaked to another room")
	}
}
