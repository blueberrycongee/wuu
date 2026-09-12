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

func TestRoomMembersStartConcurrentlyWithoutAnExtraModelCall(t *testing.T) {
	fixture, provider := newCollaborationFlowFixture(t)
	peer, err := fixture.server.channelService.CreateNamedAgent(context.Background(), channels.CreateNamedAgentParams{Name: "Reviewer", Autostart: true})
	if err != nil {
		t.Fatal(err)
	}
	room := createPeerRoom(t, fixture, "Review", fixture.identity, peer.Agent)
	if _, err := fixture.server.channelService.SendHuman(context.Background(), channels.HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@all Review this from your own perspective"}); err != nil {
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
			if err != nil || !binding.Primary || binding.WorkID != task.ID || binding.RunID != "" {
				t.Fatalf("result lost its task scope or started another producer: %+v, %v", binding, err)
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
