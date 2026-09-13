package appserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
)

func TestChannelMobileMessageWindowStripsOnlyRequestedAttachmentPayloads(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	room, err := server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Mobile", CreatedBy: localChannelHumanID})
	if err != nil {
		t.Fatal(err)
	}
	image := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	_, err = server.channelService.SendHuman(context.Background(), channels.HumanSendParams{RoomID: room.ID, HumanID: localChannelHumanID, Body: "attachment", Images: []channels.MessageImage{{MediaType: "image/png", Data: image}}})
	if err != nil {
		t.Fatal(err)
	}
	var mobile ChannelMessageListResult
	callChannelRPC(t, server, out, MethodChannelMessageList, ChannelMessageListParams{RoomID: room.ID, Latest: true, Limit: 1, AttachmentMetadataOnly: true}, &mobile)
	if len(mobile.Messages) != 1 || len(mobile.Messages[0].Images) != 1 || mobile.Messages[0].Images[0].Data != "" || mobile.Messages[0].Images[0].MediaType != "image/png" {
		t.Fatalf("invalid mobile projection: %+v", mobile.Messages)
	}
	var desktop ChannelMessageListResult
	callChannelRPC(t, server, out, MethodChannelMessageList, ChannelMessageListParams{RoomID: room.ID}, &desktop)
	if len(desktop.Messages) != 1 || desktop.Messages[0].Images[0].Data != image {
		t.Fatal("mobile read mutated stored or desktop attachment")
	}
}
