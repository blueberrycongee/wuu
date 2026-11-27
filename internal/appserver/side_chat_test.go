package appserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/session"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestAttachedChatForkListAndPromote(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{responses: []providers.ChatResponse{{Content: "previous answer"}}})
	out := &lockedBuffer{}
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	srv := New(rt, out)
	t.Cleanup(srv.Close)

	dispatchPayload(t, srv, "parent", MethodThreadStart, ThreadStartParams{})
	parent := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "parent")["result"]).Thread
	fork := func(request string) Thread {
		t.Helper()
		dispatchPayload(t, srv, request, MethodThreadFork, map[string]string{"thread_id": parent.ID, "placement": "side", "anchor": "latest_completed"})
		response := responseByID(t, parseOutput(t, out.String()), request)
		if response["error"] != nil {
			t.Fatalf("fork: %v", response["error"])
		}
		return remarshal[ThreadForkResult](t, response["result"]).Thread
	}
	list := func(request, method string, params any) []Thread {
		t.Helper()
		dispatchPayload(t, srv, request, method, params)
		response := responseByID(t, parseOutput(t, out.String()), request)
		if response["error"] != nil {
			t.Fatalf("list: %v", response["error"])
		}
		return remarshal[ThreadListResult](t, response["result"]).Threads
	}
	empty := fork("empty")
	if empty.ParentID != parent.ID || empty.Placement != "side" || empty.InheritedThroughSeq != 0 || len(empty.Turns) != 0 {
		t.Fatalf("empty child: %+v", empty)
	}
	dispatchPayload(t, srv, "first", MethodTurnStart, TurnStartParams{ThreadID: parent.ID, Prompt: "previous prompt"})
	waitForNotificationCount(t, out, NotificationTurnCompleted, 1)

	// A pending tool call and its user prompt have already been written by
	// an in-progress turn. Neither may enter the attached conversation.
	if err := appendChatMessages(rt.Sessions, parent.ID, []providers.ChatMessage{
		{Role: "user", Content: "running prompt"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "pending", Name: "shell", Arguments: "{}"}}},
	}); err != nil {
		t.Fatal(err)
	}
	live := srv.thread(parent.ID)
	live.mu.Lock()
	live.running = true
	live.mu.Unlock()
	t.Cleanup(func() { live.mu.Lock(); live.running = false; live.mu.Unlock() })
	child := fork("inherited")
	if child.ParentID != parent.ID || child.ForkedFromID != parent.ID || child.InheritedThroughSeq < 2 || child.Preview != "" || len(child.Turns) != 1 {
		t.Fatalf("inherited child: %+v", child)
	}
	history, err := loadChatMessages(rt.Sessions, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	var visible []string
	for _, message := range visibleMessagesForTest(history) {
		visible = append(visible, message.Content)
	}
	if !slices.Equal(visible, []string{"previous prompt", "previous answer"}) {
		t.Fatalf("inherited history: %v", visible)
	}
	if threads := list("roots", MethodThreadListAll, ThreadListParams{}); len(threads) != 1 || threads[0].ID != parent.ID {
		t.Fatalf("roots: %+v", threads)
	}
	filter := map[string]any{"parent_thread_id": parent.ID, "placement": "side", "summary_only": true}
	if threads := list("children", MethodThreadList, filter); len(threads) != 2 || threads[0].ID != child.ID || threads[1].ID != empty.ID {
		t.Fatalf("children: %+v", threads)
	}
	dispatchPayload(t, srv, "promote", "thread/promote", map[string]string{"thread_id": child.ID})
	response := responseByID(t, parseOutput(t, out.String()), "promote")
	if response["error"] != nil {
		t.Fatalf("promote: %v", response["error"])
	}
	if threads := list("after-promote", MethodThreadList, filter); len(threads) != 1 || threads[0].ID != empty.ID {
		t.Fatalf("children after promotion: %+v", threads)
	}
	if threads := list("roots-after-promote", MethodThreadListAll, ThreadListParams{}); !slices.ContainsFunc(threads, func(thread Thread) bool {
		return thread.ID == child.ID && thread.Placement == "" && thread.ParentID == "" && thread.ForkedFromID == parent.ID
	}) {
		t.Fatalf("roots after promotion: %+v", threads)
	}
}

