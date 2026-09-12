package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestServerOpensIndependentChannelsStore(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	if server.startupErr != nil {
		t.Fatalf("NewWithCredentialStore() startup error = %v", server.startupErr)
	}
	if server.channelService == nil {
		t.Fatal("server channel service is nil")
	}
	if got, want := server.channelService.Dir(), statepath.ChannelsDir(rt.WuuHome); got != want {
		t.Fatalf("channels dir = %q, want %q", got, want)
	}
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Alpha"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	if credential.Agent.ID == "" || credential.Token == "" {
		t.Fatalf("created credential = %#v", credential)
	}

	service := server.channelService
	server.Close()
	if _, err := service.ListNamedAgents(context.Background()); err == nil {
		t.Fatal("channels store remained usable after Server.Close")
	}
}

func TestChannelAgentRPCPersistsEffortOverride(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)

	var created ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, ChannelAgentCreateParams{
		Name: "Reasoner", ProviderOverride: "openai", ModelOverride: "gpt-reasoner", EffortOverride: "high",
	}, &created)
	if created.Agent.EffortOverride != "high" {
		t.Fatalf("created agent = %#v, want effort override high", created.Agent)
	}

	var updated ChannelAgentUpdateResult
	callChannelRPC(t, server, out, MethodChannelAgentUpdate, ChannelAgentUpdateParams{
		AgentID: created.Agent.ID, Name: created.Agent.Name, ProviderOverride: "openai", ModelOverride: "gpt-reasoner", EffortOverride: "low",
	}, &updated)
	if updated.Agent.EffortOverride != "low" {
		t.Fatalf("updated agent = %#v, want effort override low", updated.Agent)
	}
}

func TestChannelAgentUpdatePreservesExistingSessionSelection(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)

	var created ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, ChannelAgentCreateParams{Name: "Reasoner"}, &created)
	thread, err := server.ensureNamedAgentThreadLocked(created.Agent)
	if err != nil {
		t.Fatalf("ensureNamedAgentThreadLocked() error = %v", err)
	}
	originalRuntime := thread.execRuntime
	thread.mu.Lock()
	thread.running = true
	thread.mu.Unlock()

	var updated ChannelAgentUpdateResult
	callChannelRPC(t, server, out, MethodChannelAgentUpdate, ChannelAgentUpdateParams{
		AgentID: created.Agent.ID, Name: "Reasoner Next", ProviderOverride: "next-provider", ModelOverride: "next-model", EffortOverride: "high",
	}, &updated)

	thread.mu.Lock()
	defer thread.mu.Unlock()
	thread.running = false
	if updated.Agent.ModelOverride != "next-model" || updated.Agent.EffortOverride != "high" {
		t.Fatalf("updated agent = %#v", updated.Agent)
	}
	if thread.execRuntime != originalRuntime {
		t.Fatal("running named agent runtime was replaced before the active turn completed")
	}
	if thread.pendingRuntimeReset {
		t.Fatal("identity defaults must not schedule a reset of an existing session")
	}
	if thread.ModelProvider != rt.ProviderName || thread.Model != rt.Model || thread.ModelEffort != rt.StreamRunner.Effort {
		t.Fatalf("thread selection = (%q, %q, %q)", thread.ModelProvider, thread.Model, thread.ModelEffort)
	}
	if thread.Title != "Reasoner" {
		t.Fatalf("session title changed with identity defaults: %q", thread.Title)
	}
	selection, err := server.namedAgentPinnedSelection(agentRuntimeFromNamed(updated.Agent), session.NewID(), "")
	if err != nil || selection.Provider != "next-provider" || selection.Model != "next-model" || selection.Effort != "high" {
		t.Fatalf("new session must use updated defaults: %#v, %v", selection, err)
	}
}

func TestNamedAgentRuntimeSelectionAppliesModelAndEffortOverrides(t *testing.T) {
	agent := channels.NamedAgent{ProviderOverride: "openai", ModelOverride: "gpt-reasoner", EffortOverride: "high"}
	provider, model, effort := namedAgentModelSelection("default-provider", "default-model", "medium", agent)
	if provider != "openai" || model != "gpt-reasoner" || effort != "high" {
		t.Fatalf("named agent runtime selection = (%q, %q, %q)", provider, model, effort)
	}
	provider, model, effort = namedAgentModelSelection("default-provider", "default-model", "medium", channels.NamedAgent{})
	if provider != "default-provider" || model != "default-model" || effort != "medium" {
		t.Fatalf("inherited runtime selection = (%q, %q, %q)", provider, model, effort)
	}
}

