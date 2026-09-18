package channels

import (
	"context"
	"errors"
	"testing"
)

func TestPeekInboxCountsFollowupsWithoutConsumingOrDuplicating(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	send := func(roomID, body string) {
		t.Helper()
		if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: roomID, HumanID: "human-1", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	send(room.ID, "Initial request")
	client, _ := prepareIdentityTestTurn(t, s, owner.Agent.ID)
	if got, err := client.PeekInbox(ctx); err != nil || got.Unread != 0 {
		t.Fatalf("initial delivery was counted again: %+v, %v", got, err)
	}
	send(room.ID, "First correction")
	first, err := client.PeekInbox(ctx)
	if err != nil || first.Unread != 1 {
		t.Fatalf("public message and private delivery should count once: %+v, %v", first, err)
	}
	send(room.ID, "Second correction")
	otherRoom := createTestRoom(t, s, owner)
	send(otherRoom.ID, "Different room's responsibility")
	wakeBefore, err := s.WakeState(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := client.PeekInbox(ctx)
		if err != nil || got.Unread != 2 || got.RoomID != room.ID || got.DeliverySequence <= first.DeliverySequence {
			t.Fatalf("scoped peek consumed messages or included another room: %+v, %v", got, err)
		}
	}
	wakeAfter, err := s.WakeState(ctx, owner.Agent.ID)
	if err != nil || wakeBefore != wakeAfter {
		t.Fatalf("peek changed wake state: %+v -> %+v, %v", wakeBefore, wakeAfter, err)
	}
	check, err := client.Check(ctx)
	if err != nil || len(check.Collaboration) != 2 {
		t.Fatalf("messages were unavailable for explicit pull: %+v, %v", check, err)
	}
	if got, err := client.PeekInbox(ctx); err != nil || got.Unread != 0 {
		t.Fatalf("pulled messages stayed unread: %+v, %v", got, err)
	}
	send(room.ID, "Third correction")
	if got, err := client.PeekInbox(ctx); err != nil || got.Unread != 1 || got.DeliverySequence == first.DeliverySequence {
		t.Fatalf("same-count new mail lost its revision: %+v, %v", got, err)
	}
}

func TestPeekInboxRespectsRecipientSessionAndWorkScope(t *testing.T) {
	ctx := context.Background()
	s, sender, owner, room, _ := newIdentityTaskFixture(t)
	client, _ := prepareIdentityTestTurn(t, s, owner.Agent.ID)
	other := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "other-session")
	if _, err := s.EnqueueSessionInput(ctx, CollaborationSessionSendParams{SessionRef: other.SessionRef(), Body: "Private to sibling", RequestID: "private-sibling"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, OwnerID: owner.Agent.ID, Title: "Separate job"}); err != nil {
		t.Fatal(err)
	}
	if got, err := client.PeekInbox(ctx); err != nil || got.Unread != 0 {
		t.Fatalf("unrelated job or sibling inbox leaked into active work: %+v, %v", got, err)
	}
	if got, err := other.PeekInbox(ctx); err != nil || got.Unread != 1 {
		t.Fatalf("sibling cannot see its own delivery: %+v, %v", got, err)
	}
	peer := createTestAgent(t, s, "Peer")
	unauthorized, err := s.BindAgent(ctx, peer.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.sessionRef = client.SessionRef()
	if _, err := unauthorized.PeekInbox(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("another identity could peek private inbox: %v", err)
	}
}
