package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	pluginapi "github.com/blueberrycongee/wuu/packages/plugin-go"
)

type capturedCall struct {
	method string
	params map[string]any
}

// taskStore emulates the host storage contract the Subagent plugin relies on:
// one central tasks.v2 index whose value is a JSON-encoded taskIndex. It keeps
// state across calls so load/migrate/store round-trips behave like the real
// host instead of resetting on every read.
type taskStore struct {
	index *taskIndex
}

func (s *taskStore) resetWith(records ...taskRecord) {
	s.index = &taskIndex{Records: append([]taskRecord(nil), records...)}
}

func (s *taskStore) recordBySession(sessionID string) (taskRecord, bool) {
	if s.index == nil {
		return taskRecord{}, false
	}
	for _, record := range s.index.Records {
		if record.SessionID == sessionID {
			return record, true
		}
	}
	return taskRecord{}, false
}

// handle serves the storage methods used by the plugin and reports whether it
// consumed the call.
func (s *taskStore) handle(method string, params map[string]any, result any) (bool, error) {
	switch method {
	case pluginapi.HostServiceStorageGet:
		key, _ := params["key"].(string)
		if key == taskIndexKey && s.index != nil {
			encoded, err := json.Marshal(s.index)
			if err != nil {
				return true, err
			}
			value := string(encoded)
			return true, decodeInto(map[string]any{"value": &value}, result)
		}
		return true, decodeInto(map[string]any{"value": nil}, result)
	case pluginapi.HostServiceStorageSet:
		if key, _ := params["key"].(string); key == taskIndexKey {
			var index taskIndex
			if err := json.Unmarshal([]byte(fmt.Sprint(params["value"])), &index); err != nil {
				return true, err
			}
			s.index = &index
		}
		return true, decodeInto(struct{}{}, result)
	case pluginapi.HostServiceStorageKeys:
		var keys []string
		if s.index != nil {
			keys = append(keys, taskIndexKey)
		}
		return true, decodeInto(map[string]any{"keys": keys}, result)
	case pluginapi.HostServiceStorageDelete:
		if key, _ := params["key"].(string); key == taskIndexKey {
			s.index = nil
		}
		return true, decodeInto(struct{}{}, result)
	}
	return false, nil
}

type captureHost struct {
	calls         []capturedCall
	store         taskStore
	inspectResult *pluginapi.SessionInspectResult
}

type lifecycleHost struct {
	captureHost
	record taskRecord
}

type interruptHost struct {
	captureHost
	record taskRecord
}

func newLifecycleHost(record taskRecord) *lifecycleHost {
	host := &lifecycleHost{record: record}
	host.store.resetWith(record)
	return host
}

func newInterruptHost(record taskRecord) *interruptHost {
	host := &interruptHost{record: record}
	host.store.resetWith(record)
	return host
}

func (h *lifecycleHost) CallHost(ctx context.Context, method string, params, result any) error {
	if method == pluginapi.HostServiceSessionList {
		return decodeInto(pluginapi.SessionListResult{Sessions: []pluginapi.SessionSummary{{
			SessionID: h.record.SessionID, ParentSessionID: h.record.ParentSessionID, Name: h.record.Name, State: h.record.State,
		}}}, result)
	}
	return h.captureHost.CallHost(ctx, method, params, result)
}

func (h *interruptHost) CallHost(ctx context.Context, method string, params, result any) error {
	switch method {
	case pluginapi.HostServiceSessionList:
		return decodeInto(pluginapi.SessionListResult{Sessions: []pluginapi.SessionSummary{{
			SessionID: h.record.SessionID, ParentSessionID: h.record.ParentSessionID, Name: h.record.Name, State: h.record.State,
		}}}, result)
	case pluginapi.HostServiceSessionCancel:
		var input pluginapi.SessionCancelParams
		raw, _ := json.Marshal(params)
		_ = json.Unmarshal(raw, &input)
		if err := h.captureHost.CallHost(ctx, method, params, result); err != nil {
			return err
		}
		return decodeInto(pluginapi.SessionCancelResult{SessionID: input.SessionID, TurnID: input.TurnID, Cancelled: true}, result)
	}
	return h.captureHost.CallHost(ctx, method, params, result)
}

