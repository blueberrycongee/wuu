package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestNamedAgentRoomImagesReachSharedRequestPolicy(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(fmt.Sprintf("image_support_%v", supported), func(t *testing.T) {
			fixture := newCollaborationRPCFixture(t)
			peer, err := fixture.server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Peer"})
			if err != nil {
				t.Fatal(err)
			}
			room, err := fixture.server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Images", CreatedBy: localChannelHumanID, Members: []channels.RoomMember{
				{MemberType: channels.MemberAgent, MemberID: fixture.identity.ID},
				{MemberType: channels.MemberAgent, MemberID: peer.Agent.ID},
			}})
			if err != nil {
				t.Fatal(err)
			}
			fixture.room = room
			fixture.server.rt.StreamRunner.MediaInput = providers.MediaInputPolicy{ImageKnown: true, Image: supported}
			const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
			var sent ChannelMessageSendResult
			fixture.rpc(t, MethodChannelMessageSend, ChannelMessageSendParams{
				RoomID: fixture.room.ID,
				Images: []TurnStartImage{{MediaType: "image/png", Data: png}, {MediaType: "image/png", Data: png}},
			}, &sent)
			if err := fixture.server.deliverNamedAgentWake(context.Background(), fixture.identity.ID); err != nil {
				t.Fatal(err)
			}
			call := fixture.nextCall(t)
			if !call.request.MediaInput.ImageKnown || call.request.MediaInput.Image != supported {
				t.Fatalf("named runtime lost model policy: %+v", call.request.MediaInput)
			}
			// The fake transport records the raw durable history. Real adapters run
			// this shared preparation step immediately before wire encoding.
			for i := 0; i < 2; i++ {
				prepared, err := providers.PrepareMessagesForProviderRequestWithPolicy(call.request.Provider, call.request.Model, call.request.Messages, call.request.MediaInput)
				if err != nil {
					t.Fatal(err)
				}
				images, markers := 0, 0
				for _, message := range prepared {
					images += len(message.Images)
					markers += strings.Count(message.Content, "[2 images omitted: unsupported]")
					if strings.Contains(message.Content, png) {
						t.Fatal("attachment bytes were serialized as text")
					}
					for _, image := range message.Images {
						if image.Data != png || image.MediaType != "image/png" {
							t.Fatalf("changed image: %+v", image)
						}
					}
				}
				if (supported && (images != 2 || markers != 0)) || (!supported && (images != 0 || markers != 1)) {
					t.Fatalf("images=%d markers=%d", images, markers)
				}
			}
			var history ChannelMessageListResult
			fixture.rpc(t, MethodChannelMessageList, ChannelMessageListParams{RoomID: fixture.room.ID}, &history)
			if len(history.Messages) != 1 || len(history.Messages[0].Images) != 2 || history.Messages[0].Images[1].Data != png {
				t.Fatalf("stored images lost: %+v", history.Messages)
			}
			ref := channels.NamedAgentConversationRef(agentRuntimeFromNamed(fixture.identity))
			restored, err := loadChatMessages(fixture.server.rt.SessionDir, ref)
			if err != nil {
				t.Fatal(err)
			}
			storedImages := 0
			for _, message := range restored {
				storedImages += len(message.Images)
			}
			if storedImages != 2 {
				t.Fatalf("session replay lost or duplicated images: %d", storedImages)
			}
		})
	}
}

func TestRoomMediaReusesHistoryWithoutLosingCompactedImages(t *testing.T) {
	roomMessage := channels.Message{ID: "source", Images: []channels.MessageImage{
		{MediaType: "image/png", Data: "Zmlyc3Q="},
		{MediaType: "image/jpeg", Data: "c2Vjb25k"},
	}}
	var first providers.ChatMessage
	appendRoomMessageMedia(&first, []channels.Message{roomMessage, roomMessage}, nil)
	if len(first.Images) != 2 {
		t.Fatalf("overlapping windows duplicated or lost images: %+v", first.Images)
	}
	var next providers.ChatMessage
	appendRoomMessageMedia(&next, []channels.Message{roomMessage}, []providers.ChatMessage{first})
	if len(next.Images) != 0 {
		t.Fatal("later discussion round injected existing images again")
	}
	read := providers.ChatMessage{Role: "tool", ToolResult: &toolresult.Result{Content: []toolresult.ContentPart{
		{Type: toolresult.ContentTypeImage, MIMEType: "image/png", Data: "Zmlyc3Q="},
	}}}
	appendRoomMessageMedia(&next, []channels.Message{roomMessage}, []providers.ChatMessage{read})
	if len(next.Images) != 1 || next.Images[0].Data != "c2Vjb25k" {
		t.Fatalf("history read media not reused: %+v", next.Images)
	}
	var afterCompaction providers.ChatMessage
	appendRoomMessageMedia(&afterCompaction, []channels.Message{roomMessage}, nil)
	if len(afterCompaction.Images) != 2 {
		t.Fatal("compacted images were not reattached")
	}
}