func TestChannelHumanRPCsCreateRoomAndSendMessage(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)

	var createdAgent ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, ChannelAgentCreateParams{Name: "Alpha"}, &createdAgent)
	if createdAgent.Agent.ID == "" || createdAgent.Agent.Name != "Alpha" {
		t.Fatalf("created agent = %#v", createdAgent.Agent)
	}
	if strings.Contains(out.String(), `"token"`) {
		t.Fatal("human agent-create RPC exposed the internal agent token")
	}

	var createdRoom ChannelRoomCreateResult
	const roomAvatar = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	callChannelRPC(t, server, out, MethodChannelRoomCreate, ChannelRoomCreateParams{
		Name: "Review", AvatarImage: roomAvatar, AgentIDs: []string{createdAgent.Agent.ID},
	}, &createdRoom)
	if len(createdRoom.Room.Members) != 2 || createdRoom.Room.AvatarImage != roomAvatar {
		t.Fatalf("created room members = %#v", createdRoom.Room.Members)
	}
	if createdRoom.Room.RuntimeID != "" || createdRoom.Room.AgentID != "" {
		t.Fatal("room RPC exposed its hidden execution runtime")
	}
	var updatedRoom ChannelRoomUpdateResult
	emptyAvatar := ""
	callChannelRPC(t, server, out, MethodChannelRoomUpdate, ChannelRoomUpdateParams{
		RoomID: createdRoom.Room.ID, AvatarImage: &emptyAvatar,
	}, &updatedRoom)
	if updatedRoom.Room.AvatarImage != "" {
		t.Fatalf("updated room avatar = %q", updatedRoom.Room.AvatarImage)
	}

	var sent ChannelMessageSendResult
	callChannelRPC(t, server, out, MethodChannelMessageSend, ChannelMessageSendParams{
		RoomID: createdRoom.Room.ID, Body: "@Alpha review this",
		Images: []TurnStartImage{{
			MediaType: "image/png",
			Data:      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
		}},
	}, &sent)
	if sent.Message.Seq != 1 || sent.Message.AuthorID != localChannelHumanID || len(sent.Message.Images) != 1 {
		t.Fatalf("sent message = %#v", sent.Message)
	}

	var listed ChannelMessageListResult
	callChannelRPC(t, server, out, MethodChannelMessageList, ChannelMessageListParams{RoomID: createdRoom.Room.ID}, &listed)
	if len(listed.Messages) != 1 || listed.Messages[0].Body != "@Alpha review this" || len(listed.Messages[0].Images) != 1 {
		t.Fatalf("listed messages = %#v", listed.Messages)
	}
	namedState, err := server.channelService.WakeState(context.Background(), createdAgent.Agent.ID)
	if err != nil || !namedState.Outstanding {
		t.Fatalf("room message did not wake its named recipient: %#v, err %v", namedState, err)
	}
	var createdTask ChannelTaskCreateResult
	callChannelRPC(t, server, out, MethodChannelTaskCreate, ChannelTaskCreateParams{
		RoomID: createdRoom.Room.ID, Title: "Review patch", OwnerID: createdAgent.Agent.ID,
	}, &createdTask)
	if createdTask.Task.Kind != channels.MessageTask || createdTask.Task.TaskState != string(channels.TaskStateOpen) {
		t.Fatalf("created task = %#v", createdTask.Task)
	}
	var updatedTask ChannelTaskUpdateResult
	callChannelRPC(t, server, out, MethodChannelTaskUpdate, ChannelTaskUpdateParams{
		TaskID: createdTask.Task.ID, State: string(channels.TaskStateDone),
	}, &updatedTask)
	if updatedTask.Task.TaskState != string(channels.TaskStateDone) {
		t.Fatalf("updated task = %#v", updatedTask.Task)
	}
	client, err := server.channelService.BindAgent(context.Background(), createdAgent.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	if _, err := client.Send(context.Background(), channels.AgentSendParams{
		RoomID: createdRoom.Room.ID, Body: "@local-user finished", BasisSeq: createdTask.Task.Seq,
	}); err != nil {
		t.Fatalf("agent human mention send error = %v", err)
	}
	var mentionStatus ChannelHumanMentionStatusResult
	callChannelRPC(t, server, out, MethodChannelMentionStatus, struct{}{}, &mentionStatus)
	if mentionStatus.Count != 1 {
		t.Fatalf("human mention count = %d", mentionStatus.Count)
	}
	var mentionAck ChannelHumanMentionAckResult
	callChannelRPC(t, server, out, MethodChannelMentionAck, struct{}{}, &mentionAck)
	if mentionAck.Acknowledged != 1 {
		t.Fatalf("human mention ack = %d", mentionAck.Acknowledged)
	}
	var started ChannelAgentStartResult
	callChannelRPC(t, server, out, MethodChannelAgentStart, ChannelAgentStartParams{AgentID: createdAgent.Agent.ID}, &started)
	if started.Agent.ID != createdAgent.Agent.ID || started.WakeState.AgentID != createdAgent.Agent.ID || started.ThreadID != namedAgentSessionID(createdAgent.Agent) || server.thread(started.ThreadID) == nil {
		t.Fatalf("started named agent = %#v", started)
	}
}

func TestNamedAgentWakeAutostartCreatesIsolatedSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha review",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake() error = %v", err)
	}

	threadID := namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID)
	th := server.thread(threadID)
	if th == nil {
		t.Fatal("named agent thread was not created")
	}
	if th.NamedAgentID != credential.Agent.ID || th.CWD != filepath.Dir(credential.Agent.MemoryDir) {
		t.Fatalf("named agent thread identity = %#v", th)
	}
	if th.execRuntime == nil || th.execRuntime.StreamRunner == nil {
		t.Fatal("named agent execution runtime is nil")
	}
	prompt := th.execRuntime.StreamRunner.SystemPrompt
	if !strings.Contains(prompt, credential.Agent.MemoryDir) || !strings.Contains(prompt, "You are Alpha") {
		t.Fatalf("named agent prompt does not carry isolated identity:\n%s", prompt)
	}
	metadata, found, err := session.Find(rt.SessionDir, threadID)
	if err != nil || !found {
		t.Fatalf("Find(named agent session) = found %v, err %v", found, err)
	}
	if metadata.Source != namedAgentSessionSource+credential.Agent.ID {
		t.Fatalf("named agent session source = %q", metadata.Source)
	}
	var listed ThreadListResult
	callChannelRPC(t, server, out, MethodThreadList, ThreadListParams{
		CWD: filepath.Dir(credential.Agent.MemoryDir),
	}, &listed)
	for _, thread := range listed.Threads {
		if thread.ID == threadID {
			t.Fatalf("named agent session leaked into thread/list: %#v", thread)
		}
	}
	var searched ThreadSearchResult
	callChannelRPC(t, server, out, MethodThreadSearch, ThreadSearchParams{
		Query: credential.Agent.Name,
	}, &searched)
	for _, result := range searched.Results {
		if result.Thread.ID == threadID {
			t.Fatalf("named agent session leaked into thread/search: %#v", result)
		}
	}

	offline, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Offline"})
	if err != nil {
		t.Fatalf("CreateNamedAgent(offline) error = %v", err)
	}
	offlineRoom := createAppserverTestRoom(t, server.channelService, offline.Agent)
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: offlineRoom.ID, HumanID: "human-1", Body: "@Offline review",
	}); err != nil {
		t.Fatalf("SendHuman(offline) error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), offline.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake(offline) error = %v", err)
	}
	if server.thread(namedAgentRoomSessionID(agentRuntimeFromNamed(offline.Agent), offlineRoom.ID)) != nil {
		t.Fatal("non-autostart agent was started while offline")
	}
	state, err := server.channelService.WakeState(context.Background(), offline.Agent.ID)
	if err != nil || !state.Outstanding {
		t.Fatalf("offline wake state = %#v, err %v", state, err)
	}
}

