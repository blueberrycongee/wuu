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
	for _, scenario := range []string{"normal", "coalesced_receipts", "coalesced_receipts_unattributed", "zero_turn_user_result", "zero_turn_wake_error", "discarded_input", "receipt_only", "terminal_first", "parallel", "streamed_wake", "already_consumed", "wake_error", "crash", "cancel", "shell", "foreground", "foreground_without_tool_id", "queued_input"} {
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
			if scenario == "zero_turn_wake_error" || scenario == "wake_error" || scenario == "crash" || scenario == "cancel" || scenario == "discarded_input" {
				if scenario == "discarded_input" && (err == nil || !strings.Contains(err.Error(), "discarded")) {
					t.Fatalf("discarded input did not fail promptly: %v", err)
				}
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
			if scenario == "zero_turn_user_result" {
				want = ""
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
			if scenario != "zero_turn_user_result" && scenario != "shell" && scenario != "queued_input" && states["spawn-1"] != providers.AgentActivityCompleted {
				t.Fatalf("spawn state = %q, want completed", states["spawn-1"])
			}
			wantTokens := 60
			if scenario == "parallel" {
				wantTokens = 90
			}
			if scenario == "already_consumed" || scenario == "queued_input" {
				wantTokens = 30
			}
			if scenario != "zero_turn_user_result" && scenario != "shell" && !strings.HasPrefix(scenario, "foreground") && (result.Result.OutputTokens != wantTokens || result.Result.InputTokens != wantTokens*3 || result.Result.CacheCreationTokens != wantTokens/3 || result.Result.CacheReadTokens != wantTokens*2) {
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

func TestAgentResumedTaskWaitsForCurrentRun(t *testing.T) {
	for _, scenario := range []string{"legacy_tool_id", "run_id", "run_id_same_tool", "run_id_terminal_only"} {
		t.Run(scenario, func(t *testing.T) {
			withRunID := scenario != "legacy_tool_id"
			resumeTool := "resume"
			if scenario == "run_id_same_tool" {
				resumeTool = "spawn"
			}
			done := make(chan turnOutcome, 1)
			sub := newTurnSubscription(nil, done)
			taskEvent := func(kind, toolID, runID string) {
				frame := map[string]any{"type": "system", "subtype": kind, "task_id": "agent", "tool_use_id": toolID, "task_type": "local_agent", "status": "completed"}
				if withRunID {
					frame["run_id"] = runID
				}
				if scenario == "run_id_terminal_only" && kind == "task_notification" {
					delete(frame, "tool_use_id")
				}
				sub.handleLine(marshalLine(frame))
			}
			result := func(text string) { sub.handleLine(marshalLine(map[string]any{"type": "result", "result": text})) }
			taskEvent("task_started", "spawn", "run-1")
			result("Launched.")
			taskEvent("task_notification", "spawn", "run-1")
			sub.handleLine(`{"type":"stream_event","event":{"type":"message_start"}}`)
			taskEvent("task_started", resumeTool, "run-2")
			result("Resumed.")
			select {
			case <-done:
				t.Fatal("new run ended at its launch result")
			default:
			}
			taskEvent("task_started", "spawn", "run-1")
			taskEvent("task_notification", "spawn", "run-1")
			result("Still waiting.")
			select {
			case <-done:
				t.Fatal("stale run events ended the current run")
			default:
			}
			taskEvent("task_notification", resumeTool, "run-2")
			result("Final.")
			select {
			case out := <-done:
				if out.err != nil || !strings.HasSuffix(out.result.Result.Content, "Final.") {
					t.Fatalf("bad completion: %+v", out)
				}
			default:
				t.Fatal("current run did not finish")
			}
		})
	}
}

func TestAgentResumedTaskTerminalBeforeLaunchReceipt(t *testing.T) {
	done := make(chan turnOutcome, 1)
	sub := newTurnSubscription(nil, done)
	for _, line := range []string{
		`{"type":"system","subtype":"task_started","task_id":"agent","tool_use_id":"spawn","task_type":"local_agent","run_id":"old"}`,
		`{"type":"result","result":"Launched."}`,
		`{"type":"system","subtype":"task_notification","task_id":"agent","tool_use_id":"spawn","status":"completed","run_id":"old"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"resume","name":"Agent","input":{"resume":"agent"}}]}}`,
		`{"type":"system","subtype":"task_notification","task_id":"agent","tool_use_id":"resume","status":"completed","run_id":"new"}`,
		`{"type":"user","tool_use_result":{"agentId":"agent","isAsync":true},"message":{"content":[{"type":"tool_result","tool_use_id":"resume","content":"Accepted"}]}}`,
		`{"type":"system","subtype":"task_started","task_id":"agent","tool_use_id":"resume","task_type":"local_agent","run_id":"new"}`,
		`{"type":"result","result":"Resumed."}`,
		`{"type":"result","result":"Final.","origin":{"kind":"task-notification"}}`,
	} {
		sub.handleLine(line)
	}
	select {
	case out := <-done:
		if out.err != nil || !strings.HasSuffix(out.result.Result.Content, "Final.") {
			t.Fatalf("bad completion: %+v", out)
		}
	default:
		t.Fatal("early new-run terminal was lost")
	}
}
