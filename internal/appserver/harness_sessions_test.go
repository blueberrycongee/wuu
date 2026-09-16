package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func harnessTestActor(t *testing.T, f *collaborationRPCFixture, ref string) channels.HarnessSessionActor {
	t.Helper()
	b, err := f.server.channelService.LookupCollaborationSession(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return channels.HarnessSessionActor{AgentID: f.identity.ID, SessionRef: ref, TurnID: b.TurnID, RoomID: f.room.ID}
}

func harnessTestCreate(t *testing.T, f *collaborationRPCFixture, actor channels.HarnessSessionActor) (string, channels.HarnessSessionParams) {
	t.Helper()
	p := channels.HarnessSessionParams{Action: "create", Title: "Document refresh", Prompt: "Inspect current docs and report evidence.", WorkspaceRoot: f.server.rt.RootDir, OperationID: "create-docs"}
	result, err := f.server.HarnessSession(context.Background(), actor, p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result)
	var decoded struct {
		Session harnessSessionView `json:"session"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Session.ID, p
}

func TestHarnessSessionVisibleIdempotentAndWakesOriginalConversation(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Update the docs", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, p := harnessTestCreate(t, f, actor)
	worker := provider.next(t)
	if id == actor.SessionRef {
		t.Fatal("created another identity conversation")
	}
	m, found, err := session.Find(f.server.rt.SessionDir, id)
	if err != nil || !found {
		t.Fatal(err)
	}
	if m.Owner != "user" || m.Visibility != "user" || m.CWD != f.server.rt.RootDir || isNamedAgentSessionSource(m.Source) {
		t.Fatalf("not a normal project session: %+v", m)
	}
	if _, err := f.server.HarnessSession(context.Background(), actor, p); err != nil {
		t.Fatal(err)
	}
	all, err := session.List(f.server.rt.SessionDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, candidate := range all {
		if candidate.CreationRequestID == m.CreationRequestID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("replayed creation made %d sessions", count)
	}
	coordinatorModelTool(decision, "yield-parent", "yield_turn", map[string]any{"reason": "Waiting for execution result"})
	f.waitForCompletion(t)
	worker.response <- providers.ChatResponse{Content: "Inspected docs; stale installation instructions remain."}
	wake := provider.next(t)
	if text := collaborationRequestText(wake.request); !strings.Contains(text, id) {
		t.Fatalf("result wake lost session reference: %s", text)
	}
	inspected, err := f.server.inspectHarnessSession(context.Background(), channels.HarnessSessionParams{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(inspected)
	if !strings.Contains(string(data), "stale installation") {
		t.Fatalf("inspect omitted actual result: %s", data)
	}
	wake.response <- providers.ChatResponse{Content: "I found the outdated instructions and am checking the replacement."}
	f.waitForCompletion(t)
}

func TestHarnessTakeoverFencesQueuedWorkAndLeavesHistory(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Update docs", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	id, _ := harnessTestCreate(t, f, actor)
	worker := provider.next(t)
	if _, err := f.server.HarnessSession(context.Background(), actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Apply the draft", OperationID: "queued-edit"}); err != nil {
		t.Fatal(err)
	}
	th := f.server.thread(id)
	th.mu.Lock()
	c, _, _ := session.ReadControl(f.server.rt.SessionDir, id)
	th.pendingSteers = append(th.pendingSteers, providers.ChatMessage{ClientID: "auto-steer", Origin: "plugin", Content: "obsolete correction"}, providers.ChatMessage{ClientID: "human-steer", Content: "human correction"})
	th.pendingSteerControls = map[string]session.Control{"auto-steer": c}
	th.mu.Unlock()
	if err := f.server.takeHarnessControl(id, session.ControlTakenOver); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.HarnessSession(context.Background(), actor, channels.HarnessSessionParams{Action: "send", SessionID: id, Prompt: "Should not run", OperationID: "stale"}); err == nil {
		t.Fatal("automatic instruction accepted after takeover")
	}
	th.mu.Lock()
	for _, msg := range th.pendingSteers {
		if msg.ClientID == "auto-steer" {
			t.Error("revoked automatic steer survived")
		}
	}
	th.pendingSteers = nil
	th.mu.Unlock()
	worker.response <- providers.ChatResponse{Content: "Draft evidence"}
	waitForTurnCompletedForThread(t, f.out, id)
	if err := f.server.reconcileHarnessSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	ops, err := f.server.channelService.PendingHarnessOperations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Params.Prompt == "Apply the draft" {
			t.Fatal("queued instruction survived control change")
		}
	}
	m, found, err := session.Find(f.server.rt.SessionDir, id)
	if err != nil || !found || m.Entries == 0 {
		t.Fatalf("takeover lost session history: %+v %v", m, err)
	}
	coordinatorModelTool(decision, "yield-parent", "yield_turn", map[string]any{"reason": "User is editing the session"})
	f.waitForCompletion(t)
}

func TestHarnessManageExistingReleaseAndStop(t *testing.T) {
	f, provider := newCollaborationFlowFixture(t)
	ctx := context.Background()
	var parent ChannelSessionResult
	f.rpc(t, MethodChannelSessionCreate, ChannelSessionCreateParams{AgentID: f.identity.ID, RoomID: f.room.ID, Prompt: "Continue existing work", RequestID: "request"}, &parent)
	decision := provider.next(t)
	actor := harnessTestActor(t, f, parent.Session.SessionRef)
	th, err := f.server.createHostSessionThread("user", "", "", pluginhost.SessionCreateParams{Name: "Existing work", Visibility: "user", ContextSource: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(action, mode, prompt, key string) {
		t.Helper()
		if _, err := f.server.HarnessSession(ctx, actor, channels.HarnessSessionParams{Action: action, SessionID: th.ID, Mode: mode, Prompt: prompt, OperationID: key}); err != nil {
			t.Fatal(err)
		}
	}
	call("manage", "attach", "Verify the existing draft", "attach")
	if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, th.ID); err != nil || active {
		t.Fatalf("attach started execution: %v %v", active, err)
	}
	call("send", "queue", "Inspect the draft", "inspect")
	worker := provider.next(t)
	call("manage", "release", "", "release")
	if active, err := session.ThreadExecutionActive(f.server.rt.SessionDir, th.ID); err != nil || !active {
		t.Fatalf("release stopped execution: %v %v", active, err)
	}
	worker.response <- providers.ChatResponse{Content: "Draft inspected"}
	waitForTurnCompletedForThread(t, f.out, th.ID)
	call("manage", "attach", "Continue verification", "reattach")
	call("send", "queue", "Check the generated page", "continue")
	_ = provider.next(t)
	call("stop", "", "", "stop")
	waitForTurnCompletedForThread(t, f.out, th.ID)
	c, _, err := session.ReadControl(f.server.rt.SessionDir, th.ID)
	if err != nil || c.State != session.ControlPaused {
		t.Fatalf("stop did not persist pause: %+v %v", c, err)
	}
	call("manage", "release", "", "release-paused")
	metadata, found, err := session.Find(f.server.rt.SessionDir, th.ID)
	if err != nil || !found || metadata.ParentID != "" || metadata.Owner != "user" {
		t.Fatalf("management changed identity: %+v %v", metadata, err)
	}
	coordinatorModelTool(decision, "yield", "yield_turn", map[string]any{"reason": "Work stopped"})
	f.waitForCompletion(t)
}
