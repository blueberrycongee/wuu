package subagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

const (
	capabilityPrompt    = "agent.system_prompt.section"
	capabilityClient    = "plugin.client.request"
	capabilityLifecycle = "agent.turn.lifecycle"
	capabilityInterrupt = "agent.turn.interrupted"
)

var taskNameCleaner = regexp.MustCompile(`[^a-z0-9_]+`)

type taskRecord struct {
	SessionID          string `json:"session_id"`
	ParentSessionID    string `json:"parent_session_id"`
	ParentTurnID       string `json:"parent_turn_id,omitempty"`
	Name               string `json:"name"`
	RequestID          string `json:"request_id"`
	TurnID             string `json:"turn_id,omitempty"`
	QueueID            string `json:"queue_id,omitempty"`
	State              string `json:"state"`
	SuppressCompletion bool   `json:"suppress_completion,omitempty"`
}

const taskIndexKey = "tasks.v2"
const maxTaskRecords = 128
const foregroundAwaitBudgetMS = 10 * 60 * 1000

type spawnAgentOutput struct {
	SessionID       string `json:"session_id"`
	TaskName        string `json:"task_name"`
	State           string `json:"state"`
	RunInBackground bool   `json:"run_in_background"`
	Backgrounded    bool   `json:"backgrounded"`
	TimedOut        bool   `json:"timed_out,omitempty"`
	FinalOutput     string `json:"final_output,omitempty"`
	Error           string `json:"error,omitempty"`
}

type taskIndex struct {
	Records []taskRecord `json:"records"`
}

var taskIndexMu sync.Mutex

func Handler() pluginapi.Handler {
	return pluginapi.Handler{
		ConcurrentCapabilities: []string{capabilityPrompt, capabilityClient},
		Definition: pluginapi.Definition{
			Tools: []pluginapi.Tool{
				{ID: "spawn_agent", Description: "Delegate a bounded task when separate context materially improves the result. Set run_in_background=false when the next step depends on the child result; the tool waits in the current turn and returns the result directly. Background tasks return immediately and deliver completion later as a normal read-only query bubble.", InputSchema: spawnSchema(), ExecutionScopes: []string{"root", "collaboration"}, Activity: &pluginapi.ToolActivity{ConcurrencySafe: true}},
				{ID: "send_message", Description: "Send a follow-up turn to an existing child task by session id or task name.", InputSchema: objectSchema(map[string]any{"target": stringField("Child session id or task name."), "message": stringField("Message to deliver.")}, "target", "message"), ExecutionScopes: []string{"root", "collaboration"}, Activity: &pluginapi.ToolActivity{ConcurrencySafe: true}},
				{ID: "close_agent", Description: "Cancel an existing child task by session id or task name.", InputSchema: objectSchema(map[string]any{"target": stringField("Child session id or task name.")}, "target"), ExecutionScopes: []string{"root", "collaboration"}, Activity: &pluginapi.ToolActivity{ConcurrencySafe: true}},
			},
			Capabilities: []pluginapi.Capability{
				{ID: capabilityPrompt, Kind: "transform", Version: 1},
				{ID: capabilityClient, Kind: "decision", Version: 1},
				{ID: capabilityLifecycle, Kind: "observe", Version: 1, ErrorPolicy: "isolate"},
				{ID: capabilityInterrupt, Kind: "observe", Version: 1, ErrorPolicy: "isolate"},
			},
			RequiredHostServices: []pluginapi.HostService{
				{ID: pluginapi.HostServiceSessionCreate, Required: true},
				{ID: pluginapi.HostServiceSessionSend, Required: true},
				{ID: pluginapi.HostServiceSessionList, Required: true},
				{ID: pluginapi.HostServiceSessionCancel, Required: true},
				{ID: pluginapi.HostServiceSessionInspect, Required: true},
				{ID: pluginapi.HostServiceStorageGet, Required: true},
				{ID: pluginapi.HostServiceStorageSet, Required: true},
				{ID: pluginapi.HostServiceStorageKeys, Required: true},
				{ID: pluginapi.HostServiceStorageDelete, Required: true},
			},
		},
		ExecuteTool:      executeTool,
		InvokeCapability: invokeCapability,
	}
}