func TestEnsureNamedAgentThreadReplacesOrdinaryRuntimeAfterLoad(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Alpha"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	th, err := server.ensureNamedAgentThreadLocked(credential.Agent)
	if err != nil {
		t.Fatalf("ensureNamedAgentThreadLocked() error = %v", err)
	}
	releaseThreadRuntime(th)
	staleRuntime, err := rt.NewThreadRuntimeForRoot(th.ID, th.CWD)
	if err != nil {
		t.Fatal(err)
	}
	th.execRuntime = staleRuntime
	th.NamedAgentID = ""

	loaded, err := server.ensureNamedAgentThreadLocked(credential.Agent)
	if err != nil {
		t.Fatalf("ensureNamedAgentThreadLocked(loaded) error = %v", err)
	}
	if loaded != th || loaded.execRuntime == nil || loaded.NamedAgentID != credential.Agent.ID {
		t.Fatalf("rebuilt named agent thread = %#v", loaded)
	}
	if loaded.execRuntime == staleRuntime {
		t.Fatal("ordinary restored runtime was reused without named-agent identity")
	}
	wantTools := map[string]bool{
		"chat_check": false, "chat_read": false, "chat_send": false, "collaboration_send": false,
		"chat_draft": false, "chat_task": false, "chat_work": false, "chat_remind": false,
		"read_file": false, "list_files": false, "bash": false,
	}
	for _, definition := range loaded.execRuntime.Toolkit.Definitions() {
		if _, ok := wantTools[definition.Name]; ok {
			wantTools[definition.Name] = true
		}
	}
	for name, found := range wantTools {
		if !found {
			t.Fatalf("named-agent runtime omitted main/chat tool %q", name)
		}
	}
	if !strings.Contains(loaded.execRuntime.StreamRunner.SystemPrompt, credential.Agent.MemoryDir) {
		t.Fatal("loaded named-agent thread was rebuilt without chat tools or private memory prompt")
	}
}

func TestNonAutostartNamedAgentWakeLoadsExistingPersistedSession(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Alpha"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	ref := namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID)
	client, err := server.channelService.BindAgent(context.Background(), credential.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.BindCollaborationSession(context.Background(), channels.CollaborationSessionBindParams{
		SessionRef: ref, RoomID: room.ID, Purpose: channels.CollaborationSessionConversation,
		Provider: rt.ProviderName, Model: rt.Model,
	}); err != nil {
		t.Fatal(err)
	}
	thread, err := server.ensureAgentRuntimeSessionThreadLocked(agentRuntimeFromNamed(credential.Agent), ref)
	if err != nil {
		t.Fatalf("ensureNamedAgentThreadLocked() error = %v", err)
	}
	server.mu.Lock()
	delete(server.threads, thread.ID)
	server.mu.Unlock()
	releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: thread.execRuntime})
	thread.execRuntime = nil
	rt.Model = "new-global-model"

	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha resume",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake() error = %v", err)
	}
	loaded := server.thread(thread.ID)
	if loaded == nil || loaded.NamedAgentID != credential.Agent.ID || loaded.Source != namedAgentSessionSource+credential.Agent.ID {
		t.Fatalf("loaded persisted non-autostart thread = %#v", loaded)
	}
	if loaded.Model != "fake-model" {
		t.Fatalf("loaded named agent model = %q, want persisted session model", loaded.Model)
	}
	metadata, found, err := session.Find(rt.SessionDir, thread.ID)
	if err != nil || !found || metadata.Model != "fake-model" {
		t.Fatalf("persisted named agent model = %q, found %v, err %v", metadata.Model, found, err)
	}
}

func TestChannelAgentResetRequestsTheCurrentThreadOwner(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	threadID := namedAgentSessionID(credential.Agent)
	owner, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, threadID)
	if err != nil || !acquired || owner == nil {
		t.Fatalf("owner lease = %v, acquired %v, err %v", owner, acquired, err)
	}
	defer owner.Release()

	var reset ChannelAgentResetResult
	callChannelRPC(t, server, out, MethodChannelAgentReset, ChannelAgentResetParams{AgentID: credential.Agent.ID}, &reset)
	if !reset.Requested || reset.ThreadID != threadID || reset.Agent.ID != credential.Agent.ID {
		t.Fatalf("reset result = %#v", reset)
	}
	requested, err := owner.ResetRequested()
	if err != nil || !requested {
		t.Fatalf("owner reset requested = %v, err %v", requested, err)
	}
}

func TestChannelAgentListSeesAnOwnerInAnotherAppServer(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	owner, acquired, err := session.TryAcquireThreadExecutionLease(rt.SessionDir, namedAgentSessionID(credential.Agent))
	if err != nil || !acquired || owner == nil {
		t.Fatalf("owner lease = %v, acquired %v, err %v", owner, acquired, err)
	}
	defer owner.Release()

	var listed ChannelAgentListResult
	callChannelRPC(t, server, out, MethodChannelAgentList, nil, &listed)
	if len(listed.Agents) != 1 || listed.Agents[0].ActivityStatus != "thinking" {
		t.Fatalf("cross-process agent activity = %#v", listed.Agents)
	}
}

