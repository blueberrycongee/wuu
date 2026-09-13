package channels

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

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
		Body: strings.Repeat("报告🙂", 100),
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
