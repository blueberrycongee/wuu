package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type testHost struct {
	mu      sync.Mutex
	state   string
	methods []string
	creates []pluginapi.SessionCreateParams
	sends   []pluginapi.SessionSendParams
}

func (h *testHost) InitializeParams() pluginapi.InitializeParams { return pluginapi.InitializeParams{} }
func (h *testHost) CallHost(_ context.Context, method string, params, result any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.methods = append(h.methods, method)
	var response any = struct{}{}
	switch method {
	case pluginapi.HostServiceStorageGet:
		if h.state == "" {
			response = map[string]any{"value": nil}
		} else {
			response = map[string]any{"value": h.state}
		}
	case pluginapi.HostServiceStorageSet:
		encoded, _ := json.Marshal(params)
		var input struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(encoded, &input)
		h.state = input.Value
	case pluginapi.HostServiceSessionCreate:
		encoded, _ := json.Marshal(params)
		var input pluginapi.SessionCreateParams
		_ = json.Unmarshal(encoded, &input)
		h.creates = append(h.creates, input)
		response = pluginapi.SessionCreateResult{SessionID: "generated-session", Created: true}
	case pluginapi.HostServiceSessionSend:
		encoded, _ := json.Marshal(params)
		var input pluginapi.SessionSendParams
		_ = json.Unmarshal(encoded, &input)
		h.sends = append(h.sends, input)
		response = pluginapi.SessionSendResult{State: "running", SessionID: input.SessionID, TurnID: "turn-one"}
	}
	encoded, _ := json.Marshal(response)
	return json.Unmarshal(encoded, result)
}

func TestAutomationTimerUsesPublicSessionServicesAndSettlesRun(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	host := &testHost{}
	c := &controller{now: func() time.Time { return now }, tick: time.Hour}
	if err := c.prepare(context.Background(), host, pluginapi.InitializeParams{WorkspaceID: "workspace-one", ProjectRoot: "/workspace/one"}); err != nil {
		t.Fatal(err)
	}
	if err := c.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.shutdown(context.Background()) })
	task, err := c.add(context.Background(), mutationInput{Title: "Daily review", Prompt: "Review open work", Schedule: "1 9 * * *", Timezone: "UTC", Mode: "new_thread", Recurring: boolPointer(true), Durable: boolPointer(true)})
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	due := c.tasks[task.ID]
	due.NextRunAt = now.Add(-time.Minute)
	c.tasks[task.ID] = due
	c.mu.Unlock()
	c.fireDue(context.Background())
	host.mu.Lock()
	creates := append([]pluginapi.SessionCreateParams(nil), host.creates...)
	sends := append([]pluginapi.SessionSendParams(nil), host.sends...)
	host.mu.Unlock()
	if len(creates) != 1 || creates[0].WorkspaceID != "workspace-one" || creates[0].WorkspaceRoot != "/workspace/one" {
		t.Fatalf("session creates = %+v", creates)
	}
	if len(sends) != 1 || sends[0].SessionID != "generated-session" || sends[0].Cause != "automation.trigger" || sends[0].Presentation == nil || sends[0].Presentation.Kind != "query_bubble" {
		t.Fatalf("sends = %+v", sends)
	}
	runs := c.snapshotRuns()
	if len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("runs = %+v", runs)
	}
	if err := c.settle(context.Background(), pluginapi.TurnLifecycleInput{RequestID: runs[0].RequestID, State: "completed", ThreadID: "generated-session", TurnID: "turn-one", FinalOutput: "done"}); err != nil {
		t.Fatal(err)
	}
	runs = c.snapshotRuns()
	if runs[0].Status != "completed" || runs[0].CompletedAt == nil {
		t.Fatalf("settled run = %+v", runs[0])
	}
}

func TestAutomationFireDueWaitsForInFlightDispatch(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	host := &blockingSendHost{started: started, release: release}
	c := &controller{host: host, workspaceID: "workspace-one", workspaceRoot: "/workspace/one", tasks: map[string]Task{}, now: func() time.Time { return now }}
	task := Task{ID: "task-inflight", Title: "Daily review", Prompt: "Review open work", Cron: "1 9 * * *", Timezone: "UTC", Mode: "new_thread", Recurring: true, WorkspaceID: "workspace-one", WorkspaceRoot: "/workspace/one", NextRunAt: now.Add(-time.Minute)}
	c.tasks[task.ID] = task

	first := make(chan struct{})
	go func() {
		defer close(first)
		c.fireDue(context.Background())
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight session send")
	}

	second := make(chan struct{})
	go func() {
		defer close(second)
		c.fireDue(context.Background())
	}()
	select {
	case <-second:
		t.Fatalf("overlapping fireDue returned while in-flight run was still starting: %+v", c.snapshotRuns())
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight fireDue")
	}
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for overlapping fireDue")
	}
	runs := c.snapshotRuns()
	if len(runs) != 1 || runs[0].Status != "running" || runs[0].SessionID != "generated-session" || runs[0].TurnID != "turn-one" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestAutomationFireDoesNotOverwriteSettledRun(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	host := &blockingSendHost{started: started, release: release}
	c := &controller{host: host, workspaceID: "workspace-one", workspaceRoot: "/workspace/one", tasks: map[string]Task{}, now: func() time.Time { return now }}
	task := Task{ID: "task-settle", Title: "Daily review", Prompt: "Review open work", Mode: "new_thread", WorkspaceID: "workspace-one", WorkspaceRoot: "/workspace/one", NextRunAt: now}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.fire(context.Background(), task, now)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight session send")
	}
	if err := c.settle(context.Background(), pluginapi.TurnLifecycleInput{RequestID: fmt.Sprintf("automation-run-%s-%d", task.ID, now.Unix()), State: "completed", ThreadID: "generated-session", TurnID: "turn-done"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for late fire to finish")
	}
	runs := c.snapshotRuns()
	if len(runs) != 1 || runs[0].Status != "completed" || runs[0].TurnID != "turn-done" || runs[0].CompletedAt == nil {
		t.Fatalf("settled run = %+v", runs)
	}
}