func TestChannelAgentResetClearsStaleWakeWithoutDroppingInbox(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha queued",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}

	var reset ChannelAgentResetResult
	callChannelRPC(t, server, out, MethodChannelAgentReset, ChannelAgentResetParams{AgentID: credential.Agent.ID}, &reset)
	if reset.Requested || reset.WakeState.Outstanding || reset.WakeState.Pending {
		t.Fatalf("idle reset result = %#v", reset)
	}
	inbox, err := server.channelService.ListInbox(context.Background(), credential.Agent.ID, true)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("preserved inbox = %#v, err %v", inbox, err)
	}
}

func TestChannelAgentResetInterruptsCurrentWakeAndDrainsFollowup(t *testing.T) {
	client := newBlockingStreamClient("followup done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	})
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha first",
	}); err != nil {
		t.Fatalf("first SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("first deliverNamedAgentWake() error = %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("named agent turn did not start")
	}
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha second",
	}); err != nil {
		t.Fatalf("second SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("second deliverNamedAgentWake() error = %v", err)
	}
	thread := server.thread(namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID))
	thread.mu.Lock()
	firstTurnID := thread.currentTurn
	thread.mu.Unlock()

	var reset ChannelAgentResetResult
	callChannelRPC(t, server, out, MethodChannelAgentReset, ChannelAgentResetParams{AgentID: credential.Agent.ID}, &reset)
	if !reset.Requested {
		t.Fatalf("running reset result = %#v", reset)
	}
	transitionDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(transitionDeadline) {
		thread.mu.Lock()
		currentTurnID := thread.currentTurn
		thread.mu.Unlock()
		if currentTurnID != firstTurnID {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	thread.mu.Lock()
	currentTurnID := thread.currentTurn
	thread.mu.Unlock()
	if currentTurnID == firstTurnID {
		t.Fatal("reset did not interrupt the current named-agent turn")
	}
	close(client.release)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, stateErr := server.channelService.WakeState(context.Background(), credential.Agent.ID)
		if stateErr == nil && !state.Outstanding && !state.Pending {
			inbox, inboxErr := server.channelService.ListInbox(context.Background(), credential.Agent.ID, true)
			if inboxErr == nil && len(inbox) == 0 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, _ := server.channelService.WakeState(context.Background(), credential.Agent.ID)
	inbox, _ := server.channelService.ListInbox(context.Background(), credential.Agent.ID, true)
	t.Fatalf("reset followup did not drain: state %#v, inbox %#v", state, inbox)
}

func TestNamedAgentRunningWakeUsesDurableInboxWithoutOrdinaryQueuedTurn(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha first",
	}); err != nil {
		t.Fatalf("first SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("first deliverNamedAgentWake() error = %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("named agent turn did not start")
	}
	var listed ChannelAgentListResult
	callChannelRPC(t, server, server.out.(*lockedBuffer), MethodChannelAgentList, nil, &listed)
	if len(listed.Agents) != 1 || listed.Agents[0].ActivityStatus != "thinking" ||
		len(listed.Agents[0].ActivityRoomIDs) != 1 || listed.Agents[0].ActivityRoomIDs[0] != room.ID {
		t.Fatalf("running named agent activity = %#v, want room %q", listed.Agents, room.ID)
	}
	if err := server.channelService.ClearWakeOnCheck(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("ClearWakeOnCheck() error = %v", err)
	}
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha second",
	}); err != nil {
		t.Fatalf("second SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("second deliverNamedAgentWake() error = %v", err)
	}
	state, err := server.channelService.WakeState(context.Background(), credential.Agent.ID)
	if err != nil || !state.Outstanding || !state.Pending {
		t.Fatalf("running wake state = %#v, err %v", state, err)
	}
	held, err := server.loadHeldUserTurns(namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID))
	if err != nil || len(held) != 0 {
		t.Fatalf("held named agent wake = %#v, err %v", held, err)
	}

	close(client.release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err = server.channelService.WakeState(context.Background(), credential.Agent.ID)
		held, _ = server.loadHeldUserTurns(namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID))
		if err == nil && !state.Pending && len(held) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pending wake did not drain: state %#v, held %#v", state, held)
}

