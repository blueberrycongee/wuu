package goal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type testHost struct {
	mu         sync.Mutex
	stored     *string
	sends      []pluginapi.SessionSendParams
	turns      map[string]*pluginapi.SessionTurnInspection
	cancels    []pluginapi.SessionCancelParams
	failSave   bool
	sendError  bool
	queued     bool
	cancelRace bool
}

func (h *testHost) InitializeParams() pluginapi.InitializeParams { return pluginapi.InitializeParams{} }
func (h *testHost) CallHost(_ context.Context, method string, params, out any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if method != pluginapi.HostServiceCallMethod {
		return fmt.Errorf("unexpected gateway %s", method)
	}
	data, _ := json.Marshal(params)
	var call struct {
		Service string
		Params  json.RawMessage
	}
	if err := json.Unmarshal(data, &call); err != nil {
		return err
	}
	var value any
	switch call.Service {
	case pluginapi.HostServiceStorageGet:
		value = pluginapi.StorageGetResult{Value: h.stored}
	case pluginapi.HostServiceStorageSet:
		if h.failSave {
			return errors.New("disk full")
		}
		var p pluginapi.StorageSetParams
		_ = json.Unmarshal(call.Params, &p)
		h.stored = &p.Value
		value = struct{}{}
	case pluginapi.HostServiceSessionSend:
		var p pluginapi.SessionSendParams
		_ = json.Unmarshal(call.Params, &p)
		h.sends = append(h.sends, p)
		if h.turns == nil {
			h.turns = map[string]*pluginapi.SessionTurnInspection{}
		}
		turn := &pluginapi.SessionTurnInspection{RequestID: p.RequestID, State: "running", TurnID: fmt.Sprintf("auto-%d", len(h.sends))}
		if h.queued {
			turn.State = "queued"
			turn.QueueID = turn.TurnID
			turn.TurnID = ""
		}
		h.turns[p.RequestID] = turn
		if h.sendError {
			return errors.New("response lost after acceptance")
		}
		value = pluginapi.SessionSendResult{SessionID: p.SessionID, State: turn.State, TurnID: turn.TurnID, QueueID: turn.QueueID}
	case pluginapi.HostServiceSessionInspect:
		var p pluginapi.SessionInspectParams
		_ = json.Unmarshal(call.Params, &p)
		value = pluginapi.SessionInspectResult{Turn: h.turns[p.RequestID]}
	case pluginapi.HostServiceSessionCancel:
		var p pluginapi.SessionCancelParams
		_ = json.Unmarshal(call.Params, &p)
		if p.TurnID != "" && p.QueueID != "" {
			return errors.New("exclusive identities")
		}
		h.cancels = append(h.cancels, p)
		if h.cancelRace {
			h.cancelRace = false
			for _, turn := range h.turns {
				if turn.QueueID == p.QueueID {
					turn.State = "running"
					turn.TurnID = "dequeued-race"
					turn.QueueID = ""
				}
			}
			data, _ := json.Marshal(pluginapi.SessionCancelResult{Cancelled: false})
			return json.Unmarshal(data, out)
		}
		for _, turn := range h.turns {
			if (p.TurnID != "" && turn.TurnID == p.TurnID) || (p.QueueID != "" && turn.QueueID == p.QueueID) {
				turn.State = "interrupted"
			}
		}
		value = pluginapi.SessionCancelResult{Cancelled: true}
	default:
		return fmt.Errorf("unexpected service %s", call.Service)
	}
	data, _ = json.Marshal(value)
	return json.Unmarshal(data, out)
}