func TestAttachedChatUsesOwnFirstPromptForPreview(t *testing.T) {
	history := []providers.ChatMessage{
		{Seq: 1, Role: "user", Content: "parent prompt"},
		{Seq: 2, Role: "assistant", Content: "parent answer"},
		{Role: "user", Content: "child prompt"},
	}
	th := newThreadState("child", history, "", "", "", true, time.Now())
	th.InheritedThroughSeq = 2
	if got := th.snapshotLocked().Preview; got != "child prompt" {
		t.Fatalf("preview = %q", got)
	}
}

func TestAttachedChatDeletionPreflightsAllChildren(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	bindThreadDeleteArtifacts(t, rt, &threadDeleteArtifactProvider{})
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	parent, err := rt.Sessions.CreateWithMetadata("parent", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"idle", "busy", "independent"} {
		if _, err := rt.Sessions.CreateInitialized(session.Session{
			ID: id, CWD: parent.CWD, ParentID: parent.ID, Placement: session.PlacementSide, ForkedFromID: parent.ID,
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	dispatchPayload(t, srv, "promote", MethodThreadPromote, map[string]string{"thread_id": "independent"})
	lease := acquireExternalThreadLease(t, rt.Sessions, rt.SessionDir, "busy")
	dispatchPayload(t, srv, "blocked", MethodThreadDelete, ThreadDeleteParams{ThreadID: parent.ID})
	if responseByID(t, parseOutput(t, out.String()), "blocked")["error"] == nil {
		t.Fatal("deleted a parent with an executing child")
	}
	for _, id := range []string{parent.ID, "idle", "busy", "independent"} {
		if _, found, err := rt.Sessions.Find(id); err != nil || !found {
			t.Fatalf("child or parent disappeared: %s, %v", id, err)
		}
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	dispatchPayload(t, srv, "delete", MethodThreadDelete, ThreadDeleteParams{ThreadID: parent.ID})
	if response := responseByID(t, parseOutput(t, out.String()), "delete"); response["error"] != nil {
		t.Fatalf("delete: %v", response["error"])
	}
	for _, id := range []string{parent.ID, "idle", "busy", "independent"} {
		if _, found, err := rt.Sessions.Find(id); err != nil || found != (id == "independent") {
			t.Fatalf("find %s: found %v, %v", id, found, err)
		}
	}
}

func TestMigrateAttachedChatKeepsArchivedParentHistory(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	parent, err := rt.Sessions.CreateWithMetadata("parent", rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Sessions.UpdateArchived(parent.ID, true); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{
		"main_thread_id": parent.ID, "side_thread_id": "legacy-side",
		"messages": []map[string]string{
			{"id": "question", "role": "user", "text": "old question"},
			{"id": "answer", "role": "assistant", "text": "old answer", "status": "completed"},
		},
	}
	if err := persistence.Update(context.Background(), rt.Documents, func(tx *persistence.Tx) error {
		return tx.Put(legacySideChatNamespace, parent.ID, legacy)
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	binding := testRuntimeState(t, srv.rt)
	for i := 0; i < 2; i++ {
		if err := migrateLegacySideChats(binding); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	migrated, found, err := rt.Sessions.Find("legacy-side")
	if err != nil || !found || migrated.ParentID != parent.ID || migrated.Placement != session.PlacementSide || migrated.Summary != "old question" {
		t.Fatalf("migrated session = %+v, found %v, %v", migrated, found, err)
	}
	history, err := loadChatMessages(rt.Sessions, migrated.ID)
	if err != nil || len(history) != 2 || history[1].Content != "old answer" {
		t.Fatalf("migrated history = %+v, %v", history, err)
	}
	remaining, err := rt.Documents.Read(context.Background(), persistence.Query{Namespace: legacySideChatNamespace})
	if err != nil || len(remaining.Documents) != 0 {
		t.Fatalf("legacy records remain: %+v, %v", remaining.Documents, err)
	}
}

type sideChatNativeForkFixture struct {
	embeddedEngineMetadata
	forkSupported bool
}

func (*sideChatNativeForkFixture) ID() string { return "fixture.side-chat-engine" }

func (fixture *sideChatNativeForkFixture) Status() pluginhost.Status {
	status := fixture.embeddedEngineMetadata.Status()
	status.ID = fixture.ID()
	return status
}

func (fixture *sideChatNativeForkFixture) ProvidedServices() []pluginhost.ServiceDescriptor {
	services := fixture.embeddedEngineMetadata.ProvidedServices()
	services[0].Name = "engine.cursor"
	return services
}

func (fixture *sideChatNativeForkFixture) InvokeService(ctx context.Context, params pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if params.Method != "history_fork" {
		return fixture.embeddedEngineMetadata.InvokeService(ctx, params)
	}
	if !fixture.forkSupported {
		return nil, errors.New("native fork unavailable")
	}
	var request pluginprotocol.EngineHistoryForkRequest
	if err := json.Unmarshal(params.Params, &request); err != nil {
		return nil, err
	}
	if request.Ref != "main-native-ref" || !request.EndBoundary || !request.WholeTurn {
		return nil, errors.New("native fork boundary changed")
	}
	return json.Marshal(pluginprotocol.EngineHistoryForkResult{Ref: "isolated-native-ref"})
}

func TestAttachedChatExternalForkAndHandoff(t *testing.T) {
	const engine = "cursor"
	const originalRef = "main-native-ref"
	for _, tt := range []struct {
		name          string
		forkSupported bool
		running       bool
		expectSeed    bool
	}{
		{name: "idle native", forkSupported: true},
		{name: "busy native", forkSupported: true, running: true, expectSeed: true},
		{name: "native fork unavailable", expectSeed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			factory := stubProtocolEngine{id: engine}
			if err := rt.Engines().Register(factory); err != nil {
				t.Fatal(err)
			}
			parent, err := rt.Sessions.CreateInitialized(session.Session{ID: "parent", CWD: rt.RootDir, EngineID: engine, EngineRef: originalRef,
				Provider: engine, Model: "external-model", PermissionMode: "unconfined"}, []session.HistoryRecord{
				{Role: "user", Content: "main question", EngineTurnID: "native-turn-1"},
				{Role: "assistant", Content: "main reply", EngineTurnID: "native-turn-1", Phase: string(providers.MessagePhaseFinalAnswer)},
				{Role: "meta", Content: turnTerminalHistoryRecord, StopReason: string(TurnStatusCompleted)},
			})
			if err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			attachTestModelGeneration(t, rt, config.NewMemoryService("test.side-chat-config", config.Default()),
				&threadDeleteArtifactProvider{}, &threadDeleteServiceConsumer{},
				&sideChatNativeForkFixture{embeddedEngineMetadata: embeddedEngineMetadata{factory: factory}, forkSupported: tt.forkSupported})
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			if tt.running {
				lease := acquireExternalThreadLease(t, rt.Sessions, rt.SessionDir, parent.ID)
				t.Cleanup(func() { _ = lease.Release() })
				if err := appendChatMessages(rt.Sessions, parent.ID, []providers.ChatMessage{
					{Role: "user", Content: "pending prompt", EngineTurnPending: true},
					{Role: "assistant", Content: "provisional answer", Phase: providers.MessagePhaseFinalAnswer},
				}); err != nil {
					t.Fatal(err)
				}
			}
			dispatchPayload(t, srv, "external", MethodThreadFork, ThreadForkParams{ThreadID: parent.ID, Placement: session.PlacementSide, Anchor: "latest_completed"})
			response := waitForResponseByID(t, out, "external")
			if response["error"] != nil {
				t.Fatalf("fork external child: %v", response["error"])
			}
			child := remarshal[ThreadForkResult](t, response["result"]).Thread
			row, found, err := rt.Sessions.Find(child.ID)
			if err != nil || !found || row.Model != parent.Model || row.PermissionMode != parent.PermissionMode || child.InheritedThroughSeq < 2 {
				t.Fatalf("external chat selection or history lost: %+v, %v", row, err)
			}
			if !tt.expectSeed {
				if row.EngineRef != "isolated-native-ref" || row.SeedID != "" {
					t.Fatalf("native fork lost its independent ref: %+v", row)
				}
				return
			}
			seed, found, err := rt.Sessions.LoadContextSeed(row.SeedID)
			if row.EngineRef != "" || err != nil || !found || !strings.Contains(seed.Body, "main reply") || strings.Contains(seed.Body, "pending prompt") {
				t.Fatalf("invalid handoff seed: %+v, row %+v, error %v", seed, row, err)
			}
		})
	}
}
