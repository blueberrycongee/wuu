package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/channels"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func createPeerRoom(t *testing.T, fixture *collaborationRPCFixture, name string, identities ...channels.NamedAgent) channels.Room {
	t.Helper()
	members := make([]channels.RoomMember, 0, len(identities))
	for _, identity := range identities {
		members = append(members, channels.RoomMember{MemberType: channels.MemberAgent, MemberID: identity.ID})
	}
	room, err := fixture.server.channelService.CreateRoom(context.Background(), channels.CreateRoomParams{
		Kind: channels.RoomChannel, Name: name, CreatedBy: "human-1", Members: members,
	})
	if err != nil {
		t.Fatal(err)
	}
	return room
}

func TestRoomMessageDelegatesAndPublishesThroughVisibleMember(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	fixture.room = createPeerRoom(t, fixture, "Investigation", fixture.identity)
	ctx := context.Background()
	const objective = "Investigate the reconnect defect and report the evidence here"
	if _, err := fixture.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: fixture.room.ID, HumanID: "human-1", Body: objective}); err != nil {
		t.Fatal(err)
	}
	call := provider.next(t)
	if !strings.Contains(collaborationRequestText(call.request), objective) {
		t.Fatal("room input did not reach the member")
	}
	bindings, err := fixture.server.channelService.ListAllCollaborationSessions(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].NamedAgentID != fixture.identity.ID || bindings[0].Purpose != channels.CollaborationSessionConversation {
		t.Fatalf("room input did not create a member conversation: %+v, %v", bindings, err)
	}
	parent, _, childCall := startCollaborationChild(t, fixture, provider, bindings[0], call)
	const evidence = "The experiment found an old socket retained by the retry callback"
	childCall.response <- providers.ChatResponse{Content: evidence}
	fixture.waitForCompletion(t)
	continuation := provider.next(t)
	if !strings.Contains(collaborationRequestText(continuation.request), evidence) {
		t.Fatal("the room member did not receive its child's evidence")
	}
	beforePublish, err := fixture.server.channelService.ListMessages(ctx, fixture.room.ID, 0, 50)
	if err != nil || len(beforePublish) == 0 {
		t.Fatalf("read current room before publishing: %+v, %v", beforePublish, err)
	}
	args, _ := json.Marshal(map[string]any{"room_id": fixture.room.ID, "kind": "text", "body": evidence, "basis_seq": beforePublish[len(beforePublish)-1].Seq})
	continuation.response <- providers.ChatResponse{ToolCalls: []providers.ToolCall{{ID: "publish", Name: "chat_send", Arguments: string(args)}}}
	published := provider.next(t)
	published.response <- providers.ChatResponse{Content: evidence}
	fixture.waitForCompletion(t)
	messages, err := fixture.server.channelService.ListMessages(ctx, fixture.room.ID, 0, 50)
	if err != nil || len(messages) != 3 || messages[2].Body != evidence || messages[2].AuthorID != fixture.identity.ID {
		t.Fatalf("member did not publish its result: %+v, %v", messages, err)
	}
	parent, err = fixture.server.channelService.LookupCollaborationSession(ctx, parent.SessionRef)
	if err != nil || parent.State != channels.CollaborationSessionIdle {
		t.Fatalf("room conversation did not return to idle: %+v, %v", parent, err)
	}
}

func TestRoomMembersStartConcurrentlyWithoutAnExtraModelCall(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	peer, err := fixture.server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Reviewer", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	room := createPeerRoom(t, fixture, "Review", fixture.identity, peer.Agent)
	if _, err := fixture.server.channelService.SendHuman(context.Background(), channels.HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Review this from your own perspective"}); err != nil {
		t.Fatal(err)
	}
	first, second := provider.next(t), provider.next(t)
	bindings, err := fixture.server.channelService.ListAllCollaborationSessions(context.Background())
	if err != nil || len(bindings) != 2 {
		t.Fatalf("expected exactly two member sessions: %+v, %v", bindings, err)
	}
	for _, binding := range bindings {
		if binding.NamedAgentID == "" || binding.RoomID != room.ID || binding.State != channels.CollaborationSessionRunning {
			t.Fatalf("unexpected room session: %+v", binding)
		}
	}
	first.response <- providers.ChatResponse{Content: "First independent observation"}
	second.response <- providers.ChatResponse{Content: "Second independent observation"}
	completed := make(map[string]bool)
	for range 2 {
		select {
		case identity := <-fixture.completed:
			completed[identity] = true
		case <-time.After(collaborationTestWaitTimeout):
			t.Fatal("member completion was not persisted")
		}
	}
	if !completed[fixture.identity.ID] || !completed[peer.Agent.ID] {
		t.Fatalf("member completions = %v", completed)
	}
}

