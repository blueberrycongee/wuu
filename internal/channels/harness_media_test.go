package channels

import (
	"context"
	"errors"
	"testing"
)

func TestHarnessMediaReferencesRespectRoomAccessAndLifetime(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	agent := createTestAgent(t, s, "Reader")
	room := createTestRoom(t, s, agent)
	other := createTestRoom(t, s, agent)
	send := func(roomID string) Message {
		t.Helper()
		result, err := s.SendHuman(ctx, HumanSendParams{RoomID: roomID, HumanID: "human-1", Body: "Synthetic source", Images: []MessageImage{{MediaType: "image/png", Data: "Zmlyc3Q="}, {MediaType: "image/png", Data: "c2Vjb25k"}}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Message
	}
	source, foreign := send(room.ID), send(other.ID)
	actor := HarnessSessionActor{AgentID: agent.Agent.ID, RoomID: room.ID}
	refs := []HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 2, Description: "Selected second image"}}
	selected, err := s.ResolveHarnessMedia(ctx, actor, refs)
	if err != nil || len(selected) != 1 || selected[0].Image.Data != source.Images[1].Data || selected[0].Source.Body != source.Body {
		t.Fatalf("selected source lost: %#v %v", selected, err)
	}
	if _, err := s.ResolveHarnessMedia(ctx, actor, []HarnessMediaRef{{MessageID: foreign.ID, Kind: "image", Index: 1}}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("membership in another room enlarged this turn's boundary: %v", err)
	}
	// Model a stored reference whose backing message has been removed.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM room_messages WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveHarnessMedia(ctx, actor, refs); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted source remained resolvable: %v", err)
	}
	actor.RoomID = other.ID
	refs[0].MessageID, refs[0].Index = foreign.ID, 1
	if _, err := s.ResolveHarnessMedia(ctx, actor, refs); err != nil {
		t.Fatal(err)
	}
	members := []RoomMember{{MemberType: MemberHuman, MemberID: "human-1"}}
	if _, err := s.UpdateRoom(ctx, UpdateRoomParams{RoomID: other.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveHarnessMedia(ctx, actor, refs); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reference survived membership revocation: %v", err)
	}
}