func executeTool(ctx context.Context, host pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	switch call.ToolID {
	case "spawn_agent":
		return spawnAgent(ctx, host, call)
	case "send_message":
		return sendMessage(ctx, host, call)
	case "close_agent":
		return closeAgent(ctx, host, call)
	default:
		return pluginapi.ToolResult{}, fmt.Errorf("unknown tool %q", call.ToolID)
	}
}

func spawnAgent(ctx context.Context, host pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	var args struct {
		Description     string `json:"description"`
		Prompt          string `json:"prompt"`
		SubagentType    string `json:"subagent_type"`
		Name            string `json:"name"`
		AgentProfile    string `json:"agent_profile"`
		Model           string `json:"model"`
		Context         string `json:"context"`
		Isolation       string `json:"isolation"`
		RunInBackground *bool  `json:"run_in_background"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return pluginapi.ToolResult{}, err
	}
	if strings.TrimSpace(args.Description) == "" || strings.TrimSpace(args.Prompt) == "" {
		return pluginapi.ToolResult{}, errors.New("spawn_agent requires description and prompt")
	}
	if strings.TrimSpace(args.AgentProfile) != "" {
		return pluginapi.ToolResult{}, errors.New("agent_profile is no longer part of the Subagent plugin contract; put required memory in the task prompt")
	}
	if strings.TrimSpace(call.SessionID) == "" {
		return pluginapi.ToolResult{}, errors.New("spawn_agent requires a parent session")
	}
	runInBackground := args.RunInBackground == nil || *args.RunInBackground
	name := strings.TrimSpace(args.Name)
	if name == "" {
		name = deriveTaskName(args.Description)
	}
	requestID, err := newRequestID()
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	contextSource := "fresh"
	switch strings.TrimSpace(args.Context) {
	case "", "fresh":
	case "fork":
		contextSource = "fork"
	default:
		return pluginapi.ToolResult{}, fmt.Errorf("unsupported subagent context %q", args.Context)
	}
	workspace := "shared"
	if strings.TrimSpace(args.Isolation) == "worktree" {
		workspace = "worktree"
		contextSource = "fork"
	}
	record, err := createTask(ctx, host, pluginapi.SessionCreateParams{RequestID: "create-" + requestID, Name: name, Visibility: "plugin", ParentSessionID: call.SessionID, ContextSource: contextSource, Workspace: workspace, ModelAlias: strings.TrimSpace(args.Model), Instructions: workerInstructions(strings.TrimSpace(args.SubagentType))}, taskRecord{ParentSessionID: call.SessionID, ParentTurnID: call.TurnID, Name: name, RequestID: "turn-" + requestID, State: "created"})
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	var sent pluginapi.SessionSendResult
	err = host.CallHost(ctx, pluginapi.HostServiceSessionSend, pluginapi.SessionSendParams{RequestID: record.RequestID, SessionID: record.SessionID, Input: pluginapi.SessionInput{Prompt: strings.TrimSpace(args.Prompt)}, Presentation: &pluginapi.SessionInputPresentation{Kind: "query_bubble", Text: "子任务 " + name + " 已开始", Name: name}, Cause: "subagent.task"}, &sent)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	record.State = sent.State
	record.TurnID = sent.TurnID
	record.QueueID = sent.QueueID
	if err := saveRecord(ctx, host, record); err != nil {
		return pluginapi.ToolResult{}, err
	}
	if runInBackground {
		return spawnAgentResult(spawnAgentOutput{
			SessionID: record.SessionID, TaskName: name, State: sent.State,
			RunInBackground: true, Backgrounded: true,
		})
	}

	var inspected pluginapi.SessionInspectResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionInspect, pluginapi.SessionInspectParams{
		SessionID: record.SessionID,
		RequestID: record.RequestID,
		Wait:      pluginapi.SessionInspectWaitTerminal,
		TimeoutMS: foregroundAwaitBudgetMS,
	}, &inspected); err != nil {
		return pluginapi.ToolResult{}, err
	}
	if inspected.TimedOut || inspected.Turn == nil || !terminalTaskState(inspected.Turn.State) {
		state := sent.State
		if inspected.Turn != nil && strings.TrimSpace(inspected.Turn.State) != "" {
			state = inspected.Turn.State
		}
		return spawnAgentResult(spawnAgentOutput{
			SessionID: record.SessionID, TaskName: name, State: state,
			RunInBackground: false, Backgrounded: true, TimedOut: inspected.TimedOut,
		})
	}

	// The terminal result is returned by this tool call, so the queued lifecycle
	// notification must only acknowledge and clean up the record instead of
	// starting a duplicate parent turn with the same completion.
	record.State = inspected.Turn.State
	record.TurnID = inspected.Turn.TurnID
	record.QueueID = inspected.Turn.QueueID
	record.SuppressCompletion = true
	if err := saveRecord(ctx, host, record); err != nil {
		return pluginapi.ToolResult{}, err
	}
	return spawnAgentResult(spawnAgentOutput{
		SessionID: record.SessionID, TaskName: name, State: inspected.Turn.State,
		RunInBackground: false, Backgrounded: false,
		FinalOutput: inspected.Turn.FinalOutput, Error: inspected.Turn.Error,
	})
}

func spawnAgentResult(output spawnAgentOutput) (pluginapi.ToolResult, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(string(encoded)), nil
}

func sendMessage(ctx context.Context, host pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	var args struct {
		Target  string `json:"target"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return pluginapi.ToolResult{}, err
	}
	record, err := resolveTask(ctx, host, call.SessionID, args.Target)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	requestID, err := newRequestID()
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	nextRequestID := "turn-" + requestID
	previousRequestID := record.RequestID
	record.RequestID = nextRequestID
	record.ParentSessionID = call.SessionID
	record.ParentTurnID = call.TurnID
	record.SuppressCompletion = false
	record.State = "created"
	if err := saveRecord(ctx, host, record); err != nil {
		return pluginapi.ToolResult{}, err
	}
	var sent pluginapi.SessionSendResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionSend, pluginapi.SessionSendParams{RequestID: nextRequestID, SessionID: record.SessionID, Input: pluginapi.SessionInput{Prompt: strings.TrimSpace(args.Message)}, Presentation: &pluginapi.SessionInputPresentation{Kind: "query_bubble", Text: "父任务已更新要求", Name: record.Name}, Cause: "subagent.followup", IfRunning: pluginapi.SessionIfRunningSteer}, &sent); err != nil {
		return pluginapi.ToolResult{}, err
	}
	if sent.Steered {
		record.RequestID = previousRequestID
	}
	record.State = sent.State
	record.TurnID = sent.TurnID
	record.QueueID = sent.QueueID
	if err := saveRecord(ctx, host, record); err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(fmt.Sprintf(`{"session_id":%q,"state":%q,"steered":%t}`, record.SessionID, sent.State, sent.Steered)), nil
}

