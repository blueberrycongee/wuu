package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSessionMessageQueuedDeliveryAndReplay(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	client := &fakeClient{responses: []providers.ChatResponse{providersResponse("first"), providersResponse("received")}, onChat: func(call int, _ providers.ChatRequest) {
		if call == 1 {
			close(entered)
			<-release
		}
	}}
	rt := newTestRuntime(t, client)
	owner := &pluginTurnLifecycleClient{id: "messenger", calls: make(chan pluginhost.AgentTurnLifecycleInput, 8)}
	rt.PluginHost = pluginhost.New(owner)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(func() { once.Do(func() { close(release) }); srv.Close() })
	ctx := context.Background()
	create := func(id, plugin, visibility string) string {
		t.Helper()
		result, err := srv.createPluginSession(ctx, plugin, pluginhost.SessionCreateParams{RequestID: id, Name: id, Visibility: visibility, ContextSource: pluginhost.SessionContextFresh})
		if err != nil {
			t.Fatal(err)
		}
		return result.SessionID
	}
	source := create("Source task", "unrelated-owner", "user")
	target := create("Target task", owner.id, "user")
	private := create("Private task", "unrelated-owner", "plugin")
	params := pluginhost.SessionSendParams{RequestID: "message", SessionID: target,
		Input:        pluginhost.SessionInput{Prompt: "Delivery context\n\nPlease coordinate."},
		Presentation: &pluginhost.SessionInputPresentation{Kind: "session_message", Text: "Please coordinate.", Name: "Forged name", RelatedSessionID: source},
	}
	for _, invalid := range []string{"", "missing", target, private} {
		p := *params.Presentation
		p.RelatedSessionID = invalid
		bad := params
		bad.Presentation = &p
		if _, err := srv.sendPluginSession(ctx, owner.id, bad); err == nil {
			t.Fatalf("accepted source %q", invalid)
		}
	}
	if _, err := srv.sendPluginSession(ctx, owner.id, pluginhost.SessionSendParams{RequestID: "first", SessionID: target, Input: pluginhost.SessionInput{Prompt: "first"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first turn did not start")
	}
	queued, err := srv.sendPluginSession(ctx, owner.id, params)
	if err != nil || queued.State != "queued" {
		t.Fatalf("send = %+v, %v", queued, err)
	}
	duplicate, err := srv.sendPluginSession(ctx, owner.id, params)
	if err != nil || duplicate.QueueID != queued.QueueID {
		t.Fatalf("retry = %+v, %v", duplicate, err)
	}
	once.Do(func() { close(release) })
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-owner.calls:
			if event.RequestID != params.RequestID || event.State != "completed" {
				continue
			}
			loaded, err := srv.loadPersistedThreadState(target, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var messages []ThreadItem
			for _, turn := range loaded.Turns {
				for _, item := range turn.Items {
					if item.PresentationKind == "session_message" {
						messages = append(messages, item)
					}
				}
			}
			if len(messages) != 1 {
				t.Fatalf("replayed messages = %+v", messages)
			}
			item := messages[0]
			if item.Text != params.Presentation.Text || item.InputText != params.Input.Prompt || item.Name != "Source task" || item.RelatedSessionID != source || item.Origin != "plugin" || item.OriginID != owner.id || !item.ReadOnly {
				t.Fatalf("replayed provenance = %+v", item)
			}
			return
		case <-deadline:
			t.Fatal("queued message did not finish")
		}
	}
}
