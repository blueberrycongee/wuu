package channels

import (
	"context"
	"testing"
)

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