func TestRoomConversationsShareIdentityCapacityAndKeepSeparateHistory(t *testing.T) {
	t.Setenv("WUU_COLLAB_AGENT_RUN_LIMIT", "1")
	fixture, provider := newCollaborationFlowFixture(t)
	fixture.server.channelService.SetWakeSink(nil)
	ctx := context.Background()
	firstRoom := createPeerRoom(t, fixture, "First", fixture.identity)
	secondRoom := createPeerRoom(t, fixture, "Second", fixture.identity)
	for _, room := range []channels.Room{firstRoom, secondRoom} {
		if _, err := fixture.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: room.Name + " private objective"}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.server.deliverNamedAgentWake(ctx, fixture.identity.ID); err != nil {
			t.Fatal(err)
		}
	}
	first := provider.next(t)
	secondRef := namedAgentRoomSessionID(agentRuntimeFromNamed(fixture.identity), secondRoom.ID)
	queued, err := fixture.server.channelService.LookupCollaborationSession(ctx, secondRef)
	if err != nil || queued.State != channels.CollaborationSessionQueued {
		t.Fatalf("room traffic bypassed identity capacity: %+v, %v", queued, err)
	}
	first.response <- providers.ChatResponse{Content: "First room result"}
	fixture.waitForCompletion(t)
	second := provider.next(t)
	text := collaborationRequestText(second.request)
	if !strings.Contains(text, "Second private objective") || strings.Contains(text, "First private objective") || strings.Contains(text, "First room result") {
		t.Fatalf("room conversations shared private history: %s", text)
	}
	second.response <- providers.ChatResponse{Content: "Second room result"}
	fixture.waitForCompletion(t)
}

func TestRoomMemberAcceptsNewInputAfterProviderFailure(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	room := createPeerRoom(t, fixture, "Recovery", fixture.identity)
	ctx := context.Background()
	if _, err := fixture.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "First request"}); err != nil {
		t.Fatal(err)
	}
	provider.next(t).failure <- errors.New("permanent provider rejection")
	fixture.waitForCompletion(t)
	ref := namedAgentRoomSessionID(agentRuntimeFromNamed(fixture.identity), room.ID)
	binding, err := fixture.server.channelService.LookupCollaborationSession(ctx, ref)
	if err != nil || binding.State != channels.CollaborationSessionIdle || binding.FailureReason == "" {
		t.Fatalf("failed request disabled its room entrypoint or lost its error: %+v, %v", binding, err)
	}
	if _, err := fixture.server.channelService.SendHuman(ctx, channels.HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "Try this new request"}); err != nil {
		t.Fatal(err)
	}
	call := provider.next(t)
	if !strings.Contains(collaborationRequestText(call.request), "Try this new request") {
		t.Fatal("new input did not restart the room conversation")
	}
	call.response <- providers.ChatResponse{Content: "Recovered"}
	fixture.waitForCompletion(t)
}