func setup(t *testing.T) (*controller, *testHost) {
	t.Helper()
	h := &testHost{}
	c := &controller{now: func() time.Time { return time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC) }}
	if err := c.initialize(context.Background(), h, pluginapi.InitializeParams{}); err != nil {
		t.Fatal(err)
	}
	return c, h
}
func tool(t *testing.T, c *controller, id, args string) pluginapi.ToolResult {
	t.Helper()
	result, err := c.executeTool(context.Background(), c.host, pluginapi.ToolCall{ToolID: id, SessionID: "thread", TurnID: "initial", Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func event(t *testing.T, c *controller, capability string, input any) {
	t.Helper()
	raw, _ := json.Marshal(input)
	if _, err := c.invokeCapability(context.Background(), c.host, pluginapi.CapabilityCall{Capability: capability, Input: raw}); err != nil {
		t.Fatal(err)
	}
}
func finish(t *testing.T, c *controller, id string, tokens int64) {
	t.Helper()
	event(t, c, completedCapability, completedTurn{ThreadID: "thread", TurnID: id, StartedAt: c.now(), CompletedAt: c.now().Add(time.Second), Succeeded: true, InputTokens: tokens})
}
func client(t *testing.T, c *controller, method string, args mutation) {
	t.Helper()
	raw, _ := json.Marshal(args)
	event(t, c, clientCapability, map[string]any{"method": method, "input": json.RawMessage(raw)})
}

func TestGoalContinuesOnceAndPreservesUsageAcrossReload(t *testing.T) {
	c, h := setup(t)
	tool(t, c, "create_goal", `{"objective":"finish the work"}`)
	finish(t, c, "initial", 40)
	finish(t, c, "initial", 40)
	if len(h.sends) != 1 || c.goals["thread"].TokensUsed != 40 {
		t.Fatalf("duplicate event: %+v", c.goals["thread"])
	}
	finish(t, c, "auto-1", 65)
	g := c.goals["thread"]
	if g.Status != "active" || g.TokensUsed != 105 || len(h.sends) != 2 {
		t.Fatalf("continuation did not settle usage: %+v", g)
	}
	if !strings.Contains(h.sends[0].Input.Prompt, "finish the work") {
		t.Fatal("objective absent from continuation")
	}
	// A fresh controller reads the same accounting without restarting work.
	restored := &controller{now: c.now}
	if err := restored.initialize(context.Background(), h, pluginapi.InitializeParams{}); err != nil {
		t.Fatal(err)
	}
	if err := restored.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restored.goals["thread"].TokensUsed != 105 || restored.goals["thread"].Status != "paused" || len(h.sends) != 2 {
		t.Fatal("recovery changed usage or sent another turn")
	}
}

func TestCompletionDoesNotCancelExecutingToolAndSettlesFinalTurn(t *testing.T) {
	c, h := setup(t)
	tool(t, c, "create_goal", `{"objective":"work"}`)
	finish(t, c, "initial", 30)
	tool(t, c, "update_goal", `{"status":"complete"}`)
	if len(h.cancels) != 0 {
		t.Fatal("completion cancelled its own turn")
	}
	finish(t, c, "auto-1", 20)
	if c.goals["thread"].TokensUsed != 50 || len(h.sends) != 1 || c.goals["thread"].Status != "complete" {
		t.Fatalf("final settlement: %+v", c.goals["thread"])
	}
}

func TestLegacyBudgetLimitedGoalLoadsPausedAndResumes(t *testing.T) {
	c, h := setup(t)
	stored := `{"thread":{"id":"legacy","thread_id":"thread","objective":"finish work","status":"budget_limited","token_budget":100,"tokens_used":105}}`
	h.stored = &stored
	if err := c.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.goals["thread"].Status != "paused" || c.goals["thread"].TokensUsed != 105 || len(h.sends) != 0 {
		t.Fatalf("legacy goal changed usage or resumed automatically: %+v", c.goals["thread"])
	}
	client(t, c, "resume", mutation{ThreadID: "thread"})
	if c.goals["thread"].Status != "active" || len(h.sends) != 1 {
		t.Fatalf("legacy goal cannot resume: %+v", c.goals["thread"])
	}
}

func TestGoalCreatedAndCompletedInSameTurnSettlesUsage(t *testing.T) {
	c, h := setup(t)
	created := c.now()
	tool(t, c, "create_goal", `{"objective":"verify goal tools"}`)
	c.now = func() time.Time { return created.Add(10 * time.Second) }
	tool(t, c, "update_goal", `{"status":"complete"}`)
	observation := completedTurn{
		ThreadID: "thread", TurnID: "initial", StartedAt: created.Add(-time.Minute),
		CompletedAt: created.Add(20 * time.Second), Succeeded: true,
		InputTokens: 3000, OutputTokens: 1014,
	}
	c.now = func() time.Time { return observation.CompletedAt }
	event(t, c, completedCapability, observation)
	event(t, c, completedCapability, observation)
	var saved map[string]Goal
	if err := json.Unmarshal([]byte(*h.stored), &saved); err != nil {
		t.Fatal(err)
	}
	g := saved["thread"]
	if g.TokensUsed != 4014 || g.TimeUsedSeconds != 20 || g.Status != "complete" || len(h.sends) != 0 {
		t.Fatalf("same-turn completion settlement: %+v, sends=%d", g, len(h.sends))
	}
}

func TestUserPauseCancelsQueuedContinuationAndResumeIgnoresOldEvents(t *testing.T) {
	c, h := setup(t)
	h.queued = true
	tool(t, c, "create_goal", `{"objective":"work"}`)
	finish(t, c, "initial", 10)
	client(t, c, "pause", mutation{ThreadID: "thread"})
	if len(h.cancels) != 1 || h.cancels[0].QueueID != "auto-1" {
		t.Fatalf("queue not cancelled: %+v", h.cancels)
	}
	oldTime := c.now()
	c.now = func() time.Time { return oldTime.Add(time.Minute) }
	client(t, c, "resume", mutation{ThreadID: "thread"})
	event(t, c, completedCapability, completedTurn{ThreadID: "thread", TurnID: "late-old", StartedAt: oldTime, CompletedAt: oldTime.Add(time.Second), Succeeded: true, InputTokens: 99})
	event(t, c, pluginapi.CapabilityAgentTurnInterrupted, pluginapi.AgentTurnInterruptedInput{ThreadID: "thread", TurnID: "initial"})
	if len(h.sends) != 2 || c.goals["thread"].TokensUsed != 10 || c.goals["thread"].Status != "active" {
		t.Fatalf("stale event changed resumed goal: %+v", c.goals["thread"])
	}
}

func TestLifecycleAndCompletedDeliveryOrderDoesNotDuplicateWork(t *testing.T) {
	for _, lifecycleFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(lifecycleFirst), func(t *testing.T) {
			c, h := setup(t)
			h.queued = true
			tool(t, c, "create_goal", `{"objective":"work"}`)
			finish(t, c, "initial", 10)
			request := h.sends[0].RequestID
			h.turns[request] = &pluginapi.SessionTurnInspection{RequestID: request, State: "completed", TurnID: "dequeued"}
			lifecycle := func() {
				event(t, c, pluginapi.CapabilityAgentTurnLifecycle, pluginapi.TurnLifecycleInput{RequestID: request, ThreadID: "thread", TurnID: "dequeued", State: "completed", StartedAt: c.now().Format(time.RFC3339Nano), CompletedAt: c.now().Add(time.Second).Format(time.RFC3339Nano), InputTokens: 20})
			}
			if lifecycleFirst {
				lifecycle()
				finish(t, c, "dequeued", 20)
			} else {
				finish(t, c, "dequeued", 20)
				lifecycle()
			}
			if len(h.sends) != 2 || c.goals["thread"].TokensUsed != 30 {
				t.Fatalf("delivery order: %+v sends=%d", c.goals["thread"], len(h.sends))
			}
		})
	}
}

func TestFailedPersistenceDoesNotPublishOrSend(t *testing.T) {
	c, h := setup(t)
	tool(t, c, "create_goal", `{"objective":"work"}`)
	h.failSave = true
	err := c.completed(context.Background(), completedTurn{ThreadID: "thread", TurnID: "initial", StartedAt: c.now(), CompletedAt: c.now(), Succeeded: true, InputTokens: 20})
	if err == nil || c.goals["thread"].TokensUsed != 0 || len(h.sends) != 0 || c.goals["thread"].Settled["initial"] {
		t.Fatal("failed write changed live state")
	}
	h.failSave = false
	finish(t, c, "initial", 20)
	if len(h.sends) != 1 {
		t.Fatal("retry did not continue")
	}
}

func TestLostSendResponseIsCancelledBeforeResume(t *testing.T) {
	c, h := setup(t)
	tool(t, c, "create_goal", `{"objective":"work"}`)
	h.sendError = true
	if err := c.completed(context.Background(), completedTurn{ThreadID: "thread", TurnID: "initial", StartedAt: c.now(), CompletedAt: c.now(), Succeeded: true}); err == nil {
		t.Fatal("lost response succeeded")
	}
	if c.goals["thread"].Status != "paused" || c.goals["thread"].Pending == nil {
		t.Fatal("lost accepted request identity")
	}
	h.sendError = false
	client(t, c, "resume", mutation{ThreadID: "thread"})
	if len(h.cancels) != 1 || h.cancels[0].TurnID != "auto-1" || len(h.sends) != 2 {
		t.Fatal("resume duplicated uncertain work")
	}
}

func TestRestartAndDisableStopContinuation(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprint(restart), func(t *testing.T) {
			c, h := setup(t)
			tool(t, c, "create_goal", `{"objective":"work"}`)
			finish(t, c, "initial", 5)
			if restart {
				next := &controller{now: c.now}
				if err := next.initialize(context.Background(), h, pluginapi.InitializeParams{}); err != nil {
					t.Fatal(err)
				}
				c = next
				if err := c.activate(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := c.shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			expectedCancels := 1
			if restart {
				expectedCancels = 0
			}
			if c.goals["thread"].Status != "paused" || len(h.cancels) != expectedCancels || len(h.sends) != 1 {
				t.Fatal("reload/disable left goal running")
			}
		})
	}
}

func TestGoalToolIsolationAndValidation(t *testing.T) {
	c, _ := setup(t)
	tool(t, c, "create_goal", `{"objective":"work"}`)
	for _, call := range []pluginapi.ToolCall{
		{SessionID: "thread", ToolID: "create_goal", Arguments: json.RawMessage(`{"objective":"replacement"}`)},
		{SessionID: "thread", ToolID: "update_goal", Arguments: json.RawMessage(`{"status":"paused"}`)},
		{SessionID: "thread", ToolID: "get_goal", Arguments: json.RawMessage(`{"thread_id":"another"}`)},
		{SessionID: "fresh-thread", ToolID: "create_goal", Arguments: json.RawMessage(`{"objective":""}`)},
	} {
		if _, err := c.executeTool(context.Background(), c.host, call); err == nil {
			t.Fatalf("accepted invalid operation: %+v", call)
		}
	}
	result, err := c.executeTool(context.Background(), c.host, pluginapi.ToolCall{ToolID: "get_goal", SessionID: "another", Arguments: json.RawMessage(`{}`)})
	if err != nil || string(result.StructuredContent) != `{"goal":null}` {
		t.Fatalf("session leaked: %+v %v", result, err)
	}
}

func TestPauseReconcilesDequeueRaceAndLateInterruption(t *testing.T) {
	c, h := setup(t)
	h.queued = true
	tool(t, c, "create_goal", `{"objective":"work"}`)
	finish(t, c, "initial", 10)
	h.cancelRace = true
	client(t, c, "pause", mutation{ThreadID: "thread"})
	if len(h.cancels) != 2 || h.cancels[1].TurnID != "dequeued-race" {
		t.Fatalf("dequeued turn escaped pause: %+v", h.cancels)
	}
	client(t, c, "resume", mutation{ThreadID: "thread"})
	event(t, c, pluginapi.CapabilityAgentTurnInterrupted, pluginapi.AgentTurnInterruptedInput{ThreadID: "thread", TurnID: "dequeued-race"})
	if c.goals["thread"].Status != "active" || len(h.sends) != 2 {
		t.Fatal("old cancellation interrupted resumed goal")
	}
}

func TestClearSupersedesActiveContextAndAllowsFreshGoal(t *testing.T) {
	c, h := setup(t)
	tool(t, c, "create_goal", `{"objective":"old objective"}`)
	client(t, c, "clear", mutation{ThreadID: "thread"})
	if got := tool(t, c, "get_goal", `{}`); string(got.StructuredContent) != `{"goal":null}` {
		t.Fatalf("cleared goal remains visible: %s", got.StructuredContent)
	}
	raw, err := c.invokeCapability(context.Background(), h, pluginapi.CapabilityCall{Capability: pluginapi.CapabilityAgentPreStep, Input: json.RawMessage(`{"session_id":"thread"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var context pluginapi.AgentPreStepOutput
	_ = json.Unmarshal(raw, &context)
	if len(context.AppendMessages) != 1 || !strings.Contains(context.AppendMessages[0].Content, "inactive") || strings.Contains(context.AppendMessages[0].Content, "old objective") {
		t.Fatalf("stale active context survives clear: %s", raw)
	}
	finish(t, c, "initial", 99)
	if len(h.sends) != 0 {
		t.Fatal("cleared goal continued")
	}
	tool(t, c, "create_goal", `{"objective":"new objective"}`)
	if c.goals["thread"].Objective != "new objective" {
		t.Fatal("clear did not allow a fresh goal")
	}
}
