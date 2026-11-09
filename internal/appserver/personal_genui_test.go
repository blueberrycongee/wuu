package appserver

import (
	"context"
	"strings"
	"testing"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Real host persistence/admission, synthetic provider. This does not establish a
// credentialed model call. The envelope matches Personal's GenUI submission path.
func TestPersonalGenUISubmissionPersistsContextAndToolFenceAcrossResume(t *testing.T) {
	client := &fakeClient{response: providersResponse("synthetic reply")}
	rt := newTestRuntime(t, client)
	rt.UserSpaceID, rt.ProductDomain = "local", "personal"
	rt.ActivePlugins = []pluginpkg.Plugin{{Manifest: pluginpkg.Manifest{ID: "personal"}}}
	attachTestModelGeneration(t, rt, &pluginClientRequestTestRuntime{id: "personal"})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	created, err := srv.createPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "personal", pluginhost.SessionCreateParams{RequestID: "genui-main", Visibility: "plugin", ContextSource: "fresh", AgentID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	th := srv.thread(created.SessionID)
	th.mu.Lock()
	th.running = true
	th.mu.Unlock()
	state := `{"schemaVersion":1,"surfaceId":"synthetic-surface","messageId":"synthetic-message","actionId":"submit","eventId":"synthetic-event","revision":1,"state":{"count":7,"price":13},"businessStateChanged":false}`
	proof := runtime.ClientUserInput{RequestID: "synthetic-event", Prompt: "Continue using my submitted choices"}
	params := pluginhost.SessionSendParams{RequestID: "personal-genui-synthetic", UserInputRequestID: proof.RequestID, SessionID: th.ID, DisableTools: true, Cause: "personal.genui.submit", IfRunning: "queue", Input: pluginhost.SessionInput{Prompt: proof.Prompt, ContextBlocks: []pluginhost.SessionContextBlock{{Kind: "personal.genui.submission.v1", Content: state}}}}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "personal", params); err == nil {
		t.Fatal("submission without live client proof admitted")
	}
	ack, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "personal", proof)), "personal", params)
	if err != nil || ack.Receipt == nil || ack.Receipt.ExecutionState != "queued" {
		t.Fatalf("durable ACK missing: %+v %v", ack, err)
	}
	changed := params
	changed.Input.ContextBlocks = []pluginhost.SessionContextBlock{{Kind: "personal.genui.submission.v1", Content: `{"state":{"count":99}}`}}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "personal", proof)), "personal", changed); err == nil {
		t.Fatal("same input identity accepted changed state")
	}
	th.mu.Lock()
	th.running = false
	th.mu.Unlock()
	srv.Close()
	out = &lockedBuffer{}
	srv = New(rt, out)
	defer srv.Close()
	pending, err := srv.inspectPluginSession(pluginInvocationContext(t, srv.rt, context.Background()), "personal", pluginhost.SessionInspectParams{SessionID: th.ID, RequestID: params.RequestID})
	if err != nil || pending.Receipt == nil || pending.Receipt.ExecutionState != "held" {
		t.Fatalf("restart lost held submission: %+v %v", pending, err)
	}
	assertFakeClientRequestCount(t, client, 0)
	resumeProof := runtime.ClientUserInput{RequestID: proof.RequestID, ResumeReceiptID: pending.Receipt.ReceiptID}
	resume := pluginhost.SessionSendParams{RequestID: params.RequestID, UserInputRequestID: proof.RequestID, SessionID: th.ID, IfRunning: "queue", ResumeAccepted: true, ResumeReceiptID: resumeProof.ResumeReceiptID}
	if _, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "personal", resumeProof)), "personal", resume); err != nil {
		t.Fatal(err)
	}
	waitForTurnCompletedForThread(t, out, th.ID)
	client.mu.Lock()
	requests := append([]providers.ChatRequest(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("provider calls = %d", len(requests))
	}
	if len(requests[0].Tools) != 0 {
		t.Fatal("resume lost disable_tools fence")
	}
	found := false
	for _, message := range requests[0].Messages {
		if strings.Contains(message.Content, state) {
			found = true
		}
	}
	if !found {
		t.Fatal("provider did not receive immutable actual submitted state")
	}
	final, err := srv.sendPluginSession(pluginInvocationContext(t, srv.rt, runtime.WithClientUserInput(context.Background(), "personal", proof)), "personal", params)
	if err != nil || final.Receipt == nil || final.Receipt.ReceiptID != ack.Receipt.ReceiptID || final.Receipt.MessageSeq == 0 {
		t.Fatalf("retry changed receipt identity: %+v %v", final, err)
	}
	assertFakeClientRequestCount(t, client, 1)
	t.Log("real host: persisted context, held restart, explicit resume, unchanged receipt, one synthetic provider call, zero tools")
}