func closeAgent(ctx context.Context, host pluginapi.Host, call pluginapi.ToolCall) (pluginapi.ToolResult, error) {
	var args struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return pluginapi.ToolResult{}, err
	}
	record, err := resolveTask(ctx, host, call.SessionID, args.Target)
	if err != nil {
		return pluginapi.ToolResult{}, err
	}
	// Closing a child is an explicit decision by the parent. Persist that intent
	// before cancellation so the resulting interrupted lifecycle event cannot
	// wake the parent with a completion it deliberately dismissed.
	record.SuppressCompletion = true
	if err := saveRecord(ctx, host, record); err != nil {
		return pluginapi.ToolResult{}, err
	}
	var result pluginapi.SessionCancelResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionCancel, pluginapi.SessionCancelParams{SessionID: record.SessionID, TurnID: record.TurnID, QueueID: record.QueueID}, &result); err != nil {
		return pluginapi.ToolResult{}, err
	}
	return pluginapi.TextResult(fmt.Sprintf(`{"session_id":%q,"cancelled":%t}`, result.SessionID, result.Cancelled)), nil
}

func invokeCapability(ctx context.Context, host pluginapi.Host, call pluginapi.CapabilityCall) (json.RawMessage, error) {
	switch call.Capability {
	case capabilityPrompt:
		return json.Marshal(map[string]string{"text": promptSection})
	case capabilityClient:
		var request struct {
			Method string          `json:"method"`
			Input  json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(call.Input, &request); err != nil {
			return nil, err
		}
		switch request.Method {
		case "status.list":
			var params pluginapi.SessionListParams
			if len(request.Input) != 0 {
				if err := json.Unmarshal(request.Input, &params); err != nil {
					return nil, err
				}
			}
			var result pluginapi.SessionListResult
			if err := host.CallHost(ctx, pluginapi.HostServiceSessionList, params, &result); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"result": result})
		default:
			return nil, fmt.Errorf("unknown client method %q", request.Method)
		}
	case capabilityLifecycle:
		var input pluginapi.TurnLifecycleInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return nil, err
		}
		if input.State != "completed" && input.State != "failed" && input.State != "interrupted" && input.State != "discarded" {
			return json.RawMessage(`{}`), nil
		}
		record, ok, err := loadRecord(ctx, host, input.RequestID)
		if err == nil && !ok {
			err = reconcileOwnedSessions(ctx, host)
			if err == nil {
				record, ok, err = loadRecord(ctx, host, input.RequestID)
			}
		}
		if err != nil || !ok {
			return json.RawMessage(`{}`), err
		}
		record.State = input.State
		if err := saveRecord(ctx, host, record); err != nil {
			return nil, err
		}
		if record.SuppressCompletion {
			return json.RawMessage(`{}`), deleteRecord(ctx, host, record.SessionID)
		}
		output := strings.TrimSpace(input.FinalOutput)
		if output == "" {
			output = strings.TrimSpace(input.Error)
		}
		if output == "" {
			output = "子任务已结束，但没有返回文本结果。"
		}
		deliveryRequestID := completionDeliveryRequestID(input.RequestID)
		continuation, continuationParent, tracksContinuation, err := parentContinuationRecord(ctx, host, record, deliveryRequestID)
		if err != nil {
			return nil, err
		}
		if tracksContinuation {
			if err := saveRecord(ctx, host, continuation); err != nil {
				return nil, err
			}
		}
		prompt := fmt.Sprintf("子任务 %s 已%s。请检查并整合以下交接结果：\n\n%s", record.Name, lifecycleLabel(input.State), output)
		var sent pluginapi.SessionSendResult
		if err := host.CallHost(ctx, pluginapi.HostServiceSessionSend, pluginapi.SessionSendParams{RequestID: deliveryRequestID, SessionID: record.ParentSessionID, ReplyToTurnID: record.ParentTurnID, Input: pluginapi.SessionInput{Prompt: prompt}, Presentation: &pluginapi.SessionInputPresentation{Kind: "query_bubble", Text: "子任务 " + record.Name + " 已更新", Name: record.Name, RelatedSessionID: record.SessionID}, Cause: "subagent.completion", IfRunning: pluginapi.SessionIfRunningSteer}, &sent); err != nil {
			if tracksContinuation {
				return nil, errors.Join(err, saveRecord(ctx, host, continuationParent))
			}
			return nil, err
		}
		if tracksContinuation {
			tracked := continuation
			if sent.Steered {
				// A steered completion joins the parent's existing turn, whose own
				// request record remains responsible for routing one combined result
				// upward. Restore that record instead of making this child completion
				// look like a separate continuation turn.
				tracked = continuationParent
			} else {
				tracked.State = sent.State
			}
			if err := saveRecord(ctx, host, tracked); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`{}`), deleteRecord(ctx, host, record.SessionID)
	case capabilityInterrupt:
		var input pluginapi.AgentTurnInterruptedInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.ThreadID) == "" || strings.TrimSpace(input.TurnID) == "" {
			return json.RawMessage(`{}`), nil
		}
		var listed pluginapi.SessionListResult
		if err := host.CallHost(ctx, pluginapi.HostServiceSessionList, pluginapi.SessionListParams{ParentSessionID: input.ThreadID}, &listed); err != nil {
			return nil, err
		}
		for _, child := range listed.Sessions {
			record, ok, err := loadRecordBySession(ctx, host, child.SessionID)
			if err != nil {
				return nil, err
			}
			if !ok || strings.TrimSpace(record.ParentTurnID) != strings.TrimSpace(input.TurnID) {
				continue
			}
			record.SuppressCompletion = true
			if err := saveRecord(ctx, host, record); err != nil {
				return nil, err
			}
			var result pluginapi.SessionCancelResult
			if err := host.CallHost(ctx, pluginapi.HostServiceSessionCancel, pluginapi.SessionCancelParams{SessionID: record.SessionID, TurnID: record.TurnID, QueueID: record.QueueID}, &result); err != nil {
				return nil, err
			}
			if result.Cancelled {
				record.State = "interrupted"
				if err := saveRecord(ctx, host, record); err != nil {
					return nil, err
				}
			}
		}
		return json.RawMessage(`{}`), nil
	default:
		return nil, fmt.Errorf("unknown capability %q", call.Capability)
	}
}

