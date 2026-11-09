package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestAuthenticatedClientReadWhenInputEntersModelContext(t *testing.T) {
	requests := make(chan providers.ChatRequest, 1)
	release := make(chan struct{})
	client := &fakeClient{response: providersResponse("done"), onChat: func(_ int, request providers.ChatRequest) { requests <- request; <-release }}
	rt := newTestRuntime(t, client)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "fixture"}}}
	attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "fixture"})
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	created, err := srv.createPluginSession(pluginInvocationContext(t, rt, context.Background()), "fixture", pluginhost.SessionCreateParams{RequestID: "create", Visibility: "plugin", ContextSource: "fresh", AgentID: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	th := srv.thread(created.SessionID)
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	proof := runtime.ClientUserInput{RequestID: "client", Prompt: "the exact input"}
	ack, err := srv.sendPluginSession(pluginInvocationContext(t, rt, runtime.WithClientUserInput(context.Background(), "fixture", proof)), "fixture", pluginhost.SessionSendParams{RequestID: "host", UserInputRequestID: proof.RequestID, SessionID: created.SessionID, Input: pluginhost.SessionInput{Prompt: proof.Prompt}})
	if err != nil || ack.Receipt == nil || ack.State != "queued" {
		t.Fatalf("queued: %+v %v", ack, err)
	}
	readAt := func(receipt *providers.ClientInputReceipt) string {
		t.Helper()
		raw, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			ReadAt string `json:"read_at"`
		}
		if err := json.Unmarshal(raw, &status); err != nil {
			t.Fatal(err)
		}
		return status.ReadAt
	}
	if got := readAt(ack.Receipt); got != "" {
		t.Fatalf("queued input is already read: %s", got)
	}
	th.mu.Lock()
	th.running = false
	th.mu.Unlock()
	srv.tryDrainQueuedTurns(testRuntimeState(t, rt), th.ID, true)
	var request providers.ChatRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("model request did not begin")
	}
	defer close(release)
	found := false
	for _, message := range request.Messages {
		found = found || message.Role == "user" && message.Content == proof.Prompt
	}
	if !found {
		t.Fatal("model context lost accepted input")
	}
	result, err := srv.inspectPluginSession(pluginInvocationContext(t, rt, context.Background()), "fixture", pluginhost.SessionInspectParams{SessionID: th.ID, RequestID: "host"})
	if err != nil || result.Receipt == nil {
		t.Fatalf("inspect: %+v %v", result, err)
	}
	if got := readAt(result.Receipt); got == "" {
		t.Fatal("input entered model context without a read receipt")
	}
}