func TestNamedAgentDirectMessageImagesQueuedDuringWorkAreNotReinjected(t *testing.T) {
	for _, supported := range []bool{true, false} {
		t.Run(fmt.Sprintf("image_support_%v", supported), func(t *testing.T) {
			fixture := newCollaborationRPCFixture(t)
			fixture.server.rt.StreamRunner.MediaInput = providers.MediaInputPolicy{ImageKnown: true, Image: supported}
			ctx := context.Background()
			room, err := fixture.server.channelService.OpenDirectMessage(ctx, localChannelHumanID, fixture.identity.ID)
			if err != nil {
				t.Fatal(err)
			}
			makeImage := func(c color.NRGBA) string {
				t.Helper()
				img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
				img.SetNRGBA(0, 0, c)
				var data bytes.Buffer
				if err := png.Encode(&data, img); err != nil {
					t.Fatal(err)
				}
				return base64.StdEncoding.EncodeToString(data.Bytes())
			}
			red, blue := makeImage(color.NRGBA{R: 255, A: 255}), makeImage(color.NRGBA{B: 255, A: 255})
			send := func(body, data string) {
				t.Helper()
				params := ChannelMessageSendParams{RoomID: room.ID, Body: body}
				if data != "" {
					params.Images = []TurnStartImage{{MediaType: "image/png", Data: data}}
				}
				var result ChannelMessageSendResult
				fixture.rpc(t, MethodChannelMessageSend, params, &result)
				if err := fixture.server.deliverNamedAgentWake(ctx, fixture.identity.ID); err != nil {
					t.Fatal(err)
				}
			}
			check := func(call *collaborationRPCCall, wantRed, wantBlue int) {
				t.Helper()
				counts := make(map[string]int)
				for _, message := range call.request.Messages {
					for _, image := range message.Images {
						counts[image.Data]++
					}
				}
				if counts[red] != wantRed || counts[blue] != wantBlue {
					t.Fatalf("raw request image counts: red=%d blue=%d; want %d,%d", counts[red], counts[blue], wantRed, wantBlue)
				}
				prepared, err := providers.PrepareMessagesForProviderRequestWithPolicy(call.request.Provider, call.request.Model, call.request.Messages, call.request.MediaInput)
				if err != nil {
					t.Fatal(err)
				}
				images, markers := 0, 0
				for _, message := range prepared {
					images += len(message.Images)
					markers += strings.Count(message.Content, "[1 image omitted: unsupported]")
				}
				want := wantRed + wantBlue
				if (supported && (images != want || markers != 0)) || (!supported && (images != 0 || markers != want)) {
					t.Fatalf("wire projection: images=%d markers=%d want=%d supported=%v", images, markers, want, supported)
				}
			}
			send("Inspect the red image", red)
			first := fixture.nextCall(t)
			check(first, 1, 0)
			// The provider is held open until explicitly released: this send is
			// guaranteed to happen during work, not after a timing-dependent sleep.
			send("Also inspect the blue image", blue)
			select {
			case <-first.ctx.Done():
				t.Fatal("image follow-up interrupted ongoing work")
			default:
			}
			close(first.release)
			fixture.waitForCompletion(t)
			second := fixture.nextCall(t)
			check(second, 1, 1)
			send("Now compare the two images", "")
			close(second.release)
			fixture.waitForCompletion(t)
			third := fixture.nextCall(t)
			check(third, 1, 1)
		})
	}
}
