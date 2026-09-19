package appserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func handoffTestImage(t *testing.T, c color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, c)
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(data.Bytes())
}

func handoffTestSource(t *testing.T, f *collaborationRPCFixture) channels.Message {
	t.Helper()
	var sent ChannelMessageSendResult
	f.rpc(t, MethodChannelMessageSend, ChannelMessageSendParams{
		RoomID: f.room.ID, Body: "Compare the two synthetic layouts against the attached brief.",
		Images: []TurnStartImage{
			{MediaType: "image/png", Data: handoffTestImage(t, color.NRGBA{R: 255, A: 255}), Original: true},
			{MediaType: "image/png", Data: handoffTestImage(t, color.NRGBA{B: 255, A: 255}), Original: true},
		},
		Files: []TurnStartFile{{MediaType: "application/pdf", Filename: "synthetic.pdf", Data: base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n% Synthetic handoff fixture\n%%EOF"))}},
	}, &sent)
	return sent.Message
}

func handoffTestRoom(t *testing.T, f *collaborationRPCFixture) {
	t.Helper()
	room, err := f.server.channelService.OpenDirectMessage(context.Background(), localChannelHumanID, f.identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.room = room
}

// Exercise actual OpenAI wire lowering against a loopback server. A path or a
// base64 string placed in a text part must not satisfy the media assertion.
func assertHandoffWire(t *testing.T, req providers.ChatRequest, images []providers.InputImage, files []providers.InputFile) {
	t.Helper()
	wire := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		wire <- data
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture accepted"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	client, err := openai.New(openai.ClientConfig{BaseURL: server.URL, APIKey: "synthetic-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	data := <-wire
	var payload struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	var gotImages, gotFiles []string
	for _, message := range payload.Messages {
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
			File struct {
				Data string `json:"file_data"`
			} `json:"file"`
		}
		if json.Unmarshal(message.Content, &parts) != nil {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" {
				gotImages = append(gotImages, part.ImageURL.URL)
			}
			if part.Type == "file" {
				gotFiles = append(gotFiles, part.File.Data)
			}
		}
		if bytes.Contains(bytes.ToLower(message.Content), []byte(`"required"`)) {
			t.Fatal("local admission metadata leaked into the provider media parts")
		}
	}
	if len(gotImages) != len(images) || len(gotFiles) != len(files) {
		t.Fatalf("wire lost media: images=%d files=%d, want %d/%d", len(gotImages), len(gotFiles), len(images), len(files))
	}
	for i, img := range images {
		if gotImages[i] != "data:"+img.MediaType+";base64,"+img.Data {
			t.Fatalf("wire image %d changed", i)
		}
	}
	for i, file := range files {
		if gotFiles[i] != "data:"+file.MediaType+";base64,"+file.Data {
			t.Fatalf("wire file %d changed", i)
		}
	}
}

func TestHarnessMediaCreateAndSendEndToEnd(t *testing.T) {
	for _, mode := range []string{"queue", "steer"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, provider := newCollaborationFlowFixture(t)
			f.server.channelService.SetWakeSink(nil)
			f.server.rt.StreamRunner.MediaInput = providers.MediaInputPolicy{ImageKnown: true, Image: true, FileKnown: true, File: true}
			handoffTestRoom(t, f)
			source := handoffTestSource(t, f)
			if err := f.server.deliverNamedAgentWake(ctx, f.identity.ID); err != nil {
				t.Fatal(err)
			}
			manager := provider.next(t)
			count := 0
			for _, msg := range manager.request.Messages {
				count += len(msg.Images)
			}
			if count != 2 {
				t.Fatalf("named agent did not receive source images: %d", count)
			}
			ref := channels.NamedAgentConversationRef(agentRuntimeFromNamed(f.identity))
			client, err := f.server.channelService.BindAgentSession(ctx, f.identity.ID, ref)
			if err != nil {
				t.Fatal(err)
			}
			tool := tools.NewHarnessSessionTool(&tools.Env{ChatAgent: client})
			invoke := func(id string, p channels.HarnessSessionParams) (string, string) {
				t.Helper()
				args, _ := json.Marshal(p)
				result, err := tool.ExecuteResultCall(ctx, providers.ToolCall{ID: id, Name: "session", Arguments: string(args)})
				if err != nil {
					t.Fatal(err)
				}
				var response struct {
					Session harnessSessionView `json:"session"`
					State   string             `json:"state"`
				}
				if err := json.Unmarshal([]byte(result.TextProjection()), &response); err != nil {
					t.Fatal(err)
				}
				return response.Session.ID, response.State
			}
			create := channels.HarnessSessionParams{Action: "create", WorkspaceRoot: f.server.rt.RootDir, Prompt: "Review the blue layout", Media: []channels.HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 2, Description: "Blue layout under review"}}}
			id, _ := invoke("create-with-image", create)
			worker := provider.next(t)
			check := func(req providers.ChatRequest, wantIndex int, wantFile bool) providers.ChatMessage {
				t.Helper()
				var input providers.ChatMessage
				for _, msg := range req.Messages {
					if msg.Cause == "session_management" {
						input = msg
					}
				}
				if len(input.Images) != 1 || input.Images[0].Data != source.Images[wantIndex-1].Data || !input.Images[0].Required {
					t.Fatal("selected original image did not reach the executor")
				}
				if (len(input.Files) == 1) != wantFile || wantFile && (input.Files[0].Data != source.Files[0].Data || !input.Files[0].Required) {
					t.Fatal("selected file did not reach the executor")
				}
				if !strings.Contains(input.Content, source.ID) || !strings.Contains(input.Content, source.Body) || input.RelatedSessionID != ref {
					t.Fatal("input lost evidence provenance or original explanation")
				}
				return input
			}
			input := check(worker.request, 2, false)
			if !strings.Contains(input.Content, create.Media[0].Description) {
				t.Fatal("handoff description was lost")
			}
			assertHandoffWire(t, worker.request, input.Images, nil)
			if retryID, _ := invoke("create-with-image", create); retryID != id {
				t.Fatal("replay duplicated the session")
			}
			_, state := invoke("send-with-evidence", channels.HarnessSessionParams{Action: "send", SessionID: id, Mode: mode, Prompt: "Compare with the red layout and brief", Media: []channels.HarnessMediaRef{
				{MessageID: source.ID, Kind: "image", Index: 1, Description: "Red comparison"},
				{MessageID: source.ID, Kind: "file", Index: 1, Description: "Expected layout"},
			}})
			if mode == "queue" && state != "pending" {
				t.Fatalf("busy executor did not queue the handoff: %s", state)
			}
			worker.response <- providers.ChatResponse{Content: "Blue layout inspected"}
			next := provider.next(t)
			followup := check(next.request, 1, true)
			assertHandoffWire(t, next.request, append(input.Images, followup.Images...), followup.Files)
			persisted, err := loadChatMessages(f.server.rt.SessionDir, id)
			if err != nil {
				t.Fatal(err)
			}
			// Admission commits the actual media before starting the model call.
			check(providers.ChatRequest{Messages: persisted}, 1, true)
			if _, err := providers.PrepareMessagesForProviderRequestWithPolicy("test", "text-only", persisted, providers.MediaInputPolicy{ImageKnown: true}); err == nil {
				t.Fatal("reloaded required evidence silently disappeared on a text-only model")
			}
		})
	}
}

