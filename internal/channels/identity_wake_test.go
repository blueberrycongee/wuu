package channels

import (
	"context"
	"fmt"
	"testing"
)

func TestIdentityPrioritizesHumanRoomInputAfterRestart(t *testing.T) {
	ctx := context.Background()
	service, _, agent, room, task, result := prepareChildResultParent(t)
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{
		SessionRef: result.ParentSessionRef, TurnID: result.ParentTurnID, State: CollaborationSessionIdle,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnqueueSessionResult(ctx, result); err != nil {
		t.Fatal(err)
	}
	other := createTestRoom(t, service, agent)
	if _, err := service.SendHuman(ctx, HumanSendParams{RoomID: other.ID, HumanID: "human-1", Body: "Review the updated requirement first"}); err != nil {
		t.Fatal(err)
	}
	dir := service.dir
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	_, next := prepareIdentityTestTurn(t, service, agent.Agent.ID)
	if next.RoomID != other.ID || next.WorkID != "" {
		t.Fatalf("background result delayed human room input: %+v", next)
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{
		SessionRef: next.SessionRef, TurnID: "human-followup", State: CollaborationSessionIdle,
	}); err != nil {
		t.Fatal(err)
	}
	_, resumed := prepareIdentityTestTurn(t, service, agent.Agent.ID)
	if resumed.RoomID != room.ID || resumed.WorkID != task.ID || resumed.SessionRef != next.SessionRef {
		t.Fatalf("human input lost the earlier responsibility or changed identity: %+v", resumed)
	}
}

func TestSessionInboxKeepsHumanCorrectionsAheadOfResultBacklog(t *testing.T) {
	for _, receive := range []bool{true, false} {
		t.Run(fmt.Sprintf("receive=%t", receive), func(t *testing.T) {
			ctx := context.Background()
			service, client, _, _, _, result := prepareChildResultParent(t)
			var expected []string
			for i := 0; i < checkLimit+1; i++ {
				result.RequestID = fmt.Sprintf("worker-result-%d", i)
				delivery, err := service.EnqueueSessionResult(ctx, result)
				if err != nil {
					t.Fatal(err)
				}
				expected = append(expected, delivery.Message.ID)
			}
			var corrections []string
			for _, body := range []string{"Keep the existing data", "Also preserve the export format"} {
				delivery, err := service.EnqueueSessionInput(ctx, CollaborationSessionSendParams{
					SessionRef: result.ParentSessionRef, Body: body, RequestID: body,
				})
				if err != nil {
					t.Fatal(err)
				}
				corrections = append(corrections, delivery.ID)
			}
			expected = append(corrections, expected...)
			var got []string
			for len(got) < len(expected) {
				var messages []CollaborationMessage
				if receive {
					var err error
					messages, err = client.ReceiveCollaboration(ctx, 32)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					check, err := client.Check(ctx)
					if err != nil {
						t.Fatal(err)
					}
					messages = check.Collaboration
					if check.HasMore != (len(got)+len(messages) < len(expected)) {
						t.Fatalf("backlog pagination lost pending results: %+v", check)
					}
				}
				if len(messages) == 0 {
					t.Fatal("pending messages were lost")
				}
				var ids []string
				for _, message := range messages {
					ids = append(ids, message.ID)
					got = append(got, message.ID)
				}
				if receive {
					if err := client.AcknowledgeCollaboration(ctx, ids); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(got) != len(expected) {
				t.Fatalf("deliveries duplicated: got %d, want %d", len(got), len(expected))
			}
			for i := range expected {
				if got[i] != expected[i] {
					t.Fatalf("delivery %d = %s, want %s; corrections must lead without reordering or losing results", i, got[i], expected[i])
				}
			}
		})
	}
}

func TestIdentityTaskUpdateDoesNotRepeatConsumedAssignment(t *testing.T) {
	for _, recovery := range []string{"check", "restart"} {
		t.Run(recovery, func(t *testing.T) {
			ctx := context.Background()
			service, _, agent, _, task := newIdentityTaskFixture(t)
			owner, binding := prepareIdentityTestTurn(t, service, agent.Agent.ID)
			// Updating the task after receiving its assignment reopens the room
			// notification even though there is no new execution input.
			if _, err := owner.UpdateTask(ctx, TaskUpdateParams{TaskID: task.ID, State: TaskStateDoing}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{
				SessionRef: binding.SessionRef, TurnID: "task-turn", State: CollaborationSessionIdle,
			}); err != nil {
				t.Fatal(err)
			}
			if recovery == "restart" {
				dir := service.dir
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				service, err = Open(dir, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = service.Close() })
			} else {
				check, err := owner.Check(ctx)
				if err != nil || len(check.Items) != 0 || len(check.Collaboration) != 0 {
					t.Fatalf("check after task turn = %+v, %v", check, err)
				}
				if followup, err := service.FinishWakeAttempt(ctx, agent.Agent.ID); err != nil || followup {
					t.Fatalf("empty check retained a wake: %v, %v", followup, err)
				}
			}
			runtime, err := service.GetAgentRuntime(ctx, agent.Agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if next, ready, err := service.PrepareIdentityConversation(ctx, runtime, CollaborationSessionBindParams{}); err != nil || ready {
					t.Fatalf("consumed task started an empty turn: %+v, %v, %v", next, ready, err)
				}
				if followup, err := service.FinishWakeAttempt(ctx, agent.Agent.ID); err != nil || followup {
					t.Fatalf("consumed task retained a wake: %v, %v", followup, err)
				}
			}
			work, err := service.GetWork(ctx, task.ID)
			if err != nil || work.State != WorkWorking {
				t.Fatalf("retiring a notification changed task progress: %+v, %v", work, err)
			}
			// A fresh assignment in another room must still wake the same identity.
			room := createTestRoom(t, service, agent)
			client, err := service.BindAgent(ctx, agent.Agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			nextTask, err := client.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "New request", OwnerID: agent.Agent.ID})
			if err != nil {
				t.Fatal(err)
			}
			_, next := prepareIdentityTestTurn(t, service, agent.Agent.ID)
			if next.RoomID != room.ID || next.WorkID != nextTask.ID {
				t.Fatalf("fresh assignment lost its room/work scope: %+v", next)
			}
		})
	}
}