func TestAutomationPartialUpdatePreservesBooleanFields(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	host := &testHost{}
	c := &controller{host: host, tasks: map[string]Task{}, now: func() time.Time { return now }}
	task := Task{ID: "daily", Title: "Daily", Prompt: "Review", Cron: "1 9 * * *", Timezone: "UTC", Mode: "new_thread", Recurring: true, Paused: true, Durable: true, CreatedAt: now}
	c.tasks[task.ID] = task

	updated, err := c.update(context.Background(), mutationInput{ID: task.ID, Title: "Renamed"})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Recurring || !updated.Paused || !updated.Durable {
		t.Fatalf("partial update cleared booleans: %+v", updated)
	}

	updated, err = c.update(context.Background(), mutationInput{ID: task.ID, Paused: boolPointer(false)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Paused || !updated.Recurring || !updated.Durable {
		t.Fatalf("explicit false update = %+v", updated)
	}
}

func boolPointer(value bool) *bool { return &value }

type blockingSendHost struct {
	testHost
	started chan struct{}
	release chan struct{}
}

func (h *blockingSendHost) CallHost(ctx context.Context, method string, params, result any) error {
	if method == pluginapi.HostServiceSessionSend {
		select {
		case <-h.started:
		default:
			close(h.started)
		}
		<-h.release
	}
	return h.testHost.CallHost(ctx, method, params, result)
}

func TestAutomationHeartbeatReusesExistingSession(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	host := &testHost{}
	c := &controller{host: host, tasks: map[string]Task{}, now: func() time.Time { return now }}
	task := Task{ID: "heartbeat", Title: "Continue", Prompt: "Continue the audit", Cron: "1 9 * * *", Timezone: "UTC", Mode: "thread_heartbeat", HeartbeatThreadID: "parent-session", Recurring: false, NextRunAt: now.Add(-time.Minute)}
	c.tasks[task.ID] = task
	c.fireDue(context.Background())
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.sends) != 1 || host.sends[0].SessionID != "parent-session" {
		t.Fatalf("heartbeat sends = %+v", host.sends)
	}
	for _, method := range host.methods {
		if method == pluginapi.HostServiceSessionCreate {
			t.Fatal("heartbeat created a new session")
		}
	}
}

func TestAutomationShutdownStopsTimerAndNextGenerationRestoresDurableState(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	host := &testHost{}
	first := &controller{now: func() time.Time { return now }, tick: time.Hour}
	if err := first.prepare(context.Background(), host, pluginapi.InitializeParams{WorkspaceID: "workspace-one", ProjectRoot: "/workspace/one"}); err != nil {
		t.Fatal(err)
	}
	if err := first.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	task, err := first.add(context.Background(), mutationInput{
		Title: "Persisted review", Prompt: "Review durable state", Schedule: "0 10 * * *", Timezone: "UTC",
		Mode: "new_thread", Recurring: boolPointer(true), Durable: boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	default:
		t.Fatal("automation timer loop remained active after shutdown")
	}

	second := &controller{now: func() time.Time { return now }, tick: time.Hour}
	if err := second.prepare(context.Background(), host, pluginapi.InitializeParams{WorkspaceID: "workspace-one", ProjectRoot: "/workspace/one"}); err != nil {
		t.Fatal(err)
	}
	if err := second.activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.shutdown(context.Background()) })
	second.mu.Lock()
	restored, ok := second.tasks[task.ID]
	second.mu.Unlock()
	if !ok || restored.Prompt != task.Prompt || !restored.Durable || restored.WorkspaceID != "workspace-one" || restored.WorkspaceRoot != "/workspace/one" {
		t.Fatalf("restored task = %+v, present=%v", restored, ok)
	}
}

func TestAutomationRunRequestsAreStableForOneScheduledOccurrence(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	host := &testHost{}
	c := &controller{host: host, workspaceID: "workspace-one", workspaceRoot: "/workspace/one", tasks: map[string]Task{}, now: func() time.Time { return now }}
	task := Task{ID: "task-stable", Title: "Review", Prompt: "Review work", Mode: "new_thread", WorkspaceID: "workspace-one", WorkspaceRoot: "/workspace/one", NextRunAt: now}

	c.fire(context.Background(), task, now)
	c.fire(context.Background(), task, now.Add(10*time.Second))

	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.creates) != 2 || host.creates[0].RequestID != host.creates[1].RequestID {
		t.Fatalf("create requests = %+v", host.creates)
	}
	if len(host.sends) != 2 || host.sends[0].RequestID != host.sends[1].RequestID {
		t.Fatalf("send requests = %+v", host.sends)
	}
}