func TestHarnessMediaRecoveryRecognizesDeliveredCopyAfterAccessRevoked(t *testing.T) {
	ctx := context.Background()
	f, provider := newCollaborationFlowFixture(t)
	f.server.channelService.SetWakeSink(nil)
	room, err := f.server.channelService.CreateRoom(ctx, channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Evidence", CreatedBy: localChannelHumanID, Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: f.identity.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	f.room = room
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: room.ID, Prompt: "Review evidence", RequestID: "parent"}, &parent)
	_ = provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	source := handoffTestSource(t, f)
	p := channels.HarnessSessionParams{Action: "create", WorkspaceRoot: f.server.rt.RootDir, Prompt: "Review image", OperationID: "delivered", Media: []channels.HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 1}}}
	result, err := f.server.HarnessSession(ctx, actor, p)
	if err != nil {
		t.Fatal(err)
	}
	_ = provider.next(t)
	data, _ := json.Marshal(result)
	var receipt struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	op, err := f.server.channelService.HarnessOperation(ctx, receipt.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	turnID := op.TurnID
	// Simulate losing the ACK after admission. Use a second host with no
	// in-memory target to require reconciliation from durable history.
	op.State, op.TurnID = "pending", ""
	if err := f.server.channelService.PutHarnessOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	members := []channels.RoomMember{{MemberType: channels.MemberHuman, MemberID: localChannelHumanID}}
	if _, err := f.server.channelService.UpdateRoom(ctx, channels.UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.channelService.ResolveHarnessMedia(ctx, actor, p.Media); err == nil {
		t.Fatal("fixture did not revoke source access")
	}
	recovery := &Server{rt: f.server.rt, channelService: f.server.channelService}
	if recovered, err := recovery.recoverHarnessMediaReceipt(ctx, &op); err != nil || !recovered {
		t.Fatalf("receipt recovery: %v %v", recovered, err)
	}
	if op.State != "submitted" || op.TurnID != turnID {
		t.Fatalf("delivered copy reported as lost: %+v", op)
	}
	op.State, op.TurnID = "pending", ""
	if err := f.server.channelService.PutHarnessOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := f.server.dispatchHarnessOperation(ctx, &op); err != nil {
		t.Fatal(err)
	}
	if op.State != "submitted" || op.TurnID != turnID {
		t.Fatalf("dispatch ignored an admitted receipt: %+v", op)
	}
}

func TestHarnessMediaQueueRechecksAccessAndTargetCapability(t *testing.T) {
	for _, change := range []string{"membership", "capability"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			f, provider := newCollaborationFlowFixture(t)
			f.server.channelService.SetWakeSink(nil)
			room, err := f.server.channelService.CreateRoom(ctx, channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Evidence", CreatedBy: localChannelHumanID, Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: f.identity.ID}}})
			if err != nil {
				t.Fatal(err)
			}
			f.room = room
			var parent ChannelSessionResult
			f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: room.ID, Prompt: "Review evidence", RequestID: "parent"}, &parent)
			_ = provider.next(t)
			actor := harnessTestActor(t, f, parent.Session.SessionRef)
			source := handoffTestSource(t, f)
			th, err := f.server.createHostSessionThread("user", "", "", pluginhost.SessionCreateParams{Name: "Busy target", Visibility: "user", ContextSource: "fresh"})
			if err != nil {
				t.Fatal(err)
			}
			// A real lease deterministically models another executor holding this
			// target while the media command waits in the durable outbox.
			lease, acquired, err := session.TryAcquireThreadExecutionLease(f.server.rt.SessionDir, th.ID)
			if err != nil || !acquired {
				t.Fatalf("lease: %v %v", acquired, err)
			}
			defer lease.Release()
			result, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: "send", SessionID: th.ID, Prompt: "Review selected image", OperationID: "queued-media", Media: []channels.HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(result)
			var receipt struct {
				OperationID string `json:"operation_id"`
				State       string `json:"state"`
			}
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.State != "pending" {
				t.Fatalf("expected queued input, got %s", receipt.State)
			}
			if change == "membership" {
				members := []channels.RoomMember{{MemberType: channels.MemberHuman, MemberID: localChannelHumanID}}
				if _, err := f.server.channelService.UpdateRoom(ctx, channels.UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
					t.Fatal(err)
				}
			} else {
				f.server.rt.StreamRunner.MediaInput = providers.MediaInputPolicy{ImageKnown: true}
				releaseThreadRuntime(th)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			if err := f.server.reconcileLocalHarnessSessions(ctx); err != nil {
				t.Fatal(err)
			}
			op, err := f.server.channelService.HarnessOperation(ctx, receipt.OperationID)
			if err != nil || op.State != "failed" || op.Error == "" {
				t.Fatalf("queued handoff did not fail explicitly: %+v %v", op, err)
			}
			if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, th.ID); err != nil || active {
				t.Fatalf("rejected queue started: %v %v", active, err)
			}
			history, err := loadChatMessages(f.server.rt.SessionDir, th.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, msg := range history {
				if msg.ClientID == op.ID {
					t.Fatal("rejected queue was persisted as executable input")
				}
			}
		})
	}
}