func TestNamedAgentRunsTwoWorkSessionsConcurrently(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	})
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	rooms := make([]channels.Room, 0, 2)
	for index := 0; index < 2; index++ {
		room, createErr := server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{
			Kind: channels.RoomChannel, Name: fmt.Sprintf("Room %d", index+1), CreatedBy: "human-1",
			Members: []channels.RoomMember{
				{MemberType: channels.MemberHuman, MemberID: "human-1"},
				{MemberType: channels.MemberAgent, MemberID: credential.Agent.ID},
			},
		})
		if createErr != nil {
			t.Fatalf("CreateRoom(%d) error = %v", index, createErr)
		}
		rooms = append(rooms, room)
	}
	tasks := make([]channels.Message, 0, len(rooms))
	for index, room := range rooms {
		task, createErr := server.channelService.CreateTaskHuman(context.Background(), channels.TaskCreateParams{
			RoomID: room.ID, HumanID: "human-1", OwnerID: credential.Agent.ID,
			Title: fmt.Sprintf("Work %d", index+1), Body: fmt.Sprintf("Handle work %d", index+1),
		})
		if createErr != nil {
			t.Fatalf("CreateTaskHuman(%d) error = %v", index, createErr)
		}
		tasks = append(tasks, task)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake() error = %v", err)
	}

	agent := agentRuntimeFromNamed(credential.Agent)
	threadIDs := []string{
		namedAgentWorkSessionID(agent, tasks[0].ID),
		namedAgentWorkSessionID(agent, tasks[1].ID),
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if threadIsRunning(server.thread(threadIDs[0])) && threadIsRunning(server.thread(threadIDs[1])) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if threadIDs[0] == threadIDs[1] || !threadIsRunning(server.thread(threadIDs[0])) || !threadIsRunning(server.thread(threadIDs[1])) {
		t.Fatalf("work sessions are not independently running: %q=%v %q=%v", threadIDs[0], threadIsRunning(server.thread(threadIDs[0])), threadIDs[1], threadIsRunning(server.thread(threadIDs[1])))
	}
	for index, threadID := range threadIDs {
		thread := server.thread(threadID)
		if thread.NamedAgentID != credential.Agent.ID || thread.execRuntime == nil || thread.execRuntime.Toolkit == nil {
			t.Fatalf("work session %d runtime = %#v", index, thread)
		}
		bound, bindErr := server.channelService.BindAgentSession(context.Background(), credential.Agent.ID, threadID)
		if bindErr != nil || bound.SessionRef() != threadID {
			t.Fatalf("work session %d client ref = %q, err %v", index, bound.SessionRef(), bindErr)
		}
	}
	var listed ChannelAgentListResult
	callChannelRPC(t, server, out, MethodChannelAgentList, nil, &listed)
	if len(listed.Agents) != 1 || listed.Agents[0].ActivityStatus != "thinking" || len(listed.Agents[0].ActivityRoomIDs) != 2 {
		t.Fatalf("multi-session agent activity = %#v", listed.Agents)
	}
	if err := server.channelService.ClearWakeOnCheck(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("ClearWakeOnCheck() error = %v", err)
	}
	sibling := server.thread(threadIDs[1])
	sibling.mu.Lock()
	siblingTurnID := sibling.currentTurn
	sibling.mu.Unlock()
	server.InterruptSession(credential.Agent.ID, threadIDs[0])
	interruptDeadline := time.Now().Add(5 * time.Second)
	for threadIsRunning(server.thread(threadIDs[0])) && time.Now().Before(interruptDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	sibling.mu.Lock()
	siblingStillRunning := sibling.running && sibling.currentTurn == siblingTurnID
	sibling.mu.Unlock()
	if threadIsRunning(server.thread(threadIDs[0])) || !siblingStillRunning {
		t.Fatalf("session-targeted interrupt affected the wrong work: target=%v sibling=%v", threadIsRunning(server.thread(threadIDs[0])), siblingStillRunning)
	}
	boundClient, err := server.channelService.BindAgent(context.Background(), credential.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	bindings, err := boundClient.ListCollaborationSessions(context.Background(), channels.CollaborationSessionListParams{PrincipalID: credential.Agent.ID})
	if err != nil {
		t.Fatalf("ListCollaborationSessions() error = %v", err)
	}
	states := make(map[string]channels.CollaborationSessionState, len(bindings))
	for _, binding := range bindings {
		states[binding.SessionRef] = binding.State
	}
	if states[threadIDs[0]] != channels.CollaborationSessionInterrupted || states[threadIDs[1]] != channels.CollaborationSessionRunning {
		t.Fatalf("session states after targeted interrupt = %#v", states)
	}
}

func TestInterruptSessionRefusesForeignNamedAgentSession(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
		server.Close()
	})
	caller, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Caller", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent(caller) error = %v", err)
	}
	owner, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Owner", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent(owner) error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, owner.Agent)
	task, err := server.channelService.CreateTaskHuman(context.Background(), channels.TaskCreateParams{
		RoomID: room.ID, HumanID: "human-1", OwnerID: owner.Agent.ID,
		Title: "Keep running", Body: "This session belongs to Owner.",
	})
	if err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), owner.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake() error = %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("owner work session did not start")
	}
	sessionRef := namedAgentWorkSessionID(agentRuntimeFromNamed(owner.Agent), task.ID)
	if !threadIsRunning(server.thread(sessionRef)) {
		t.Fatalf("owner session %q is not running", sessionRef)
	}

	server.InterruptSession(caller.Agent.ID, sessionRef)
	if !threadIsRunning(server.thread(sessionRef)) {
		t.Fatal("foreign agent interrupted the owner's running session")
	}
	binding, err := server.channelService.GetCollaborationSession(
		context.Background(), owner.Agent.ID, owner.Token, sessionRef,
	)
	if err != nil {
		t.Fatalf("GetCollaborationSession() error = %v", err)
	}
	if binding.State != channels.CollaborationSessionRunning {
		t.Fatalf("foreign interrupt changed binding = %#v", binding)
	}

	server.InterruptRunSession(sessionRef)
	if !threadIsRunning(server.thread(sessionRef)) {
		t.Fatal("hidden-run interrupt bypassed Named Agent session ownership")
	}
}

func TestInterruptRunSessionStopsHiddenExecution(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	server := New(rt, &lockedBuffer{})
	t.Cleanup(server.Close)

	thread := newThreadState("hidden-verifier-session", nil, rt.ProviderName, rt.Model, rt.RootDir, true, time.Now().UTC())
	turnCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	thread.mu.Lock()
	thread.running = true
	thread.currentTurn = "hidden-verifier-turn"
	thread.cancel = cancel
	thread.mu.Unlock()
	server.mu.Lock()
	server.threads[thread.ID] = thread
	server.mu.Unlock()

	server.InterruptRunSession(thread.ID)
	select {
	case <-turnCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("hidden run session was not interrupted")
	}
	thread.mu.Lock()
	interrupted := thread.interrupting
	thread.running = false
	thread.cancel = nil
	thread.mu.Unlock()
	if !interrupted {
		t.Fatal("hidden run session did not enter interrupting state")
	}
}

