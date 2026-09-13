package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/clients/native/testsupport"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/host"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Only the provider is controlled. Login, enrollment, relay authorization,
// encrypted transport, execution, persistence and history use production code.
type uiProvider struct{}

func (uiProvider) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "UI response"}, nil
}

func (uiProvider) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	text := ""
	toolDone := false
	for _, message := range request.Messages {
		// Runtime reminders also use the user role; only fixture commands
		// determine the controlled reply, never generated context text.
		if message.Role == "user" && strings.HasPrefix(message.Content, "ui-") {
			text = message.Content
			toolDone = false
		}
		if message.Role == "tool" && message.ToolCallID == "ui-read" {
			toolDone = true
		}
	}
	events := make(chan providers.StreamEvent, 3)
	go func() {
		defer close(events)
		if text == "ui-tools" && !toolDone {
			call := &providers.ToolCall{ID: "ui-read", Name: "read_file", Arguments: `{"path":"fixture.txt"}`, Display: &providers.ToolCallDisplay{Label: "Native tool"}}
			events <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: call.ID, Name: call.Name}}
			events <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: call}
			events <- providers.StreamEvent{Type: providers.EventDone}
			return
		}
		if strings.Contains(text, "ui-wait") {
			events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Streaming preview"}
			<-ctx.Done()
			events <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
			return
		}
		reply := "Received: " + text
		events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: reply}
		events <- providers.StreamEvent{Type: providers.EventMessage, Message: &providers.ChatMessage{Role: "assistant", Content: reply}}
		events <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return events, nil
}

func startUIComputer(root string, store *host.Store, server string, login account.Session) func() {
	must(store.SetAccount(&account.Credentials{Server: server, Username: login.Username, Token: login.Token}))
	kit, err := tools.New(root)
	must(err)
	must(os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("Native tool output\n"), 0o600))
	must(testsupport.WriteConfig(filepath.Join(root, "config.json"), "native-ui"))
	rt := &runtime.Session{
		ProviderName: "native-ui", Model: "native-ui", RootDir: root,
		WuuHome:    filepath.Join(root, ".wuu"),
		ConfigPath: filepath.Join(root, "config.json"), ConfigLoadMode: runtime.ConfigLoadFile, HomeDir: root, SessionDir: filepath.Join(root, "sessions"),
		HookDispatcher: hooks.NewDispatcher(nil), Toolkit: kit, UserQuestions: pluginhost.NewUserQuestionBroker(),
		StreamRunner: &agent.StreamRunner{Client: uiProvider{}, Model: "native-ui", SystemPrompt: "test"},
	}
	seedUICollaboration(rt.WuuHome)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h, err := host.New(host.Options{Runtime: rt, Store: store, RelayURL: "ws" + strings.TrimPrefix(server, "http") + "/v1/connect",
		Pusher: host.LogHostPusher{}, Logf: log.Printf})
	must(err)
	go func() { defer close(done); _ = h.Run(ctx) }()
	// Account-backed relays intentionally reject pairing. Observe the same
	// online directory that the phone uses instead of opening a pairing window.
	readyCtx, stopReady := context.WithTimeout(ctx, 15*time.Second)
	defer stopReady()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(readyCtx, "GET", server+"/v1/account/devices", nil)
		must(err)
		request.Header.Set("Authorization", "Bearer "+login.Token)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			var directory struct {
				Devices []account.Device `json:"devices"`
			}
			err = json.NewDecoder(response.Body).Decode(&directory)
			response.Body.Close()
			if err == nil && response.StatusCode == http.StatusOK {
				for _, device := range directory.Devices {
					if device.Pub == login.Pub && device.Online {
						return func() { cancel(); <-done }
					}
				}
			}
		}
		select {
		case <-done:
			panic("UI computer exited before connecting")
		case <-readyCtx.Done():
			cancel()
			<-done
			panic("UI computer did not connect")
		case <-ticker.C:
		}
	}
}

func seedUICollaboration(home string) {
	s, err := channels.Open(statepath.ChannelsDir(home), nil)
	must(err)
	defer s.Close()
	ctx := context.Background()
	alpha, err := s.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Alpha", Role: "Mobile collaboration verification", ProviderOverride: "native-ui", ModelOverride: "native-ui"})
	must(err)
	beta, err := s.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Beta", Role: "Review", ProviderOverride: "native-ui", ModelOverride: "native-ui"})
	must(err)
	room, err := s.CreateRoom(ctx, channels.CreateRoomParams{Kind: channels.RoomChannel, Name: "Native collaboration", CreatedBy: "local-user"})
	must(err)
	for i := 1; i <= 35; i++ {
		_, err = s.SendHuman(ctx, channels.HumanSendParams{RoomID: room.ID, HumanID: "local-user", Body: fmt.Sprintf("Collaboration history %02d — 手机与电脑共享同一房间。", i)})
		must(err)
	}
	_, err = s.SendHuman(ctx, channels.HumanSendParams{RoomID: room.ID, HumanID: "local-user", Body: "Collaboration ready", Images: []channels.MessageImage{{MediaType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}}})
	must(err)
	members := []channels.RoomMember{{MemberType: channels.MemberHuman, MemberID: "local-user"}, {MemberType: channels.MemberAgent, MemberID: alpha.Agent.ID}, {MemberType: channels.MemberAgent, MemberID: beta.Agent.ID}}
	_, err = s.UpdateRoom(ctx, channels.UpdateRoomParams{RoomID: room.ID, Members: &members})
	must(err)
	_, err = s.CreateTaskHuman(ctx, channels.TaskCreateParams{RoomID: room.ID, HumanID: "local-user", OwnerID: alpha.Agent.ID, Title: "Native collaboration task"})
	must(err)
	_, err = s.OpenDirectMessage(ctx, "local-user", alpha.Agent.ID)
	must(err)
}
