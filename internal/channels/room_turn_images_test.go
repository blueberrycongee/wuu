package channels

import (
	"context"
	"fmt"
	"testing"
)

func TestRoomTurnContextPreservesSourceOutsideWindowAndChecksMembership(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha := createTestAgent(t, s, "Alpha")
	beta := createTestAgent(t, s, "Beta")
	room := createTestRoom(t, s, alpha, beta)
	sent, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Images: []MessageImage{
		{MediaType: "image/png", Data: "Zmlyc3Q="}, {MediaType: "image/jpeg", Data: "c2Vjb25k"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	check := func() {
		t.Helper()
		roomContext, err := s.RoomTurnContext(ctx, pending[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, message := range roomContext.Messages {
			if message.ID == sent.Message.ID {
				count++
				if len(message.Images) != 2 || message.Images[0].Data != "Zmlyc3Q=" || message.Images[1].Data != "c2Vjb25k" {
					t.Fatalf("source images lost: %+v", message.Images)
				}
			}
		}
		if count != 1 {
			t.Fatalf("source included %d times", count)
		}
	}
	check()
	for i := 0; i < 25; i++ {
		_, err := s.SendAgent(ctx, AgentSendParams{RoomID: room.ID, AgentID: alpha.Agent.ID, Token: alpha.Token, Body: fmt.Sprintf("update %d", i), BasisSeq: sent.Message.Seq + int64(i)})
		if err != nil {
			t.Fatal(err)
		}
	}
	check()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM room_members WHERE room_id=? AND member_id=?`, room.ID, alpha.Agent.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := s.RoomTurnContext(ctx, pending[0].ID); err == nil || len(result.Messages) != 0 {
		t.Fatalf("former member received attachments: %+v %v", result, err)
	}
}

func TestRoomDeliveryMediaCannotResolveAnotherRoomsSource(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	alpha, beta := createTestAgent(t, s, "Alpha"), createTestAgent(t, s, "Beta")
	room, private := createTestRoom(t, s, alpha), createTestRoom(t, s, beta)
	secret, err := s.SendHuman(ctx, HumanSendParams{RoomID: private.ID, HumanID: "human-1", Images: []MessageImage{{MediaType: "image/png", Data: "cHJpdmF0ZQ=="}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "review"}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingCollaborationDispatches(ctx, alpha.Agent.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	// Even a forged source reference in a delivery cannot cross its room boundary.
	if _, err := s.db.ExecContext(ctx, `UPDATE collaboration_messages SET source_message_id=? WHERE id=?`, secret.Message.ID, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	result, err := s.RoomTurnContext(ctx, pending[0].ID)
	if err != nil || len(result.Messages) != 0 {
		t.Fatalf("cross-room source resolved: %+v %v", result, err)
	}
	client, err := s.BindAgent(ctx, alpha.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if messages, err := client.QueryRoomHistory(ctx, RoomHistoryQuery{RoomID: private.ID}); err == nil || len(messages) != 0 {
		t.Fatalf("history read crossed room boundary: %+v %v", messages, err)
	}
}
