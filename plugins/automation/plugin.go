package automation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

const (
	capabilityClient    = "plugin.client.request"
	capabilityLifecycle = "agent.turn.lifecycle"
	stateStorageKey     = "automation.state"
	maxTasks            = 100
	maxRuns             = 500
)

type Task struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	Prompt            string    `json:"prompt"`
	Cron              string    `json:"cron"`
	Timezone          string    `json:"timezone"`
	Mode              string    `json:"mode"`
	WorkspaceID       string    `json:"workspace_id,omitempty"`
	WorkspaceRoot     string    `json:"workspace_root"`
	WorkspaceMode     string    `json:"workspace_mode,omitempty"`
	HeartbeatThreadID string    `json:"heartbeat_thread_id,omitempty"`
	Recurring         bool      `json:"recurring"`
	Paused            bool      `json:"paused"`
	Durable           bool      `json:"durable"`
	CreatedAt         time.Time `json:"created_at"`
	NextRunAt         time.Time `json:"next_run_at"`
}

type Run struct {
	ID            string     `json:"id"`
	TaskID        string     `json:"task_id"`
	Task          Task       `json:"task"`
	RequestID     string     `json:"request_id"`
	Status        string     `json:"status"`
	TriggeredAt   time.Time  `json:"triggered_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	TurnID        string     `json:"turn_id,omitempty"`
	QueueID       string     `json:"queue_id,omitempty"`
	WorkspaceRoot string     `json:"workspace_root,omitempty"`
	Error         string     `json:"error,omitempty"`
}

type persistedState struct {
	Tasks []Task `json:"tasks"`
	Runs  []Run  `json:"runs"`
}

type controller struct {
	mu            sync.Mutex
	host          pluginapi.Host
	workspaceID   string
	workspaceRoot string
	tasks         map[string]Task
	runs          []Run
	stop          chan struct{}
	done          chan struct{}
	stopOnce      sync.Once
	now           func() time.Time
	tick          time.Duration
	// dispatch serializes due-task firing so overlapping ticks and explicit
	// fireDue calls cannot return while a run is still stuck in starting.
	dispatch sync.Mutex
}

func Handler() pluginapi.Handler {
	c := &controller{now: time.Now, tick: 15 * time.Second}
	return pluginapi.Handler{
		Definition: pluginapi.Definition{
			Tools:                []pluginapi.Tool{{ID: "cron", Description: "Manage scheduled Agent prompts. action=list returns tasks; action=add creates a one-shot or recurring task from a five-field cron expression; action=remove deletes a task. Use new_thread for an independent visible conversation and thread_heartbeat to wake an existing conversation. Set workspace=worktree so each triggered session runs in its own git worktree instead of the shared project directory.", InputSchema: cronSchema(), ExecutionScopes: []string{"root"}}},
			Capabilities:         []pluginapi.Capability{{ID: capabilityClient, Kind: "decision", Version: 1}, {ID: capabilityLifecycle, Kind: "observe", Version: 1, ErrorPolicy: "isolate"}},
			RequiredHostServices: []pluginapi.HostService{{ID: pluginapi.HostServiceSessionCreate, Required: true}, {ID: pluginapi.HostServiceSessionSend, Required: true}, {ID: pluginapi.HostServiceStorageGet, Required: true}, {ID: pluginapi.HostServiceStorageSet, Required: true}},
		},
		Initialize: func(ctx context.Context, host pluginapi.Host, params pluginapi.InitializeParams) error {
			return c.prepare(ctx, host, params)
		},
		Activate: c.activate,
		Shutdown: c.shutdown,
		ExecuteTool: func(ctx context.Context, _ pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
			return c.executeTool(ctx, call)
		},
		InvokeCapability: func(ctx context.Context, _ pluginapi.Host, call pluginapi.CapabilityCall) (json.RawMessage, error) {
			return c.invokeCapability(ctx, call)
		},
	}
}

func (c *controller) prepare(ctx context.Context, host pluginapi.Host, params pluginapi.InitializeParams) error {
	if host == nil {
		return errors.New("automation host is required")
	}
	c.mu.Lock()
	c.host = host
	c.workspaceID = strings.TrimSpace(params.WorkspaceID)
	c.workspaceRoot = strings.TrimSpace(params.ProjectRoot)
	c.tasks = make(map[string]Task)
	c.stop = make(chan struct{})
	c.done = make(chan struct{})
	if c.now == nil {
		c.now = time.Now
	}
	if c.tick <= 0 {
		c.tick = 15 * time.Second
	}
	c.mu.Unlock()
	if err := c.load(ctx); err != nil {
		return err
	}
	return nil
}

func (c *controller) activate(context.Context) error {
	go c.loop()
	return nil
}

func (c *controller) shutdown(context.Context) error {
	c.stopOnce.Do(func() {
		if c.stop != nil {
			close(c.stop)
		}
	})
	if c.done != nil {
		<-c.done
	}
	return nil
}

func (c *controller) loop() {
	defer close(c.done)
	ticker := time.NewTicker(c.tick)
	defer ticker.Stop()
	c.fireDue(context.Background())
	for {
		select {
		case <-ticker.C:
			c.fireDue(context.Background())
		case <-c.stop:
			return
		}
	}
}

func (c *controller) load(ctx context.Context) error {
	var result struct {
		Value *string `json:"value"`
	}
	if err := c.host.CallHost(ctx, pluginapi.HostServiceStorageGet, map[string]any{"scope": "workspace", "key": stateStorageKey}, &result); err != nil {
		return err
	}
	if result.Value == nil {
		return nil
	}
	var state persistedState
	if err := json.Unmarshal([]byte(*result.Value), &state); err != nil {
		return fmt.Errorf("decode automation plugin state: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, task := range state.Tasks {
		if task.Durable {
			task.WorkspaceID = c.workspaceID
			task.WorkspaceRoot = c.workspaceRoot
			c.tasks[task.ID] = task
		}
	}
	c.runs = append([]Run(nil), state.Runs...)
	return nil
}

func (c *controller) saveLocked(ctx context.Context) error {
	state := persistedState{Runs: append([]Run(nil), c.runs...)}
	for _, task := range c.tasks {
		if task.Durable {
			state.Tasks = append(state.Tasks, task)
		}
	}
	sort.Slice(state.Tasks, func(i, j int) bool { return state.Tasks[i].CreatedAt.Before(state.Tasks[j].CreatedAt) })
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return c.host.CallHost(ctx, pluginapi.HostServiceStorageSet, map[string]any{"scope": "workspace", "key": stateStorageKey, "value": string(encoded)}, &struct{}{})
}

func (c *controller) executeTool(ctx context.Context, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	var args mutationInput
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return pluginapi.ToolResult{}, err
	}
	switch strings.TrimSpace(args.Action) {
	case "list":
		return c.toolResult(c.snapshotTasks())
	case "add":
		if args.HeartbeatThreadID == "" {
			args.HeartbeatThreadID = call.SessionID
			if args.HeartbeatThreadID == "" {
				args.HeartbeatThreadID = call.ThreadID
			}
		}
		task, err := c.add(ctx, args)
		if err != nil {
			return pluginapi.ToolResult{}, err
		}
		return c.toolResult(map[string]any{"action": "cron", "task": task})
	case "remove":
		if err := c.remove(ctx, args.ID); err != nil {
			return pluginapi.ToolResult{}, err
		}
		return c.toolResult(map[string]any{"action": "cron", "removed": strings.TrimSpace(args.ID)})
	default:
		return pluginapi.ToolResult{}, errors.New("cron requires action=list, add, or remove")
	}
}

func (c *controller) invokeCapability(ctx context.Context, call pluginapi.CapabilityCall) (json.RawMessage, error) {
	switch call.Capability {
	case capabilityClient:
		var request struct {
			Method string          `json:"method"`
			Input  json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(call.Input, &request); err != nil {
			return nil, err
		}
		value, err := c.clientRequest(ctx, request.Method, request.Input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"result": value})
	case capabilityLifecycle:
		var input pluginapi.TurnLifecycleInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), c.settle(ctx, input)
	default:
		return nil, fmt.Errorf("unknown capability %q", call.Capability)
	}
}

func (c *controller) clientRequest(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch strings.TrimSpace(method) {
	case "automation.list":
		return map[string]any{"tasks": c.snapshotTasks(), "workspace": map[string]string{"id": c.workspaceID, "root": c.workspaceRoot}}, nil
	case "automation.run.list":
		return map[string]any{"runs": c.snapshotRuns()}, nil
	case "automation.create":
		var input mutationInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return c.add(ctx, input)
	case "automation.update":
		var input mutationInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return c.update(ctx, input)
	case "automation.remove":
		var input mutationInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, c.remove(ctx, input.ID)
	default:
		return nil, fmt.Errorf("unknown client method %q", method)
	}
}

type mutationInput struct {
	Action            string `json:"action,omitempty"`
	ID                string `json:"id,omitempty"`
	Title             string `json:"title,omitempty"`
	Prompt            string `json:"prompt,omitempty"`
	Schedule          string `json:"schedule,omitempty"`
	Cron              string `json:"cron,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	Mode              string `json:"mode,omitempty"`
	HeartbeatThreadID string `json:"heartbeat_thread_id,omitempty"`
	Workspace         string `json:"workspace,omitempty"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	WorkspaceRoot     string `json:"workspace_root,omitempty"`
	Recurring         *bool  `json:"recurring,omitempty"`
	Paused            *bool  `json:"paused,omitempty"`
	Durable           *bool  `json:"durable,omitempty"`
}

func (c *controller) add(ctx context.Context, input mutationInput) (Task, error) {
	input.ID = ""
	task, err := c.validatedTask(input, Task{})
	if err != nil {
		return Task{}, err
	}
	task.ID, err = randomID("task")
	if err != nil {
		return Task{}, err
	}
	task.CreatedAt = c.now().UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tasks) >= maxTasks {
		return Task{}, fmt.Errorf("automation task limit %d reached", maxTasks)
	}
	c.tasks[task.ID] = task
	if err := c.saveLocked(ctx); err != nil {
		delete(c.tasks, task.ID)
		return Task{}, err
	}
	return task, nil
}

func (c *controller) update(ctx context.Context, input mutationInput) (Task, error) {
	id := strings.TrimSpace(input.ID)
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.tasks[id]
	if !ok {
		return Task{}, fmt.Errorf("automation %q not found", id)
	}
	updated, err := c.validatedTask(input, existing)
	if err != nil {
		return Task{}, err
	}
	updated.ID, updated.CreatedAt = existing.ID, existing.CreatedAt
	c.tasks[id] = updated
	if err := c.saveLocked(ctx); err != nil {
		c.tasks[id] = existing
		return Task{}, err
	}
	return updated, nil
}

func (c *controller) validatedTask(input mutationInput, base Task) (Task, error) {
	if expected := strings.TrimSpace(input.WorkspaceID); expected != "" && expected != c.workspaceID {
		return Task{}, errors.New("automation target workspace does not match the active plugin workspace")
	}
	if expected := strings.TrimSpace(input.WorkspaceRoot); expected != "" && filepath.Clean(expected) != filepath.Clean(c.workspaceRoot) {
		return Task{}, errors.New("automation target workspace root does not match the active plugin workspace")
	}
	value := base
	value.WorkspaceID = c.workspaceID
	value.WorkspaceRoot = c.workspaceRoot
	if strings.TrimSpace(input.Title) != "" || base.ID == "" {
		value.Title = strings.TrimSpace(input.Title)
	}
	if strings.TrimSpace(input.Prompt) != "" || base.ID == "" {
		value.Prompt = strings.TrimSpace(input.Prompt)
	}
	schedule := strings.TrimSpace(input.Schedule)
	if schedule == "" {
		schedule = strings.TrimSpace(input.Cron)
	}
	if schedule != "" || base.ID == "" {
		value.Cron = schedule
	}
	if input.Recurring != nil {
		value.Recurring = *input.Recurring
	}
	if input.Paused != nil {
		value.Paused = *input.Paused
	}
	if input.Durable != nil {
		value.Durable = *input.Durable
	}
	if value.Prompt == "" && !value.Paused {
		return Task{}, errors.New("automation prompt is required")
	}
	expr, err := ParseCronExpression(value.Cron)
	if err != nil {
		return Task{}, fmt.Errorf("invalid automation schedule: %w", err)
	}
	if strings.TrimSpace(input.Timezone) != "" || base.ID == "" {
		value.Timezone = strings.TrimSpace(input.Timezone)
	}
	if value.Timezone == "" {
		value.Timezone = time.Local.String()
	}
	loc, err := time.LoadLocation(value.Timezone)
	if err != nil {
		return Task{}, fmt.Errorf("invalid automation timezone %q: %w", value.Timezone, err)
	}
	if strings.TrimSpace(input.Mode) != "" || base.ID == "" {
		value.Mode = strings.TrimSpace(input.Mode)
	}
	if value.Mode == "" {
		value.Mode = "new_thread"
	}
	if value.Mode != "new_thread" && value.Mode != "thread_heartbeat" {
		return Task{}, fmt.Errorf("invalid automation mode %q", value.Mode)
	}
	if strings.TrimSpace(input.HeartbeatThreadID) != "" {
		value.HeartbeatThreadID = strings.TrimSpace(input.HeartbeatThreadID)
	}
	if value.Mode == "thread_heartbeat" && value.HeartbeatThreadID == "" {
		return Task{}, errors.New("thread heartbeat automation requires a thread id")
	}
	if strings.TrimSpace(input.Workspace) != "" || base.ID == "" {
		value.WorkspaceMode = strings.TrimSpace(input.Workspace)
	}
	if value.WorkspaceMode == "" {
		value.WorkspaceMode = "shared"
	}
	if value.WorkspaceMode != "shared" && value.WorkspaceMode != "worktree" {
		return Task{}, fmt.Errorf("invalid automation workspace %q", value.WorkspaceMode)
	}
	if value.WorkspaceMode == "worktree" && value.Mode == "thread_heartbeat" {
		return Task{}, errors.New("worktree workspace requires new_thread mode")
	}
	next, err := expr.NextRun(c.now().In(loc))
	if err != nil {
		return Task{}, err
	}
	if next.After(c.now().AddDate(1, 0, 0)) {
		return Task{}, errors.New("automation next run is more than 1 year away")
	}
	value.NextRunAt = next.UTC()
	if value.Title == "" {
		value.Title = value.Prompt
	}
	return value, nil
}

func (c *controller) remove(ctx context.Context, rawID string) error {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return errors.New("automation id is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.tasks[id]
	if !ok {
		return fmt.Errorf("automation %q not found", id)
	}
	delete(c.tasks, id)
	if err := c.saveLocked(ctx); err != nil {
		c.tasks[id] = existing
		return err
	}
	return nil
}

func (c *controller) fireDue(ctx context.Context) {
	c.dispatch.Lock()
	defer c.dispatch.Unlock()
	now := c.now().UTC()
	c.mu.Lock()
	var due []Task
	for _, task := range c.tasks {
		if task.Paused || task.NextRunAt.After(now) {
			continue
		}
		due = append(due, task)
	}
	c.mu.Unlock()
	// The dispatch opportunity is spent only once the execution service
	// accepted the task (see consume). During startup the plugin activates
	// before the host binds the session service; firing into that window used
	// to delete one-shot tasks and advance recurring ones without ever
	// running them, so the catch-up run was lost.
	for _, task := range due {
		if c.fire(ctx, task, now) {
			c.consume(ctx, task, now)
		}
	}
}

// consume spends one dispatch opportunity for a task that fire() reported as
// dispatched: one-shot tasks leave the schedule, and recurring tasks move to
// their next occurrence.
func (c *controller) consume(ctx context.Context, task Task, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing, ok := c.tasks[task.ID]
	if !ok || existing.Paused {
		return
	}
	if existing.Recurring {
		next, err := nextRun(existing, now)
		if err == nil {
			existing.NextRunAt = next
		} else {
			existing.Paused = true
		}
		c.tasks[task.ID] = existing
	} else {
		delete(c.tasks, task.ID)
	}
	_ = c.saveLocked(ctx)
}

func (c *controller) fire(ctx context.Context, task Task, now time.Time) bool {
	scheduledAt := task.NextRunAt.UTC()
	if scheduledAt.IsZero() {
		scheduledAt = now
	}
	runID := fmt.Sprintf("run-%s-%d", task.ID, scheduledAt.Unix())
	requestID := "automation-" + runID
	run := Run{ID: runID, TaskID: task.ID, Task: task, RequestID: requestID, Status: "starting", TriggeredAt: now}
	c.mu.Lock()
	workspaceID := c.workspaceID
	workspaceRoot := c.workspaceRoot
	c.runs = append(c.runs, run)
	if len(c.runs) > maxRuns {
		c.runs = c.runs[len(c.runs)-maxRuns:]
	}
	_ = c.saveLocked(ctx)
	c.mu.Unlock()
	sessionID := task.HeartbeatThreadID
	workspaceMode := task.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = "shared"
	}
	executionRoot := ""
	var err error
	if task.Mode == "new_thread" {
		var created pluginapi.SessionCreateResult
		if task.WorkspaceID != workspaceID || filepath.Clean(task.WorkspaceRoot) != filepath.Clean(workspaceRoot) {
			err = errors.New("automation target workspace does not match the active plugin workspace")
		} else {
			err = c.host.CallHost(ctx, pluginapi.HostServiceSessionCreate, pluginapi.SessionCreateParams{RequestID: "create-" + runID, Name: task.Title, Visibility: "user", ContextSource: "fresh", Workspace: workspaceMode, WorkspaceID: task.WorkspaceID, WorkspaceRoot: task.WorkspaceRoot}, &created)
		}
		sessionID = created.SessionID
		executionRoot = created.WorkspaceRoot
	}
	var sent pluginapi.SessionSendResult
	if err == nil {
		err = c.host.CallHost(ctx, pluginapi.HostServiceSessionSend, pluginapi.SessionSendParams{RequestID: requestID, SessionID: sessionID, Input: pluginapi.SessionInput{Prompt: task.Prompt, ContextBlocks: []pluginapi.TurnContextBlock{{Kind: "automation_trigger", Source: "automation", Content: fmt.Sprintf("task_id=%s\nrun_id=%s\nworkspace_id=%s\nworkspace_root=%s\nworkspace_mode=%s\nexecution_root=%s\nschedule=%s\ntimezone=%s\ntriggered_at=%s", task.ID, runID, task.WorkspaceID, task.WorkspaceRoot, workspaceMode, executionRoot, task.Cron, task.Timezone, now.Format(time.RFC3339))}}}, Presentation: &pluginapi.SessionInputPresentation{Kind: "query_bubble", Text: "自动化任务已唤醒 Agent", Name: task.Title}, Cause: "automation.trigger"}, &sent)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := range c.runs {
		if c.runs[index].ID != runID {
			continue
		}
		if runSettled(c.runs[index].Status) {
			return true
		}
		c.runs[index].SessionID = sessionID
		c.runs[index].WorkspaceRoot = executionRoot
		if err != nil {
			if sessionServiceUnavailable(err) {
				// Nothing was ever dispatched: the host has not bound the
				// session service yet, so the startup catch-up must not be
				// burned here. Drop the placeholder run and let a later tick
				// retry the task while it is still due.
				c.runs = append(c.runs[:index], c.runs[index+1:]...)
				_ = c.saveLocked(ctx)
				return false
			}
			finished := c.now().UTC()
			c.runs[index].Status = "failed"
			c.runs[index].CompletedAt = &finished
			c.runs[index].Error = err.Error()
			return true
		} else if c.runs[index].Status == "running" && sent.State != "running" {
			// A running lifecycle event arrived while the send call was still in
			// flight; do not downgrade the run back to the queued state reported
			// by the late response. Only backfill the queue identity if missing.
			if c.runs[index].QueueID == "" {
				c.runs[index].QueueID = sent.QueueID
			}
		} else {
			c.runs[index].Status = sent.State
			c.runs[index].TurnID = sent.TurnID
			c.runs[index].QueueID = sent.QueueID
		}
		break
	}
	_ = c.saveLocked(ctx)
	return true
}

// sessionServiceUnavailable reports a host that accepted the call but has not
// bound the session service yet, which is the window between plugin activation
// and appserver startup. The plugin runs in its own process and only sees the
// error text, so that window is detected by message.
func sessionServiceUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "session service is unavailable")
}

func (c *controller) settle(ctx context.Context, input pluginapi.TurnLifecycleInput) error {
	if input.State != "running" && input.State != "completed" && input.State != "failed" && input.State != "interrupted" && input.State != "discarded" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := range c.runs {
		if c.runs[index].RequestID != input.RequestID {
			continue
		}
		if input.State == "running" {
			// The queued run started executing. Record the session/turn identity so
			// the UI can show "running" instead of "queued" until a terminal event.
			// Terminal states are never reverted by a late running event.
			if runSettled(c.runs[index].Status) {
				return nil
			}
			if c.runs[index].Status != "running" {
				c.runs[index].Status = "running"
			}
			if input.ThreadID != "" {
				c.runs[index].SessionID = input.ThreadID
			}
			if input.TurnID != "" {
				c.runs[index].TurnID = input.TurnID
			}
			if input.QueueID != "" {
				c.runs[index].QueueID = input.QueueID
			}
			return c.saveLocked(ctx)
		}
		finished := c.now().UTC()
		c.runs[index].Status = input.State
		c.runs[index].CompletedAt = &finished
		c.runs[index].SessionID = input.ThreadID
		c.runs[index].TurnID = input.TurnID
		c.runs[index].QueueID = input.QueueID
		c.runs[index].Error = input.Error
		return c.saveLocked(ctx)
	}
	return nil
}

func (c *controller) snapshotTasks() []Task {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Task, 0, len(c.tasks))
	for _, task := range c.tasks {
		out = append(out, task)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
func (c *controller) snapshotRuns() []Run {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Run(nil), c.runs...)
}

func runSettled(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "discarded":
		return true
	default:
		return false
	}
}
func (c *controller) toolResult(value any) (pluginapi.ToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(string(encoded)), nil
}
func nextRun(task Task, after time.Time) (time.Time, error) {
	expr, err := ParseCronExpression(task.Cron)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(task.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	next, err := expr.NextRun(after.In(loc))
	return next.UTC(), err
}
func randomID(prefix string) (string, error) {
	var value [10]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func cronSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"list", "add", "remove"}}, "cron": map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "timezone": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string", "enum": []string{"new_thread", "thread_heartbeat"}}, "heartbeat_thread_id": map[string]any{"type": "string"}, "workspace": map[string]any{"type": "string", "enum": []string{"shared", "worktree"}}, "recurring": map[string]any{"type": "boolean"}, "durable": map[string]any{"type": "boolean"}, "id": map[string]any{"type": "string"}}, "required": []string{"action"}}
}
