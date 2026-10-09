package claudeengine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Exercise the child-process boundary: an initial result must not close stdin
// while a launched agent still owns a completion and an automatic follow-up.
func TestEngineBackgroundAgentContinuation(t *testing.T) {
	binary := buildFakeClaude(t)
	for _, scenario := range []string{"normal", "receipt_only", "terminal_first", "parallel", "streamed_wake", "already_consumed", "wake_error", "crash", "cancel", "shell", "foreground", "foreground_without_tool_id", "queued_input"} {
		t.Run(scenario, func(t *testing.T) {
			engine := NewEngine(binary, t.TempDir())
			sess, err := engine.Open(context.Background(), agentengine.OpenRequest{ThreadID: "background-test"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var visible strings.Builder
			var doneEvents int
			states := map[string]providers.AgentActivityState{}
			result, err := sess.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "background_" + scenario}}}, func(ev providers.StreamEvent) {
				switch ev.Type {
				case providers.EventContentDelta:
					visible.WriteString(ev.Content)
				case providers.EventContentReplace:
					visible.Reset()
					visible.WriteString(ev.Content)
				case providers.EventDone:
					doneEvents++
				case providers.EventAgentActivity:
					states[ev.AgentActivity.ID] = ev.AgentActivity.State
					if scenario == "cancel" && ev.AgentActivity.Label == "Background waiting" {
						cancel()
					}
				}
			})
			if scenario == "wake_error" || scenario == "crash" || scenario == "cancel" {
				if err == nil || doneEvents != 0 {
					t.Fatalf("background failure returned success: result=%+v err=%v done=%d", result, err, doneEvents)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "Launched.\n\nFinal summary."
			if scenario == "parallel" {
				want = "Launched.\n\nFirst summary.\n\nFinal summary."
			}
			if scenario == "shell" || strings.HasPrefix(scenario, "foreground") || scenario == "already_consumed" {
				want = "Launched."
			}
			if scenario == "queued_input" {
				want = "Final summary."
			}
			if result.Result.Content != want || visible.String() != want || doneEvents != 1 {
				t.Fatalf("result=%q visible=%q done=%d; want %q and one terminal", result.Result.Content, visible.String(), doneEvents, want)
			}
			if strings.Contains(visible.String(), "CHILD") {
				t.Fatal("child text leaked into parent")
			}
			for id, state := range states {
				if state == providers.AgentActivityRunning {
					t.Fatalf("agent %s still running after successful turn", id)
				}
			}
			if scenario != "shell" && scenario != "queued_input" && states["spawn-1"] != providers.AgentActivityCompleted {
				t.Fatalf("spawn state = %q, want completed", states["spawn-1"])
			}
			if scenario != "shell" && !strings.HasPrefix(scenario, "foreground") && result.Result.OutputTokens != 30 {
				t.Fatalf("final native usage lost: %+v", result.Result)
			}
		})
	}
}

func TestAgentNotificationStatusAndScope(t *testing.T) {
	var activities []providers.AgentActivity
	sub := newTurnSubscription(func(ev providers.StreamEvent) {
		if ev.AgentActivity != nil {
			activities = append(activities, *ev.AgentActivity)
		}
	}, make(chan turnOutcome, 1))
	for _, line := range []string{
		`{"type":"system","subtype":"task_started","task_id":"nested","parent_tool_use_id":"child","tool_use_id":"nested-spawn","task_type":"local_agent"}`,
		`{"type":"system","subtype":"task_started","task_id":"bash","tool_use_id":"shell"}`,
		`{"type":"system","subtype":"task_started","task_id":"agent","tool_use_id":"spawn","task_type":"local_agent"}`,
		`{"type":"system","subtype":"task_notification","task_id":"agent","status":"unknown"}`,
		`{"type":"system","subtype":"task_notification","task_id":"agent","status":"stopped"}`,
	} {
		sub.handleLine(line)
	}
	if len(activities) != 2 || activities[0].ID != "spawn" || string(activities[1].State) != "stopped" {
		t.Fatalf("incorrect scope or terminal state: %+v", activities)
	}
}

func TestAgentTerminalNotificationCannotBeReopenedByLateStart(t *testing.T) {
	var activities []providers.AgentActivity
	sub := newTurnSubscription(func(ev providers.StreamEvent) {
		if ev.AgentActivity != nil {
			activities = append(activities, *ev.AgentActivity)
		}
	}, make(chan turnOutcome, 1))
	for _, line := range []string{
		`{"type":"system","subtype":"task_started","task_id":"agent","tool_use_id":"spawn","task_type":"local_agent"}`,
		`{"type":"system","subtype":"task_notification","task_id":"agent","status":"completed"}`,
		`{"type":"system","subtype":"task_started","task_id":"agent","tool_use_id":"spawn","task_type":"local_agent"}`,
		`{"type":"system","subtype":"task_progress","task_id":"agent"}`,
	} {
		sub.handleLine(line)
	}
	if len(activities) == 0 || activities[len(activities)-1].State != providers.AgentActivityCompleted {
		t.Fatalf("late task event reopened terminal agent: %+v", activities)
	}
}