func TestRoomConversationPinsReasoningVariantBeforeAdmission(t *testing.T) {
	fixture := newCollaborationRPCFixture(t)
	rt := fixture.server.rt
	rt.StreamRunner.Variant = "deep"
	cfg := config.Default()
	cfg.DefaultProvider = rt.ProviderName
	cfg.Providers = map[string]config.ProviderConfig{rt.ProviderName: {
		Type: "openai", BaseURL: "http://127.0.0.1:1/v1", APIKey: "test-key", Model: rt.Model,
		Models: map[string]config.ProviderModelConfig{rt.Model: {
			DefaultVariant: "fast",
			Variants:       map[string]map[string]any{"deep": {"reasoningEffort": "high"}, "fast": {"reasoningEffort": "low"}},
		}},
	}}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	agent := agentRuntimeFromNamed(fixture.identity)
	agent.Autostart = false
	client, err := fixture.server.channelService.BindAgent(context.Background(), agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.startNamedAgentConversationLocked(context.Background(), agent, client, fixture.room.ID, false); err != nil {
		t.Fatal(err)
	}
	// Selection changes while the conversation has not been admitted must not
	// alter its persisted model choice when a later host reconstructs it.
	rt.StreamRunner.Variant = "fast"
	ref := namedAgentRoomSessionID(agent, fixture.room.ID)
	for range 2 {
		thread, err := fixture.server.ensureAgentRuntimeSessionThreadLocked(agent, ref)
		if err != nil {
			t.Fatal(err)
		}
		if got := thread.execRuntime.StreamRunner.ProviderOptions["reasoningEffort"]; got != "high" {
			t.Fatalf("room lost its selected reasoning variant: %v", got)
		}
		fixture.server.mu.Lock()
		delete(fixture.server.threads, ref)
		fixture.server.mu.Unlock()
		releaseDetachedThreadRuntime(detachedThreadRuntime{runtime: thread.execRuntime})
		thread.execRuntime = nil
	}
}

func TestWorkResultsReachRoomConversationWithoutStartingProducer(t *testing.T) {
	for _, addressed := range []bool{false, true} {
		t.Run(fmt.Sprintf("addressed=%t", addressed), func(t *testing.T) {
			fixture, provider := newCollaborationFlowFixture(t)
			fixture.server.channelService.SetWakeSink(nil)
			ctx := context.Background()
			room := createPeerRoom(t, fixture, "Results", fixture.identity)
			owner, err := fixture.server.channelService.BindAgent(ctx, fixture.identity.ID)
			if err != nil {
				t.Fatal(err)
			}
			ref := namedAgentRoomSessionID(agentRuntimeFromNamed(fixture.identity), room.ID)
			if _, err := owner.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{SessionRef: ref, RoomID: room.ID, Purpose: channels.CollaborationSessionConversation}); err != nil {
				t.Fatal(err)
			}
			initiator := owner
			if addressed {
				initiator, err = fixture.server.channelService.BindAgentSession(ctx, fixture.identity.ID, ref)
				if err != nil {
					t.Fatal(err)
				}
			}
			task, err := initiator.CreateTask(ctx, channels.TaskCreateParams{RoomID: room.ID, Title: "Related task", OwnerID: fixture.identity.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owner.Check(ctx); err != nil {
				t.Fatal(err)
			}
			run, err := owner.StartWorkRun(ctx, channels.WorkRunStartParams{WorkID: task.ID, Kind: channels.WorkRunProducer})
			if err != nil {
				t.Fatal(err)
			}
			worker, err := fixture.server.channelService.BindAgentSession(ctx, fixture.identity.ID, run.SessionRef)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := worker.Check(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := owner.FinishWorkRun(ctx, channels.WorkRunFinishParams{WorkID: task.ID, RunID: run.ID, State: channels.WorkRunCompleted, Outcome: "Related work result"}); err != nil {
				t.Fatal(err)
			}
			if err := fixture.server.deliverNamedAgentWake(ctx, fixture.identity.ID); err != nil {
				t.Fatal(err)
			}
			call := provider.next(t)
			input := collaborationRequestText(call.request)
			if !strings.Contains(input, "work_run_terminal") || !strings.Contains(input, run.ID) || !strings.Contains(input, task.ID) || !strings.Contains(input, "completed") {
				t.Fatal("work result did not reach the conversation")
			}
			binding, err := fixture.server.channelService.LookupCollaborationSession(ctx, ref)
			if err != nil || binding.WorkID != "" || binding.RunID != "" {
				t.Fatalf("result changed the conversation execution scope: %+v, %v", binding, err)
			}
			work, err := owner.GetWork(ctx, task.ID)
			if err != nil || len(work.Runs) != 1 || work.Runs[0].State != channels.WorkRunCompleted {
				t.Fatalf("result started another producer: %+v, %v", work, err)
			}
			call.response <- providers.ChatResponse{Content: "Result received"}
			fixture.waitForCompletion(t)
		})
	}
}