func (h *captureHost) InitializeParams() pluginapi.InitializeParams {
	return pluginapi.InitializeParams{}
}

func (h *captureHost) CallHost(_ context.Context, method string, params, result any) error {
	raw, _ := json.Marshal(params)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	h.calls = append(h.calls, capturedCall{method: method, params: decoded})
	if handled, err := h.store.handle(method, decoded, result); handled {
		return err
	}
	response := `{}`
	switch method {
	case pluginapi.HostServiceSessionList:
		listed := pluginapi.SessionListResult{}
		if h.store.index != nil {
			for _, r := range h.store.index.Records {
				listed.Sessions = append(listed.Sessions, pluginapi.SessionSummary{SessionID: r.SessionID, State: r.State})
			}
		}
		return decodeInto(listed, result)
	case pluginapi.HostServiceSessionCreate:
		response = `{"session_id":"child-1","created":true}`
	case pluginapi.HostServiceSessionSend:
		response = `{"state":"running","session_id":"child-1","turn_id":"turn-1"}`
	case pluginapi.HostServiceSessionInspect:
		if h.inspectResult != nil {
			return decodeInto(*h.inspectResult, result)
		}
	}
	return json.Unmarshal([]byte(response), result)
}

func TestHandlerOwnsSubagentToolsAndPrompt(t *testing.T) {
	handler := Handler()
	if foregroundAwaitBudgetMS != 10*60*1000 {
		t.Fatalf("foreground wait budget = %d, want ten minutes", foregroundAwaitBudgetMS)
	}
	if len(handler.Definition.Tools) != 3 {
		t.Fatalf("tools = %+v", handler.Definition.Tools)
	}
	services := map[string]bool{}
	for _, service := range handler.Definition.RequiredHostServices {
		services[service.ID] = true
	}
	for _, want := range []string{pluginapi.HostServiceSessionCreate, pluginapi.HostServiceSessionSend, pluginapi.HostServiceSessionList, pluginapi.HostServiceSessionCancel} {
		if !services[want] {
			t.Fatalf("missing host service %s: %+v", want, handler.Definition.RequiredHostServices)
		}
	}
	for _, tool := range handler.Definition.Tools {
		if len(tool.ExecutionScopes) != 2 || tool.ExecutionScopes[0] != "root" || tool.ExecutionScopes[1] != "collaboration" {
			t.Fatalf("tool %q scopes = %v", tool.ID, tool.ExecutionScopes)
		}
	}
	raw, err := handler.InvokeCapability(context.Background(), &captureHost{}, pluginapi.CapabilityCall{Capability: capabilityPrompt})
	if err != nil || len(raw) == 0 {
		t.Fatalf("prompt capability = %s, %v", raw, err)
	}
	if !strings.Contains(string(raw), "ten minutes") || strings.Contains(string(raw), "five minutes") {
		t.Fatalf("prompt does not describe the ten-minute foreground budget: %s", raw)
	}
	schema, err := json.Marshal(handler.Definition.Tools[0].InputSchema)
	if err != nil || !strings.Contains(string(schema), "ten minutes") || strings.Contains(string(schema), "five minutes") {
		t.Fatalf("spawn schema does not describe the ten-minute foreground budget: %s, %v", schema, err)
	}
}