func resolveTask(ctx context.Context, host pluginapi.Host, parentID, target string) (taskRecord, error) {
	target = strings.TrimSpace(target)
	var listed pluginapi.SessionListResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionList, pluginapi.SessionListParams{ParentSessionID: strings.TrimSpace(parentID)}, &listed); err != nil {
		return taskRecord{}, err
	}
	var match *pluginapi.SessionSummary
	for index := range listed.Sessions {
		item := &listed.Sessions[index]
		if item.SessionID != target && item.Name != target {
			continue
		}
		if match != nil {
			return taskRecord{}, fmt.Errorf("task name %q is ambiguous; use the session id", target)
		}
		match = item
	}
	if match == nil {
		return taskRecord{}, fmt.Errorf("child session %q not found", target)
	}
	record, ok, err := loadRecordBySession(ctx, host, match.SessionID)
	if err != nil {
		return taskRecord{}, err
	}
	if ok {
		return record, nil
	}
	var inspected pluginapi.SessionInspectResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionInspect, pluginapi.SessionInspectParams{SessionID: match.SessionID, Wait: pluginapi.SessionInspectWaitNone}, &inspected); err != nil {
		return taskRecord{}, err
	}
	if inspected.Turn != nil && strings.TrimSpace(inspected.Turn.RequestID) != "" {
		recovered := taskRecord{
			SessionID: match.SessionID, ParentSessionID: match.ParentSessionID, Name: match.Name,
			RequestID: inspected.Turn.RequestID, TurnID: inspected.Turn.TurnID,
			QueueID: inspected.Turn.QueueID, State: inspected.Turn.State,
		}
		if err := saveRecord(ctx, host, recovered); err != nil {
			return taskRecord{}, err
		}
		return recovered, nil
	}
	return taskRecord{SessionID: match.SessionID, ParentSessionID: match.ParentSessionID, Name: match.Name, State: match.State}, nil
}

