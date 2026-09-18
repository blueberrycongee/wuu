package channels

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRoomPreviewIgnoresInternalActivity(t *testing.T) {
	for _, kind := range []RoomKind{RoomDM, RoomChannel} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			service := openTestService(t, nil)
			now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
			service.now = func() time.Time { return now }
			agent := createTestAgent(t, service, "Alpha")
			peer := createTestAgent(t, service, "Beta")
			var room Room
			if kind == RoomDM {
				room = createTestDirectMessage(t, service, agent)
			} else {
				room = createTestRoom(t, service, agent, peer)
			}
			assertPreview := func(want *Message) {
				t.Helper()
				got, err := service.GetRoom(ctx, room.ID)
				if err != nil {
					t.Fatal(err)
				}
				rooms, err := service.ListRooms(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(rooms) != 1 {
					t.Fatalf("rooms = %d, want 1", len(rooms))
				}
				for _, result := range []Room{got, rooms[0]} {
					preview := result.LastMessage
					if want == nil {
						if preview != nil || !result.CreatedAt.Equal(room.CreatedAt) {
							t.Fatalf("internal-only room changed preview or fallback time: %+v", result)
						}
						continue
					}
					if preview == nil || preview.ID != want.ID || preview.Kind != want.Kind || preview.Body != want.Body || !preview.CreatedAt.Equal(want.CreatedAt) {
						t.Fatalf("preview = %+v, want visible message %+v", preview, want)
					}
				}
			}
			internalActivity := func() {
				t.Helper()
				now = now.Add(time.Minute)
				if _, err := service.CreateTask(ctx, TaskCreateParams{
					AgentID: agent.Agent.ID, Token: agent.Token, RoomID: room.ID,
					OwnerID: agent.Agent.ID, Title: "Internal task", Body: "Private task details",
				}); err != nil {
					t.Fatal(err)
				}
				if kind == RoomChannel {
					if _, err := service.SendCollaboration(ctx, CollaborationSendParams{
						AgentID: agent.Agent.ID, Token: agent.Token, RoomID: room.ID,
						ToAgentID: peer.Agent.ID, Kind: CollaborationControl, Body: "Internal control",
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			internalActivity()
			assertPreview(nil)
			for _, body := range []string{"Visible conversation", "Next visible reply"} {
				now = now.Add(time.Minute)
				result, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: body})
				if err != nil {
					t.Fatal(err)
				}
				assertPreview(&result.Message)
				internalActivity()
				assertPreview(&result.Message)
			}
			if kind == RoomChannel {
				// Membership notices are visible system messages, not control records.
				members := []RoomMember{{MemberType: MemberAgent, MemberID: agent.Agent.ID}}
				if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
					t.Fatal(err)
				}
				messages, err := service.ListMessages(ctx, room.ID, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				latest := messages[len(messages)-1]
				if latest.Kind != MessageSystem {
					t.Fatalf("membership notice = %+v", latest)
				}
				assertPreview(&latest)
			}
		})
	}
}

func TestRoomPreviewUsesSequenceForSameTimeAttachmentMessages(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	service.now = func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) }
	room := createTestRoom(t, service)
	if _, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Earlier text"}); err != nil {
		t.Fatal(err)
	}
	latest, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1",
		Images: []MessageImage{{MediaType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview := got.LastMessage; preview == nil || preview.ID != latest.Message.ID || preview.Body != "" || !preview.HasAttachments {
		t.Fatalf("attachment-only preview = %+v", preview)
	}
}

func TestRoomPreviewFollowsLatestPublicMessageWithoutAttachmentPayloads(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	room := createTestRoom(t, service)
	empty := createTestRoom(t, service)
	if room.LastMessage != nil {
		t.Fatalf("empty room has a preview: %+v", room.LastMessage)
	}
	first, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "First message"})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", ReplyTo: first.Message.ID,
		Body:   strings.Repeat("报告🙂", 100),
		Images: []MessageImage{{MediaType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := service.ListRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range rooms {
		if got.ID == empty.ID {
			if got.LastMessage != nil {
				t.Fatalf("preview crossed room boundary: %+v", got.LastMessage)
			}
			continue
		}
		preview := got.LastMessage
		if preview == nil || preview.ID != latest.Message.ID || preview.AuthorID != "human-1" || !preview.CreatedAt.Equal(latest.Message.CreatedAt) {
			t.Fatalf("latest preview = %+v, want message %s", preview, latest.Message.ID)
		}
		if !preview.HasAttachments || utf8.RuneCountInString(preview.Body) != 240 || !utf8.ValidString(preview.Body) {
			t.Fatalf("invalid bounded preview: %+v", preview)
		}
		encoded, err := json.Marshal(preview)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), latest.Message.Images[0].Data) {
			t.Fatal("preview included attachment bytes")
		}
	}
}