func TestSpawnComposesPublicSessionServices(t *testing.T) {
	host := &captureHost{}
	result, err := executeTool(context.Background(), host, pluginapi.ToolCall{ToolID: "spawn_agent", SessionID: "parent-1", Arguments: json.RawMessage(`{"description":"Review parser","prompt":"Inspect and report.","subagent_type":"general-purpose","model":"cheap","run_in_background":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	var create *capturedCall
	for i := range host.calls {
		if host.calls[i].method == pluginapi.HostServiceSessionCreate {
			create = &host.calls[i]
		}
	}
	if create == nil || create.params["parent_session_id"] != "parent-1" || create.params["context_source"] != "fresh" || create.params["model_alias"] != "cheap" {
		t.Fatalf("create = %+v", host.calls)
	}
	var send *capturedCall
	for index := range host.calls {
		if host.calls[index].method == pluginapi.HostServiceSessionSend {
			send = &host.calls[index]
		}
	}
	if send == nil || send.params["session_id"] != "child-1" || send.params["cause"] != "subagent.task" {
		t.Fatalf("send = %+v, calls = %+v", send, host.calls)
	}
	inputValue, _ := send.params["input"].(map[string]any)
	if !strings.Contains(fmt.Sprint(inputValue["prompt"]), "Inspect and report.") {
		t.Fatalf("send input = %+v", inputValue)
	}
	record, ok := host.store.recordBySession("child-1")
	if !ok || record.ParentSessionID != "parent-1" || record.State != "running" || record.TurnID != "turn-1" || !strings.HasPrefix(record.RequestID, "turn-") {
		t.Fatalf("persisted record = %+v", record)
	}
	if len(result.Content) != 1 || result.Content[0].Text == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestSpawnForegroundWaitsForTerminalResultWithoutDuplicateDelivery(t *testing.T) {
	host := &captureHost{inspectResult: &pluginapi.SessionInspectResult{Turn: &pluginapi.SessionTurnInspection{
		RequestID: "child-request", State: "completed", TurnID: "turn-1", FinalOutput: "parser is correct",
	}}}
	result, err := executeTool(context.Background(), host, pluginapi.ToolCall{
		ToolID: "spawn_agent", SessionID: "parent-1", TurnID: "parent-turn-1",
		Arguments: json.RawMessage(`{"description":"Review parser","prompt":"Inspect and report.","run_in_background":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `"backgrounded":false`) || !strings.Contains(result.Content[0].Text, `"final_output":"parser is correct"`) {
		t.Fatalf("result = %+v", result)
	}
	var inspect *capturedCall
	for index := range host.calls {
		if host.calls[index].method == pluginapi.HostServiceSessionInspect {
			inspect = &host.calls[index]
		}
	}
	if inspect == nil || inspect.params["wait"] != pluginapi.SessionInspectWaitTerminal || inspect.params["timeout_ms"] != float64(foregroundAwaitBudgetMS) || !strings.HasPrefix(fmt.Sprint(inspect.params["request_id"]), "turn-") {
		t.Fatalf("inspect = %+v, calls = %+v", inspect, host.calls)
	}
	record, ok := host.store.recordBySession("child-1")
	if !ok || !record.SuppressCompletion || record.State != "completed" {
		t.Fatalf("persisted record = %+v", record)
	}

	input, _ := json.Marshal(pluginapi.TurnLifecycleInput{RequestID: record.RequestID, State: "completed", ThreadID: "child-1", FinalOutput: "parser is correct"})
	if _, err := invokeCapability(context.Background(), host, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: input}); err != nil {
		t.Fatal(err)
	}
	for _, call := range host.calls {
		if call.method == pluginapi.HostServiceSessionSend && call.params["cause"] == "subagent.completion" {
			t.Fatalf("foreground completion was delivered twice: %+v", host.calls)
		}
	}
	if _, ok := host.store.recordBySession("child-1"); ok {
		t.Fatalf("foreground terminal record was not cleaned up: %+v", host.store.index)
	}
}

func TestSpawnForegroundTimeoutContinuesInBackground(t *testing.T) {
	host := &captureHost{inspectResult: &pluginapi.SessionInspectResult{
		Turn: &pluginapi.SessionTurnInspection{State: "running", TurnID: "turn-1"}, TimedOut: true,
	}}
	result, err := executeTool(context.Background(), host, pluginapi.ToolCall{
		ToolID: "spawn_agent", SessionID: "parent-1", TurnID: "parent-turn-1",
		Arguments: json.RawMessage(`{"description":"Review parser","prompt":"Inspect and report.","run_in_background":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `"backgrounded":true`) || !strings.Contains(result.Content[0].Text, `"timed_out":true`) {
		t.Fatalf("result = %+v", result)
	}
	record, ok := host.store.recordBySession("child-1")
	if !ok || record.SuppressCompletion {
		t.Fatalf("timed-out record = %+v", record)
	}

	input, _ := json.Marshal(pluginapi.TurnLifecycleInput{RequestID: record.RequestID, State: "completed", ThreadID: "child-1", FinalOutput: "parser is correct"})
	if _, err := invokeCapability(context.Background(), host, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: input}); err != nil {
		t.Fatal(err)
	}
	var delivered bool
	for _, call := range host.calls {
		if call.method == pluginapi.HostServiceSessionSend && call.params["cause"] == "subagent.completion" {
			delivered = true
		}
	}
	if !delivered {
		t.Fatalf("timed-out foreground completion was not delivered: %+v", host.calls)
	}
}

func TestTerminalLifecycleDeliversFinalOutputToParentSession(t *testing.T) {
	host := newLifecycleHost(taskRecord{SessionID: "child-1", ParentSessionID: "parent-1", Name: "review_parser", RequestID: "turn-one"})
	input, _ := json.Marshal(pluginapi.TurnLifecycleInput{RequestID: "turn-one", State: "completed", ThreadID: "child-1", FinalOutput: "parser is correct"})
	if _, err := invokeCapability(context.Background(), host, pluginapi.CapabilityCall{Capability: capabilityLifecycle, Input: input}); err != nil {
		t.Fatal(err)
	}
	var delivery *capturedCall
	for index := range host.calls {
		if host.calls[index].method == pluginapi.HostServiceSessionSend {
			delivery = &host.calls[index]
		}
	}
	if delivery == nil || delivery.params["session_id"] != "parent-1" || delivery.params["cause"] != "subagent.completion" {
		t.Fatalf("delivery = %+v, calls = %+v", delivery, host.calls)
	}
	inputValue, _ := delivery.params["input"].(map[string]any)
	if !strings.Contains(fmt.Sprint(inputValue["prompt"]), "parser is correct") {
		t.Fatalf("delivery prompt = %+v", inputValue)
	}
}

func TestParentTurnInterruptionCancelsOnlyItsCurrentChildTurn(t *testing.T) {
	host := newInterruptHost(taskRecord{
		SessionID: "child-1", ParentSessionID: "parent-1", ParentTurnID: "parent-turn-1",
		Name: "review_parser", RequestID: "child-request", TurnID: "child-turn-1", State: "running",
	})
	input, _ := json.Marshal(pluginapi.AgentTurnInterruptedInput{ThreadID: "parent-1", TurnID: "parent-turn-1"})
	if _, err := invokeCapability(context.Background(), host, pluginapi.CapabilityCall{Capability: capabilityInterrupt, Input: input}); err != nil {
		t.Fatal(err)
	}
	var cancel *capturedCall
	for index := range host.calls {
		if host.calls[index].method == pluginapi.HostServiceSessionCancel {
			cancel = &host.calls[index]
		}
	}
	if cancel == nil || cancel.params["session_id"] != "child-1" || cancel.params["turn_id"] != "child-turn-1" {
		t.Fatalf("cancel = %+v, calls = %+v", cancel, host.calls)
	}
}

func TestSendMessageRebindsChildTurnToCurrentParentTurn(t *testing.T) {
	host := newLifecycleHost(taskRecord{
		SessionID: "child-1", ParentSessionID: "parent-1", ParentTurnID: "parent-turn-1",
		Name: "review_parser", RequestID: "old-request", TurnID: "old-child-turn", State: "completed",
	})
	_, err := sendMessage(context.Background(), host, pluginapi.ToolCall{
		SessionID: "parent-1", TurnID: "parent-turn-2", Arguments: json.RawMessage(`{"target":"child-1","message":"continue"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := lastSavedRecord(t, host.calls, "child-1")
	if !ok {
		t.Fatalf("no persisted record for child-1, calls = %+v", host.calls)
	}
	if saved.ParentTurnID != "parent-turn-2" || saved.TurnID != "turn-1" || saved.ParentSessionID != "parent-1" {
		t.Fatalf("saved task = %+v", saved)
	}
}

func lastSavedRecord(t *testing.T, calls []capturedCall, sessionID string) (taskRecord, bool) {
	t.Helper()
	var saved taskRecord
	found := false
	for _, call := range calls {
		if call.method != pluginapi.HostServiceStorageSet || call.params["key"] != taskIndexKey {
			continue
		}
		var index taskIndex
		if err := json.Unmarshal([]byte(fmt.Sprint(call.params["value"])), &index); err != nil {
			t.Fatal(err)
		}
		for _, record := range index.Records {
			if record.SessionID == sessionID {
				saved = record
				found = true
			}
		}
	}
	return saved, found
}

func decodeInto(value any, result any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func TestSpawnReclaimsHistoricalRecordsBeforeCreatingSession(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			host := &captureHost{}
			records := make([]taskRecord, maxTaskRecords)
			for i := range records {
				records[i] = taskRecord{SessionID: fmt.Sprintf("old-%d", i), RequestID: fmt.Sprintf("request-%d", i), State: "running"}
				if !stale && i > 70 {
					records[i].State = "completed"
				}
			}
			if stale {
				host.inspectResult = &pluginapi.SessionInspectResult{Session: pluginapi.SessionSummary{State: "completed"}}
			}
			host.store.resetWith(records...)
			_, err := spawnAgent(context.Background(), host, pluginapi.ToolCall{SessionID: "parent", Arguments: json.RawMessage(`{"description":"review","prompt":"inspect"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if len(host.store.index.Records) > maxTaskRecords {
				t.Fatal("record capacity exceeded")
			}
			if record, ok := host.store.recordBySession("child-1"); !ok || record.State != "running" {
				t.Fatalf("new task not started: %+v", record)
			}
			if !stale {
				for i := 0; i <= 70; i++ {
					if _, ok := host.store.recordBySession(fmt.Sprintf("old-%d", i)); !ok {
						t.Fatal("unfinished task evicted")
					}
				}
			}
		})
	}
}

func TestSpawnAtCapacityDoesNotCreateOrSend(t *testing.T) {
	for _, state := range []string{"running", "queued", "created"} {
		t.Run(state, func(t *testing.T) {
			host := &captureHost{}
			records := make([]taskRecord, maxTaskRecords)
			for i := range records {
				records[i] = taskRecord{SessionID: fmt.Sprintf("child-%d", i), State: state}
			}
			host.store.resetWith(records...)
			_, err := spawnAgent(context.Background(), host, pluginapi.ToolCall{SessionID: "parent", Arguments: json.RawMessage(`{"description":"review","prompt":"inspect"}`)})
			if err == nil {
				t.Fatal("spawn accepted without a free slot")
			}
			for _, call := range host.calls {
				if call.method == pluginapi.HostServiceSessionCreate || call.method == pluginapi.HostServiceSessionSend {
					t.Fatalf("capacity rejection left a child session: %+v", call)
				}
			}
		})
	}
}

func TestSaveRecordEvictsTerminalRecordsAfterRunningRecords(t *testing.T) {
	host := &captureHost{}
	records := make([]taskRecord, maxTaskRecords)
	for i := range records {
		records[i] = taskRecord{SessionID: fmt.Sprintf("old-%d", i), State: "running"}
	}
	records[len(records)-1].State = "completed"
	host.store.resetWith(records...)
	if err := saveRecord(context.Background(), host, taskRecord{SessionID: "new", State: "created"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := host.store.recordBySession("new"); !ok || len(host.store.index.Records) != maxTaskRecords {
		t.Fatal("record was not admitted after reclaiming terminal entry")
	}
}

func TestConcurrentCreationReservesLastSlotOnce(t *testing.T) {
	host := &captureHost{}
	records := make([]taskRecord, maxTaskRecords-1)
	for i := range records {
		records[i] = taskRecord{SessionID: fmt.Sprintf("old-%d", i), State: "running"}
	}
	host.store.resetWith(records...)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := createTask(context.Background(), host, pluginapi.SessionCreateParams{ParentSessionID: "parent"}, taskRecord{State: "created"})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	created := 0
	for _, call := range host.calls {
		if call.method == pluginapi.HostServiceSessionCreate {
			created++
		}
	}
	if succeeded != 1 || created != 1 || len(host.store.index.Records) != maxTaskRecords {
		t.Fatalf("last slot admitted %d tasks and created %d sessions", succeeded, created)
	}
}