func TestHarnessMediaRejectsUnavailableEvidenceWithoutExecution(t *testing.T) {
	for _, action := range []string{"create", "send"} {
		for _, failure := range []string{"missing-message", "missing-index", "foreign-room", "unsupported-model", "external-engine", "empty-payload", "unsupported-file"} {
			if action == "create" && failure == "external-engine" {
				continue // create selects the native engine; only existing sessions can be external.
			}
			t.Run(action+"/"+failure, func(t *testing.T) {
				ctx := context.Background()
				f, provider := newCollaborationFlowFixture(t)
				f.server.channelService.SetWakeSink(nil)
				handoffTestRoom(t, f)
				var parent ChannelSessionResult
				f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Review evidence", RequestID: "parent"}, &parent)
				_ = provider.next(t)
				actor := harnessTestActor(t, f, parent.Session.SessionRef)
				source := handoffTestSource(t, f)
				p := channels.HarnessSessionParams{Action: action, WorkspaceRoot: f.server.rt.RootDir, Prompt: "Must not run without evidence", OperationID: "invalid", Media: []channels.HarnessMediaRef{{MessageID: source.ID, Kind: "image", Index: 1}}}
				if action == "send" || failure == "external-engine" {
					id, _, _ := harnessTestCreate(t, f, actor)
					worker := provider.next(t)
					worker.response <- providers.ChatResponse{Content: "Ready"}
					waitForThreadLeaseRelease(t, f.server.rt.SessionDir, id)
					p.SessionID, p.Action = id, "send"
				}
				switch failure {
				case "missing-message":
					p.Media[0].MessageID = "deleted-message"
				case "missing-index":
					p.Media[0].Index = 3
				case "foreign-room":
					peer, err := f.server.channelService.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Private peer"})
					if err != nil {
						t.Fatal(err)
					}
					room, err := f.server.channelService.OpenDirectMessage(ctx, localChannelHumanID, peer.Agent.ID)
					if err != nil {
						t.Fatal(err)
					}
					sent, err := f.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: room.ID, HumanID: localChannelHumanID, Body: "Private", Images: source.Images})
					if err != nil {
						t.Fatal(err)
					}
					p.Media[0].MessageID = sent.Message.ID
				case "unsupported-model":
					f.server.rt.StreamRunner.MediaInput = providers.MediaInputPolicy{ImageKnown: true}
					if p.SessionID != "" {
						releaseThreadRuntime(f.server.thread(p.SessionID))
					}
				case "external-engine":
					th := f.server.thread(p.SessionID)
					releaseThreadRuntime(th)
					th.EngineID = "codex"
				case "empty-payload", "unsupported-file":
					params := channels.HumanSendParams{RoomID: f.room.ID, HumanID: localChannelHumanID, Body: "Unavailable attachment"}
					if failure == "empty-payload" {
						params.Images = []channels.MessageImage{{MediaType: "image/png", RemoteRef: "unresolvable"}}
					} else {
						params.Files = []channels.MessageFile{{MediaType: "audio/wav", Data: "YXVkaW8="}}
						p.Media[0].Kind = "file"
					}
					sent, err := f.server.channelService.SendHuman(ctx, params)
					if err != nil {
						t.Fatal(err)
					}
					p.Media[0].MessageID = sent.Message.ID
				}
				if _, err := f.server.HarnessSession(ctx, actor, p); err == nil {
					t.Fatal("invalid evidence handoff accepted")
				}
				pending, err := f.server.channelService.PendingHarnessOperations(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, op := range pending {
					if op.Params.Prompt == p.Prompt {
						t.Fatal("failed handoff left executable pending input")
					}
				}
				if p.SessionID != "" {
					if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, p.SessionID); err != nil || active {
						t.Fatalf("rejected evidence started execution: %v %v", active, err)
					}
				}
			})
		}
	}
}