func TestNamedAgentGoalCorrectionStartsReplacementWorkSession(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
		server.Close()
	})
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)
	task, err := server.channelService.CreateTaskHuman(context.Background(), channels.TaskCreateParams{
		RoomID: room.ID, HumanID: "human-1", OwnerID: credential.Agent.ID,
		Title: "Revise while running", Body: "Use the first goal.",
	})
	if err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake() error = %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("initial work session did not start")
	}
	oldSessionRef := namedAgentWorkSessionID(agentRuntimeFromNamed(credential.Agent), task.ID)
	if !threadIsRunning(server.thread(oldSessionRef)) {
		t.Fatalf("initial work session %q is not running", oldSessionRef)
	}
	server.channelService.SetWakeSink(server)
	if _, err := server.channelService.UpdateTaskHuman(context.Background(), channels.TaskUpdateParams{
		TaskID: task.ID, HumanID: "human-1", GoalCorrection: "Use the corrected goal.",
	}); err != nil {
		t.Fatalf("UpdateTask(goal correction) error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	replacementRef := ""
	for time.Now().Before(deadline) {
		agentClient, bindErr := server.channelService.BindAgent(context.Background(), credential.Agent.ID)
		if bindErr == nil {
			bindings, listErr := agentClient.ListCollaborationSessions(context.Background(), channels.CollaborationSessionListParams{
				PrincipalID: credential.Agent.ID,
			})
			if listErr == nil {
				for _, binding := range bindings {
					if binding.WorkID == task.ID && binding.SessionRef != oldSessionRef &&
						binding.State == channels.CollaborationSessionRunning && threadIsRunning(server.thread(binding.SessionRef)) {
						replacementRef = binding.SessionRef
						break
					}
				}
			}
		}
		if replacementRef != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if replacementRef == "" {
		t.Fatal("goal correction did not start a replacement Work session")
	}
	oldBinding, err := server.channelService.GetCollaborationSession(
		context.Background(), credential.Agent.ID, credential.Token, oldSessionRef,
	)
	if err != nil {
		t.Fatalf("GetCollaborationSession(old) error = %v", err)
	}
	if oldBinding.State != channels.CollaborationSessionInterrupted || oldBinding.RunID != "" {
		t.Fatalf("old goal binding = %#v, want interrupted without run", oldBinding)
	}
}

func TestNamedAgentRestoresActiveWorkSessionAfterRestart(t *testing.T) {
	ctx := context.Background()
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)

	service, err := channels.Open(statepath.ChannelsDir(rt.WuuHome), nil)
	if err != nil {
		t.Fatalf("channels.Open() error = %v", err)
	}
	credential, err := service.CreateNamedAgent(ctx, channels.CreateNamedAgentParams{Name: "Alpha", Autostart: true})
	if err != nil {
		service.Close()
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	room, err := service.CreateRoom(ctx, channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Recovery", CreatedBy: "human-1",
		Members: []channels.RoomMember{
			{MemberType: channels.MemberHuman, MemberID: "human-1"},
			{MemberType: channels.MemberAgent, MemberID: credential.Agent.ID},
		},
	})
	if err != nil {
		service.Close()
		t.Fatalf("CreateRoom() error = %v", err)
	}
	task, err := service.CreateTaskHuman(ctx, channels.TaskCreateParams{
		RoomID: room.ID, HumanID: "human-1", OwnerID: credential.Agent.ID,
		Title: "Resume durable work", Body: "Continue this work after restart.",
	})
	if err != nil {
		service.Close()
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	agent := agentRuntimeFromNamed(credential.Agent)
	sessionRef := namedAgentWorkSessionID(agent, task.ID)
	agentClient, err := service.BindAgent(ctx, credential.Agent.ID)
	if err != nil {
		service.Close()
		t.Fatalf("BindAgent() error = %v", err)
	}
	if _, err := agentClient.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{
		SessionRef: sessionRef, RoomID: room.ID, WorkID: task.ID,
		Purpose: channels.CollaborationSessionWork, State: channels.CollaborationSessionIdle,
	}); err != nil {
		service.Close()
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	sessionClient, err := service.BindAgentSession(ctx, credential.Agent.ID, sessionRef)
	if err != nil {
		service.Close()
		t.Fatalf("BindAgentSession() error = %v", err)
	}
	run, err := sessionClient.StartWorkRun(ctx, channels.WorkRunStartParams{WorkID: task.ID, Kind: channels.WorkRunProducer})
	if err != nil {
		service.Close()
		t.Fatalf("StartWorkRun() error = %v", err)
	}
	if _, err := session.CreateWithMetadata(rt.SessionDir, sessionRef, filepath.Dir(credential.Agent.MemoryDir)); err != nil {
		service.Close()
		t.Fatalf("CreateWithMetadata() error = %v", err)
	}
	if _, err := session.SetSource(rt.SessionDir, sessionRef, namedAgentSessionSource+credential.Agent.ID); err != nil {
		service.Close()
		t.Fatalf("SetSource() error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("close setup channels service: %v", err)
	}

	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	if server.startupErr != nil {
		server.Close()
		t.Fatalf("NewWithCredentialStore() startup error = %v", server.startupErr)
	}
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
		server.Close()
	})
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("restored work session did not resume")
	}
	thread := server.thread(sessionRef)
	if thread == nil || !threadIsRunning(thread) {
		t.Fatalf("restored work thread = %#v, want running", thread)
	}
	thread.mu.Lock()
	gotAgentID := thread.NamedAgentID
	gotSessionRef := thread.CollaborationSessionRef
	thread.mu.Unlock()
	if gotAgentID != credential.Agent.ID || gotSessionRef != sessionRef {
		t.Fatalf("restored work scope = agent %q session %q, want agent %q session %q", gotAgentID, gotSessionRef, credential.Agent.ID, sessionRef)
	}
	if principalRef := agentRuntimeSessionID(agent); principalRef == sessionRef || server.thread(principalRef) != nil {
		t.Fatalf("restart resumed work through principal session %q instead of %q", principalRef, sessionRef)
	}
	binding, err := server.channelService.GetCollaborationSession(ctx, credential.Agent.ID, credential.Token, sessionRef)
	if err != nil {
		t.Fatalf("GetCollaborationSession() error = %v", err)
	}
	if binding.RunID != run.ID || binding.State != channels.CollaborationSessionRunning {
		t.Fatalf("restored binding = %#v, want running run %q", binding, run.ID)
	}
}