// reconcileOwnedSessions repairs the bounded cache from durable child sessions.
// Terminal sessions are intentionally not reintroduced: successful completion
// delivery garbage-collects their task record, while the host's lifecycle
// outbox retains and retries any terminal event that was not acknowledged.
func reconcileOwnedSessions(ctx context.Context, host pluginapi.Host) error {
	var listed pluginapi.SessionListResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionList, pluginapi.SessionListParams{Scope: pluginapi.SessionListScopeOwned}, &listed); err != nil {
		return err
	}
	for _, child := range listed.Sessions {
		if _, ok, err := loadRecordBySession(ctx, host, child.SessionID); err != nil {
			return err
		} else if ok {
			continue
		}
		var inspected pluginapi.SessionInspectResult
		if err := host.CallHost(ctx, pluginapi.HostServiceSessionInspect, pluginapi.SessionInspectParams{SessionID: child.SessionID, Wait: pluginapi.SessionInspectWaitNone}, &inspected); err != nil {
			return err
		}
		if inspected.Turn == nil || terminalTaskState(inspected.Turn.State) || strings.TrimSpace(inspected.Turn.RequestID) == "" {
			continue
		}
		if err := saveRecord(ctx, host, taskRecord{
			SessionID: child.SessionID, ParentSessionID: child.ParentSessionID, Name: child.Name,
			RequestID: inspected.Turn.RequestID, TurnID: inspected.Turn.TurnID,
			QueueID: inspected.Turn.QueueID, State: inspected.Turn.State,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Hold the index lock through creation and registration so concurrent spawns
// cannot consume the same free slot after the capacity check.
func createTask(ctx context.Context, host pluginapi.Host, params pluginapi.SessionCreateParams, record taskRecord) (taskRecord, error) {
	taskIndexMu.Lock()
	defer taskIndexMu.Unlock()
	index, err := loadTaskIndexLocked(ctx, host)
	if err != nil {
		return taskRecord{}, err
	}
	if len(index.Records) >= maxTaskRecords {
		if err := refreshTaskIndex(ctx, host, &index); err != nil {
			return taskRecord{}, err
		}
	}
	if err := trimTaskIndex(&index, maxTaskRecords-1); err != nil {
		return taskRecord{}, err
	}
	var created pluginapi.SessionCreateResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionCreate, params, &created); err != nil {
		return taskRecord{}, err
	}
	record.SessionID = created.SessionID
	index.Records = append(index.Records, record)
	if err := storeTaskIndexLocked(ctx, host, index); err != nil {
		return taskRecord{}, err
	}
	return record, nil
}

// Recheck cached activity only under capacity pressure. A created record may
// still be between registration and send, so it must retain its reserved slot.
func refreshTaskIndex(ctx context.Context, host pluginapi.Host, index *taskIndex) error {
	var listed pluginapi.SessionListResult
	if err := host.CallHost(ctx, pluginapi.HostServiceSessionList, pluginapi.SessionListParams{Scope: pluginapi.SessionListScopeOwned}, &listed); err != nil {
		return err
	}
	owned := make(map[string]bool, len(listed.Sessions))
	for _, child := range listed.Sessions {
		owned[child.SessionID] = true
	}
	kept := index.Records[:0]
	for _, record := range index.Records {
		if record.State != "created" && !terminalTaskState(record.State) {
			if !owned[record.SessionID] {
				continue
			}
			var inspected pluginapi.SessionInspectResult
			if err := host.CallHost(ctx, pluginapi.HostServiceSessionInspect, pluginapi.SessionInspectParams{SessionID: record.SessionID, RequestID: record.RequestID, Wait: pluginapi.SessionInspectWaitNone}, &inspected); err != nil {
				return err
			}
			if inspected.Turn != nil {
				record.State = inspected.Turn.State
			} else if terminalTaskState(inspected.Session.State) {
				// Legacy records may have no correlated request left in the host.
				continue
			}
		}
		kept = append(kept, record)
	}
	index.Records = kept
	return nil
}

func trimTaskIndex(index *taskIndex, limit int) error {
	excess := len(index.Records) - limit
	if excess <= 0 {
		return nil
	}
	trimmed := index.Records[:0]
	for _, record := range index.Records {
		if excess > 0 && terminalTaskState(record.State) {
			excess--
			continue
		}
		trimmed = append(trimmed, record)
	}
	index.Records = trimmed
	if excess > 0 {
		return fmt.Errorf("subagent task record capacity (%d) is full; unfinished records must be resolved before creating more tasks", maxTaskRecords)
	}
	return nil
}

func saveRecord(ctx context.Context, host pluginapi.Host, record taskRecord) error {
	taskIndexMu.Lock()
	defer taskIndexMu.Unlock()
	index, err := loadTaskIndexLocked(ctx, host)
	if err != nil {
		return err
	}
	filtered := index.Records[:0]
	for _, existing := range index.Records {
		if existing.SessionID == record.SessionID || (record.RequestID != "" && existing.RequestID == record.RequestID) {
			continue
		}
		filtered = append(filtered, existing)
	}
	index.Records = append(filtered, record)
	if err := trimTaskIndex(&index, maxTaskRecords); err != nil {
		return err
	}
	return storeTaskIndexLocked(ctx, host, index)
}

func loadRecord(ctx context.Context, host pluginapi.Host, requestID string) (taskRecord, bool, error) {
	taskIndexMu.Lock()
	defer taskIndexMu.Unlock()
	index, err := loadTaskIndexLocked(ctx, host)
	if err != nil {
		return taskRecord{}, false, err
	}
	requestID = strings.TrimSpace(requestID)
	for _, record := range index.Records {
		if record.RequestID == requestID {
			return record, true, nil
		}
	}
	return taskRecord{}, false, nil
}
func loadRecordBySession(ctx context.Context, host pluginapi.Host, sessionID string) (taskRecord, bool, error) {
	taskIndexMu.Lock()
	defer taskIndexMu.Unlock()
	index, err := loadTaskIndexLocked(ctx, host)
	if err != nil {
		return taskRecord{}, false, err
	}
	sessionID = strings.TrimSpace(sessionID)
	for _, record := range index.Records {
		if record.SessionID == sessionID {
			return record, true, nil
		}
	}
	return taskRecord{}, false, nil
}

func loadStoredRecord(ctx context.Context, host pluginapi.Host, key string) (taskRecord, bool, error) {
	var result struct {
		Value *string `json:"value"`
	}
	if err := host.CallHost(ctx, pluginapi.HostServiceStorageGet, map[string]any{"scope": "workspace", "key": key}, &result); err != nil {
		return taskRecord{}, false, err
	}
	if result.Value == nil {
		return taskRecord{}, false, nil
	}
	var record taskRecord
	if err := json.Unmarshal([]byte(*result.Value), &record); err != nil {
		return taskRecord{}, false, err
	}
	return record, true, nil
}

func deleteRecord(ctx context.Context, host pluginapi.Host, sessionID string) error {
	taskIndexMu.Lock()
	defer taskIndexMu.Unlock()
	index, err := loadTaskIndexLocked(ctx, host)
	if err != nil {
		return err
	}
	filtered := index.Records[:0]
	for _, record := range index.Records {
		if record.SessionID != strings.TrimSpace(sessionID) {
			filtered = append(filtered, record)
		}
	}
	index.Records = filtered
	return storeTaskIndexLocked(ctx, host, index)
}

func loadTaskIndexLocked(ctx context.Context, host pluginapi.Host) (taskIndex, error) {
	var result struct {
		Value *string `json:"value"`
	}
	if err := host.CallHost(ctx, pluginapi.HostServiceStorageGet, map[string]any{"scope": "workspace", "key": taskIndexKey}, &result); err != nil {
		return taskIndex{}, err
	}
	if result.Value != nil {
		var index taskIndex
		if err := json.Unmarshal([]byte(*result.Value), &index); err != nil {
			return taskIndex{}, err
		}
		return index, nil
	}
	return migrateLegacyTaskRecordsLocked(ctx, host)
}

func migrateLegacyTaskRecordsLocked(ctx context.Context, host pluginapi.Host) (taskIndex, error) {
	var keys struct {
		Keys []string `json:"keys"`
	}
	if err := host.CallHost(ctx, pluginapi.HostServiceStorageKeys, map[string]any{"scope": "workspace"}, &keys); err != nil {
		return taskIndex{}, err
	}
	index := taskIndex{Records: make([]taskRecord, 0)}
	seen := make(map[string]struct{})
	for _, key := range keys.Keys {
		if !strings.HasPrefix(key, "session.") {
			continue
		}
		record, ok, err := loadStoredRecord(ctx, host, key)
		if err != nil {
			return taskIndex{}, err
		}
		if ok {
			if _, exists := seen[record.SessionID]; !exists {
				seen[record.SessionID] = struct{}{}
				index.Records = append(index.Records, record)
			}
		}
	}
	for _, key := range keys.Keys {
		if strings.HasPrefix(key, "session.") || strings.HasPrefix(key, "request.") {
			if err := host.CallHost(ctx, pluginapi.HostServiceStorageDelete, map[string]any{"scope": "workspace", "key": key}, &struct{}{}); err != nil {
				return taskIndex{}, err
			}
		}
	}
	if len(index.Records) > maxTaskRecords {
		index.Records = index.Records[len(index.Records)-maxTaskRecords:]
	}
	if err := storeTaskIndexLocked(ctx, host, index); err != nil {
		return taskIndex{}, err
	}
	return index, nil
}

func storeTaskIndexLocked(ctx context.Context, host pluginapi.Host, index taskIndex) error {
	encoded, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return host.CallHost(ctx, pluginapi.HostServiceStorageSet, map[string]any{"scope": "workspace", "key": taskIndexKey, "value": string(encoded)}, &struct{}{})
}

func terminalTaskState(state string) bool {
	switch strings.TrimSpace(state) {
	case "completed", "failed", "interrupted", "discarded":
		return true
	default:
		return false
	}
}

func parentContinuationRecord(ctx context.Context, host pluginapi.Host, child taskRecord, requestID string) (taskRecord, taskRecord, bool, error) {
	parentID := strings.TrimSpace(child.ParentSessionID)
	if parentID == "" {
		return taskRecord{}, taskRecord{}, false, nil
	}
	parent, ok, err := loadRecordBySession(ctx, host, parentID)
	if err != nil {
		return taskRecord{}, taskRecord{}, false, err
	}
	if !ok || strings.TrimSpace(parent.SessionID) != parentID || strings.TrimSpace(parent.ParentSessionID) == "" {
		return taskRecord{}, taskRecord{}, false, nil
	}
	return taskRecord{
		SessionID:       parentID,
		ParentSessionID: parent.ParentSessionID,
		Name:            parent.Name,
		RequestID:       requestID,
		State:           "created",
	}, parent, true, nil
}

func completionDeliveryRequestID(requestID string) string {
	digest := sha256.Sum256([]byte("subagent.completion\x00" + strings.TrimSpace(requestID)))
	return "deliver-" + hex.EncodeToString(digest[:12])
}

func newRequestID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
func lifecycleLabel(state string) string {
	if state == "completed" {
		return "完成"
	}
	if state == "failed" {
		return "失败"
	}
	if state == "interrupted" {
		return "中断"
	}
	return "取消"
}

func deriveTaskName(description string) string {
	name := taskNameCleaner.ReplaceAllString(strings.ToLower(strings.TrimSpace(description)), "_")
	name = strings.Trim(name, "_")
	if len(name) > 40 {
		name = strings.TrimRight(name[:40], "_")
	}
	if name == "" {
		return "task"
	}
	return name
}
func stringField(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}
func spawnSchema() map[string]any {
	return objectSchema(map[string]any{"description": stringField("Short 3-5 word summary of what the agent will do."), "prompt": stringField("Concrete, self-contained task brief with scope, constraints, acceptance criteria, and deliverable."), "subagent_type": stringField("Optional specialized agent type."), "name": stringField("Optional addressable task name using lowercase letters, digits, and underscores."), "model": stringField("Optional configured model alias or host capability model such as @verification."), "context": map[string]any{"type": "string", "enum": []string{"fresh", "fork"}, "description": "Optional conversation context source. Defaults to fresh; fork inherits the parent conversation."}, "isolation": map[string]any{"type": "string", "enum": []string{"worktree"}}, "run_in_background": map[string]any{"type": "boolean", "default": true, "description": "Return immediately when true. Set false when the parent must wait for this result before continuing; a foreground wait automatically becomes background after ten minutes."}}, "description", "prompt")
}

func workerInstructions(workerType string) string {
	instructions := `Work as a bounded child task. Complete only the assigned scope, preserve unrelated work, verify applicable claims, and report exact changed files, commands, blockers, and evidence. Your final message is the deliverable the parent receives.`
	if strings.TrimSpace(workerType) == "worker" {
		instructions = `Implement only the assigned scoped change in the provided isolated workspace. Preserve unrelated work, verify locally, and report exact changed files, commands, blockers, and evidence. Your final message is the deliverable the parent receives.`
	}
	return instructions
}

const promptSection = `# Subagent results

A completed subagent task does not mean the overall task is complete. Integrate the result and verify the overall work before claiming completion.

# Delegation

The main agent owns the user conversation, final synthesis, and decision about whether delegation is worth the overhead. Keep tightly coupled or trivial work local. Delegate bounded research, verification, or disjoint implementation when separate context materially improves the result. Set run_in_background=false when your next step depends on the result so it returns in the current tool call. Use background mode for independent work you can overlap. A foreground wait that exceeds ten minutes automatically becomes background and completes through the normal notification path.

The Subagent plugin owns its task records, cancellation propagation, concurrency policy, and recovery. Use the public Session services to build whatever task topology fits the work; the host does not impose a delegation tree or a global child limit.

Child sessions use fresh conversation context by default while retaining installed plugins and workspace capabilities. Fresh task prompts must be self-contained. Use context=fork only when inherited conversation history is materially necessary, and still provide a concrete directive and scope. Child completion is delivered as a read-only query bubble; do not poll while work is running.`