func TestNamedAgentCoordinationSessionReturnsIdleAfterTurn(t *testing.T) {
	client := newBlockingStreamClient("done")
	close(client.release)
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	recipient, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Recipient", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent(recipient) error = %v", err)
	}
	sender, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Sender"})
	if err != nil {
		t.Fatalf("CreateNamedAgent(sender) error = %v", err)
	}
	room, err := server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "Coordination", CreatedBy: "human-1",
		Members: []channels.RoomMember{
			{MemberType: channels.MemberHuman, MemberID: "human-1"},
			{MemberType: channels.MemberAgent, MemberID: recipient.Agent.ID},
			{MemberType: channels.MemberAgent, MemberID: sender.Agent.ID},
		},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	recipientClient, err := server.channelService.BindAgent(context.Background(), recipient.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(recipient) error = %v", err)
	}
	sessionRef := "coordination-session"
	if _, err := recipientClient.BindCollaborationSession(context.Background(), channels.CollaborationSessionBindParams{
		SessionRef: sessionRef, RoomID: room.ID, Purpose: channels.CollaborationSessionCoordination,
	}); err != nil {
		t.Fatalf("BindCollaborationSession() error = %v", err)
	}
	senderClient, err := server.channelService.BindAgent(context.Background(), sender.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent(sender) error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	if _, err := senderClient.SendCollaboration(context.Background(), channels.CollaborationSendParams{
		RoomID: room.ID, ToAgentID: recipient.Agent.ID, TargetSessionRef: sessionRef,
		Kind: channels.CollaborationControl, Body: "Handle this in the existing coordination session.",
	}); err != nil {
		t.Fatalf("SendCollaboration() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), recipient.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for threadIsRunning(server.thread(sessionRef)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if threadIsRunning(server.thread(sessionRef)) {
		t.Fatal("coordination session turn did not finish")
	}
	// The idle transition is persisted after the turn's running flag clears —
	// the completion path re-enters the named-agent lock and settles the store —
	// so poll for the durable state instead of reading once inside that window.
	deadline = time.Now().Add(5 * time.Second)
	var binding channels.CollaborationSessionBinding
	for {
		binding, err = server.channelService.GetCollaborationSession(
			context.Background(), recipient.Agent.ID, recipient.Token, sessionRef,
		)
		if err != nil {
			t.Fatalf("GetCollaborationSession() error = %v", err)
		}
		if binding.State == channels.CollaborationSessionIdle && binding.RunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completed coordination binding = %#v, want idle", binding)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNamedAgentSequentialWakesPersistDistinctUserTurns(t *testing.T) {
	client := newBlockingStreamClient("done")
	close(client.release)
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	wakeCompleted := make(chan string, 2)
	server.afterNamedAgentWakeCompletionForTest = func(agentID string) {
		wakeCompleted <- agentID
	}
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Alpha", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	room := createAppserverTestRoom(t, server.channelService, credential.Agent)

	for index, body := range []string{"first", "second"} {
		if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
			RoomID: room.ID, HumanID: "human-1", Body: body,
		}); err != nil {
			t.Fatalf("SendHuman(%d) error = %v", index, err)
		}
		if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
			t.Fatalf("deliverNamedAgentWake(%d) error = %v", index, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for threadIsRunning(server.thread(namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID))) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if threadIsRunning(server.thread(namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID))) {
			t.Fatalf("named agent wake %d did not complete", index)
		}
		select {
		case completedAgentID := <-wakeCompleted:
			if completedAgentID != credential.Agent.ID {
				t.Fatalf("completed wake agent = %q, want %q", completedAgentID, credential.Agent.ID)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("named agent wake %d did not finish its completion callback", index)
		}
		completionDeadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(completionDeadline) {
			state, stateErr := server.channelService.WakeState(context.Background(), credential.Agent.ID)
			if stateErr == nil && !state.Outstanding && !state.Pending {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		state, stateErr := server.channelService.WakeState(context.Background(), credential.Agent.ID)
		if stateErr != nil || state.Outstanding || state.Pending {
			t.Fatalf("named agent wake %d was not released after unchecked completion: state %#v, err %v", index, state, stateErr)
		}
	}

	records, err := session.LoadHistoryRecords(rt.SessionDir, namedAgentRoomSessionID(agentRuntimeFromNamed(credential.Agent), room.ID), false)
	if err != nil {
		t.Fatalf("LoadHistoryRecords() error = %v", err)
	}
	var wakeIDs []string
	for _, record := range records {
		if record.Role == "user" && record.Phase == "channel_wake" {
			wakeIDs = append(wakeIDs, record.ClientID)
		}
	}
	if len(wakeIDs) != 2 || wakeIDs[0] == wakeIDs[1] {
		t.Fatalf("persisted named agent wake IDs = %#v, want two unique turns", wakeIDs)
	}
}

func TestRoomMessageWakesVisibleMemberAfterTurnRuntimeRebuild(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	attachNamedAgentTestToolkit(t, rt)
	server := NewWithCredentialStore(rt, &lockedBuffer{}, nil, nil)
	t.Cleanup(server.Close)
	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{
		Name: "Andy", Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	room, err := server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: "General", CreatedBy: "human-1",
		Members: []channels.RoomMember{{MemberType: channels.MemberAgent, MemberID: credential.Agent.ID}},
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	server.channelService.SetWakeSink(nil)
	if _, err := server.channelService.SendHuman(context.Background(), channels.HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "hello room",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if err := server.deliverNamedAgentWake(context.Background(), credential.Agent.ID); err != nil {
		t.Fatalf("deliverNamedAgentWake(room member) error = %v", err)
	}
	select {
	case <-client.started:
		close(client.release)
	case <-time.After(5 * time.Second):
		t.Fatal("room member turn did not start")
	}
}

func TestChannelRoomUpdateAndDeleteRPCs(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)

	var createdAgent ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, ChannelAgentCreateParams{Name: "Alpha"}, &createdAgent)
	var createdRoom ChannelRoomCreateResult
	callChannelRPC(t, server, out, MethodChannelRoomCreate, ChannelRoomCreateParams{
		Name: "Review", AgentIDs: []string{createdAgent.Agent.ID},
	}, &createdRoom)

	var updated ChannelRoomUpdateResult
	updatedName := "  Delivery  "
	callChannelRPC(t, server, out, MethodChannelRoomUpdate, ChannelRoomUpdateParams{
		RoomID: createdRoom.Room.ID,
		Name:   &updatedName,
	}, &updated)
	if updated.Room.ID != createdRoom.Room.ID || updated.Room.Name != "Delivery" || len(updated.Room.Members) != 2 {
		t.Fatalf("updated room = %#v", updated.Room)
	}
	var createdAgentTwo ChannelAgentCreateResult
	callChannelRPC(t, server, out, MethodChannelAgentCreate, ChannelAgentCreateParams{Name: "Beta"}, &createdAgentTwo)
	agentIDs := []string{createdAgent.Agent.ID, createdAgentTwo.Agent.ID}
	callChannelRPC(t, server, out, MethodChannelRoomUpdate, ChannelRoomUpdateParams{
		RoomID:   createdRoom.Room.ID,
		AgentIDs: &agentIDs,
	}, &updated)
	if len(updated.Room.Members) != 3 {
		t.Fatalf("updated room members = %#v, want local human plus two agents", updated.Room.Members)
	}
	loaded, err := server.channelService.GetRoom(context.Background(), createdRoom.Room.ID)
	if err != nil || loaded.Name != "Delivery" {
		t.Fatalf("persisted room = %#v, err = %v", loaded, err)
	}

	var deleted ChannelRoomDeleteResult
	callChannelRPC(t, server, out, MethodChannelRoomDelete, ChannelRoomDeleteParams{RoomID: createdRoom.Room.ID}, &deleted)
	if !deleted.Deleted {
		t.Fatal("delete RPC returned deleted=false")
	}
	if _, err := server.channelService.GetRoom(context.Background(), createdRoom.Room.ID); !errors.Is(err, channels.ErrNotFound) {
		t.Fatalf("GetRoom(deleted) error = %v, want ErrNotFound", err)
	}
	if kept, err := server.channelService.GetNamedAgent(context.Background(), createdAgent.Agent.ID); err != nil || kept.ID != createdAgent.Agent.ID {
		t.Fatalf("named agent after delete RPC = %#v, err = %v", kept, err)
	}
}

func createAppserverTestRoom(t *testing.T, service *channels.Service, agents ...channels.NamedAgent) channels.Room {
	t.Helper()
	if len(agents) != 1 {
		t.Fatalf("direct-message fixture requires one named agent, got %d", len(agents))
	}
	room, err := service.OpenDirectMessage(context.Background(), "human-1", agents[0].ID)
	if err != nil {
		t.Fatalf("OpenDirectMessage() error = %v", err)
	}
	return room
}

func TestChannelRoomListReportsUnreadAndReadRPCClearsIt(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = filepath.Join(t.TempDir(), ".wuu")
	out := &lockedBuffer{}
	server := NewWithCredentialStore(rt, out, nil, nil)
	t.Cleanup(server.Close)

	credential, err := server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Alpha"})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	var createdRoom ChannelRoomCreateResult
	callChannelRPC(t, server, out, MethodChannelRoomCreate, ChannelRoomCreateParams{
		Name: "Unread", AgentIDs: []string{credential.Agent.ID},
	}, &createdRoom)
	if _, err := server.channelService.SendAgent(context.Background(), channels.AgentSendParams{
		RoomID: createdRoom.Room.ID, AgentID: credential.Agent.ID, Token: credential.Token, Body: "new message", BasisSeq: 0,
	}); err != nil {
		t.Fatalf("SendAgent() error = %v", err)
	}

	var listed ChannelRoomListResult
	callChannelRPC(t, server, out, MethodChannelRoomList, nil, &listed)
	if len(listed.Rooms) != 1 || listed.Rooms[0].UnreadCount != 1 {
		t.Fatalf("listed rooms = %#v, want one unread message", listed.Rooms)
	}
	var read ChannelRoomReadResult
	callChannelRPC(t, server, out, MethodChannelRoomRead, ChannelRoomReadParams{RoomID: createdRoom.Room.ID}, &read)
	if !read.Read {
		t.Fatal("room read result is false")
	}
	callChannelRPC(t, server, out, MethodChannelRoomList, nil, &listed)
	if len(listed.Rooms) != 1 || listed.Rooms[0].UnreadCount != 0 {
		t.Fatalf("listed rooms after read = %#v, want zero unread", listed.Rooms)
	}
}

func attachNamedAgentTestToolkit(t *testing.T, rt *runtime.Session) {
	t.Helper()
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatalf("tools.New() error = %v", err)
	}
	rt.Toolkit = kit
	rt.StreamRunner.Tools = kit
}

func callChannelRPC(t *testing.T, server *Server, out *lockedBuffer, method string, params any, target any) {
	t.Helper()
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	requestJSON, err := json.Marshal(Request{ID: json.RawMessage(`1`), Method: method, Params: paramsJSON})
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if err := server.handleLine(context.Background(), requestJSON); err != nil {
		t.Fatalf("%s handler error = %v", method, err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *ResponseError  `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &envelope); err != nil {
		t.Fatalf("decode %s response: %v", method, err)
	}
	if envelope.Error != nil {
		t.Fatalf("%s response error = %#v", method, envelope.Error)
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		t.Fatalf("decode %s result: %v", method, err)
	}
}
