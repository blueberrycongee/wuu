package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Contract failures: accepting a nonexistent/unpersisted cutoff, leaking later
// messages or compaction into an older fork, copying source execution ownership,
// changing the selected configuration, or replacing a child after a lost reply.
func TestPluginSessionHistoricalFork(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "fork-fixture", calls: make(chan pluginhost.AgentTurnLifecycleInput, 2)}
	attachTestModelGeneration(t, rt, owner)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, context.Background())
	parent, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "fork-parent", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh, Instructions: "parent configuration marker", ToolPolicy: &pluginhost.SessionToolPolicy{Deny: []string{"unavailable-tool"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.BindDriver(parent.SessionID, pluginprotocol.DriverBinding{Profile: "cloud", ProviderID: "fork-fixture", DriverID: "fork-fixture", DriverVersion: "1", ContractVersion: 1, HistoryState: true}); err != nil {
		t.Fatal(err)
	}
	original, found, err := rt.Sessions.Find(parent.SessionID)
	if err != nil || !found {
		t.Fatal(err)
	}
	records := []session.HistoryRecord{{Role: "user", Content: "included-before-fork"}, {Role: "assistant", Content: "selected-fork-answer"}, {Role: "user", Content: "must-not-leak-after-fork"}}
	if err := rt.Sessions.AppendHistoryRecords(parent.SessionID, records); err != nil {
		t.Fatal(err)
	}
	history, err := rt.Sessions.LoadHistoryRecords(parent.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := history[1].Seq
	if err := rt.Sessions.RewriteHistoryRecordsAtBaseline(parent.SessionID, []session.HistoryRecord{{Role: "system", Content: "later-compacted-context"}}, history[len(history)-1].Seq); err != nil {
		t.Fatal(err)
	}
	source, found, err := rt.Sessions.Find(parent.SessionID)
	if err != nil || !found {
		t.Fatal(err)
	}
	// A later configuration change must not leak backward into the fork.
	source, err = rt.Sessions.SetRuntimeSelection(parent.SessionID, session.RuntimeSelection{
		Provider: source.Provider, Model: source.Model, Variant: "current-variant", Effort: "high",
		Speed: "fast", ModelOptions: map[string]string{"test-option": "current-value"},
		PermissionMode: source.PermissionMode, ApproveForMe: source.ApproveForMe,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := pluginhost.SessionCreateParams{RequestID: "historical-fork", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFork, ParentSessionID: parent.SessionID, ForkSeq: &cutoff}
	child, err := rt.PluginSessionRouter.Create(ctx, owner.id, params)
	if err != nil {
		t.Fatal(err)
	}
	fork, found, err := rt.Sessions.Find(child.SessionID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if child.SessionID == parent.SessionID || fork.EngineRef != "" || fork.CWD != source.CWD || fork.Provider != source.Provider || fork.Model != source.Model || fork.Variant != original.Variant || fork.Effort != original.Effort || fork.Speed != original.Speed || !reflect.DeepEqual(fork.ModelOptions, original.ModelOptions) || fork.Instructions != source.Instructions || fork.ToolPolicyJSON != source.ToolPolicyJSON {
		t.Fatalf("fork identity/configuration mismatch: source=%+v child=%+v", source, fork)
	}
	copied, err := rt.Sessions.LoadHistoryRecords(child.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for _, record := range copied {
		text += record.Content + "\n"
		if record.ToolInvocationID != "" {
			t.Fatal("fork copied source execution ownership")
		}
	}
	if !strings.Contains(text, "included-before-fork") || !strings.Contains(text, "selected-fork-answer") || strings.Contains(text, "must-not-leak") || strings.Contains(text, "later-compacted") {
		t.Fatalf("wrong historical prefix: %s", text)
	}
	repeated, err := rt.PluginSessionRouter.Create(ctx, owner.id, params)
	if err != nil || repeated.Created || repeated.SessionID != child.SessionID {
		t.Fatalf("lost reply changed fork: %+v %v", repeated, err)
	}
	after, err := rt.Sessions.LoadHistoryRecords(parent.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) < len(history) || !reflect.DeepEqual(after[:len(history)], history) {
		t.Fatal("fork mutated parent archive")
	}
	for _, bad := range []int{-1, 0, history[len(history)-1].Seq + 100} {
		invalid := params
		invalid.RequestID = "invalid-fork-" + time.Duration(bad).String()
		invalid.ForkSeq = &bad
		if _, err := rt.PluginSessionRouter.Create(ctx, owner.id, invalid); err == nil {
			t.Fatalf("accepted invalid fork cutoff %d", bad)
		}
	}
	wrongMode := params
	wrongMode.RequestID = "fork-cutoff-with-fresh"
	wrongMode.ContextSource = pluginhost.SessionContextFresh
	if _, err := rt.PluginSessionRouter.Create(ctx, owner.id, wrongMode); err == nil {
		t.Fatal("fresh creation accepted historical cutoff")
	}
}

type durableForkModel struct {
	*durableChildGate
	requestsMu sync.Mutex
	requests   map[string]providers.ChatRequest
}

func (m *durableForkModel) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	prompt := ""
	for _, message := range req.Messages {
		if message.Role == "user" && !strings.HasPrefix(message.Content, "<system-reminder>") {
			prompt = message.Content
		}
	}
	m.requestsMu.Lock()
	m.requests[prompt] = req
	m.requestsMu.Unlock()
	return m.durableChildGate.StreamChat(ctx, req)
}

// Real plugin/model acceptance: explicit fork uses archived sequence, while the
// child request owns a fresh provider conversation key and no source checkpoint.
func TestDurableCloudHistoricalFork(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, outputs: map[string]string{"included-parent-prompt": "selected-parent-answer"}}
	model := &durableForkModel{durableChildGate: gate, requests: map[string]providers.ChatRequest{}}
	j := newDurableControlJourney(t, "historical-fork-owner", model)
	j.start(t, "", nil, "included-parent-prompt")
	j.completed(t, 1)
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, false)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := 0
	for _, record := range history {
		if record.Role == "assistant" && record.Content == "selected-parent-answer" {
			cutoff = record.Seq
		}
	}
	if cutoff == 0 {
		t.Fatal("missing durable fork point")
	}
	j.start(t, "", nil, "excluded-parent-later-prompt")
	j.completed(t, 2)
	args, _ := json.Marshal(map[string]any{"prompt": "historical-fork-child", "fork_seq": cutoff})
	gate.mu.Lock()
	gate.calls["launch-historical-fork"] = &providers.ToolCall{ID: "historical-fork", Name: j.names["durable_subagent"], Arguments: string(args)}
	gate.mu.Unlock()
	j.start(t, "", nil, "launch-historical-fork")
	j.completed(t, 4)
	var child map[string]any
	for _, value := range durableChildStoredTasks(t, j) {
		task := value.(map[string]any)
		if task["kind"] == "subagent" {
			child = task
		}
	}
	if child == nil {
		t.Fatal("no fork child")
	}
	childID := child["child_session_id"].(string)
	model.requestsMu.Lock()
	request, ok := model.requests["historical-fork-child"]
	parentRequest := model.requests["included-parent-prompt"]
	model.requestsMu.Unlock()
	if !ok {
		t.Fatal("fork child never ran")
	}
	text := ""
	for _, message := range request.Messages {
		text += message.Content + "\n"
	}
	if !strings.Contains(text, "included-parent-prompt") || !strings.Contains(text, "selected-parent-answer") || strings.Contains(text, "excluded-parent-later-prompt") || strings.Contains(text, "launch-historical-fork") {
		t.Fatalf("fork model context is not exact prefix: %s", text)
	}
	if request.CacheHint == nil || parentRequest.CacheHint == nil || request.CacheHint.PromptCacheKey != childID || parentRequest.CacheHint.PromptCacheKey != j.id || (request.TurnRouting != nil && request.TurnRouting == parentRequest.TurnRouting) {
		t.Fatalf("fork shared provider identity: parent=%+v child=%+v", parentRequest.CacheHint, request.CacheHint)
	}
	childView := *j
	childView.id = childID
	for _, value := range durableChildStoredTasks(t, &childView) {
		task := value.(map[string]any)
		if task["kind"] == "run" && task["run_id"] == child["run_id"] {
			t.Fatal("fork reused source logical checkpoint")
		}
	}
	childHistory, err := j.rt.Sessions.LoadHistoryRecords(childID, false)
	if err != nil {
		t.Fatal(err)
	}
	parentRecord, _, err := j.rt.Sessions.Find(j.id)
	if err != nil {
		t.Fatal(err)
	}
	childRecord, _, err := j.rt.Sessions.Find(childID)
	if err != nil {
		t.Fatal(err)
	}
	if childRecord.ContextSource != "fork" || childRecord.ParentID != j.id || childRecord.Provider != parentRecord.Provider || childRecord.Model != parentRecord.Model {
		t.Fatalf("fork creation metadata: %+v", childRecord)
	}
	durableCompletionEvidence(t, j, gate, "cloud-historical-fork", map[string]any{"fork_seq": cutoff, "child_session_id": childID, "child_history": childHistory, "parent_cache_key": parentRequest.CacheHint.PromptCacheKey, "child_cache_key": request.CacheHint.PromptCacheKey, "configuration": "entry-bound agent configuration; current execution admission"})
}

// The first user entry must already have proof when it is admitted, rather than
// acquiring a backdated snapshot from the first model request or answer.
func TestDurableCloudHistoricalFirstInputFork(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, outputs: map[string]string{"first-input": "must-not-be-in-fork"}}
	model := &durableForkModel{durableChildGate: gate, requests: map[string]providers.ChatRequest{}}
	j := newDurableControlJourney(t, "first-input-fork-owner", model)
	j.start(t, "", nil, "first-input")
	j.completed(t, 1)
	records, err := j.rt.Sessions.LoadHistoryRecords(j.id, false)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := 0
	for _, record := range records {
		if record.Role == "user" && record.Content == "first-input" {
			cutoff = record.Seq
			if record.HistoricalState == nil {
				t.Fatal("first input has no historical state")
			}
		}
	}
	if cutoff == 0 {
		t.Fatal("first input not persisted")
	}
	args, _ := json.Marshal(map[string]any{"prompt": "first-input-child", "fork_seq": cutoff})
	gate.mu.Lock()
	gate.calls["launch-first-input-fork"] = &providers.ToolCall{ID: "first-input-fork", Name: j.names["durable_subagent"], Arguments: string(args)}
	gate.mu.Unlock()
	j.start(t, "", nil, "launch-first-input-fork")
	j.completed(t, 3)
	model.requestsMu.Lock()
	request, ok := model.requests["first-input-child"]
	model.requestsMu.Unlock()
	if !ok {
		t.Fatal("first input fork did not execute")
	}
	text := ""
	for _, msg := range request.Messages {
		text += msg.Content + "\n"
	}
	if !strings.Contains(text, "first-input") || strings.Contains(text, "must-not-be-in-fork") || strings.Contains(text, "launch-first-input-fork") {
		t.Fatalf("first-input prefix mismatch: %s", text)
	}
	durableCompletionEvidence(t, j, gate, "cloud-historical-first-input-fork", map[string]any{"fork_seq": cutoff, "first_input_snapshot": true})
}

func TestHistoricalForkKeepsCurrentToolRestrictions(t *testing.T) {
	policy, err := intersectHistoricalToolPolicy(`{"allow":["read","write"]}`, `{"allow":["read"],"deny":["write"]}`, &hostSessionToolPolicy{Allow: []string{"read", "write"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy.Allow, []string{"read"}) || !reflect.DeepEqual(policy.Deny, []string{"write"}) {
		t.Fatalf("historical policy restored revoked tool: %+v", policy)
	}
	if _, err := intersectHistoricalToolPolicy(`{"allow":["write"]}`, `{"allow":["read"]}`, nil); err == nil {
		t.Fatal("empty intersection became unrestricted")
	}
}

// Public installed-plugin acceptance: two real code-mode programs overwrite the
// parent's store. A child fork must execute load() against the older entry state
// and retain that entry's model options rather than the parent's latest pin.
func TestDurableCloudHistoricalCodeStore(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, outputs: map[string]string{}}
	model := &durableForkModel{durableChildGate: gate, requests: map[string]providers.ChatRequest{}}
	j := newConfiguredDurableControlJourney(t, "historical-code-store", func(cfg *config.Config) {
		cfg.PTC.Enabled = true
		cfg.Agent.PermissionMode = config.PermissionModeUnconfined
	}, model)
	if _, err := j.rt.Sessions.SetRuntimeSelection(j.id, session.RuntimeSelection{Provider: "fixture", Model: "fixture", Effort: "low", PermissionMode: config.PermissionModeUnconfined}); err != nil {
		t.Fatal(err)
	}
	setCall := func(prompt, code string) {
		raw, _ := json.Marshal(map[string]string{"input": code})
		gate.mu.Lock()
		gate.calls[prompt] = &providers.ToolCall{ID: prompt, Name: "run_code", Arguments: string(raw)}
		gate.mu.Unlock()
	}
	setCall("store-old", `store("fork-value", "old-value"); text(load("fork-value"));`)
	j.start(t, "", nil, "store-old")
	j.completed(t, 1)
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, false)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := 0
	for _, record := range history {
		if record.Role == "tool" && record.ToolCallID == "store-old" {
			cutoff = record.Seq
			if record.HistoricalState == nil || len(record.HistoricalState.Stores) == 0 {
				t.Fatalf("program state was not projected: %+v", record)
			}
		}
	}
	if cutoff == 0 {
		t.Fatalf("old program result missing: %+v", history)
	}
	if _, err := j.rt.Sessions.SetRuntimeSelection(j.id, session.RuntimeSelection{Provider: "fixture", Model: "fixture", Effort: "high", PermissionMode: config.PermissionModeUnconfined}); err != nil {
		t.Fatal(err)
	}
	setCall("store-new", `store("fork-value", "new-value"); text(load("fork-value"));`)
	j.start(t, "", nil, "store-new")
	j.completed(t, 2)
	setCall("read-fork-value", `text(load("fork-value"));`)
	args, _ := json.Marshal(map[string]any{"prompt": "read-fork-value", "fork_seq": cutoff})
	gate.mu.Lock()
	gate.calls["fork-old-store"] = &providers.ToolCall{ID: "fork-old-store", Name: j.names["durable_subagent"], Arguments: string(args)}
	gate.mu.Unlock()
	j.start(t, "", nil, "fork-old-store")
	j.completed(t, 3)
	childID := ""
	for _, value := range durableChildStoredTasks(t, j) {
		task := value.(map[string]any)
		if task["kind"] == "subagent" {
			childID, _ = task["child_session_id"].(string)
		}
	}
	if childID == "" {
		t.Fatal("historical code child was not created")
	}
	childView := *j
	childView.id = childID
	childView.completed(t, 1)
	j.completed(t, 4)
	child, _, err := j.rt.Sessions.Find(childID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Effort != "low" {
		t.Fatalf("child inherited latest model configuration: %+v", child)
	}
	records, err := j.rt.Sessions.LoadHistoryRecords(childID, false)
	if err != nil {
		t.Fatal(err)
	}
	loaded := ""
	for _, record := range records {
		if record.Role == "tool" && record.ToolCallID == "read-fork-value" {
			loaded = record.Content
		}
	}
	if !strings.Contains(loaded, "old-value") || strings.Contains(loaded, "new-value") {
		t.Fatalf("fork load() saw wrong state: %q", loaded)
	}
	durableCompletionEvidence(t, j, gate, "cloud-historical-code-store", map[string]any{"fork_seq": cutoff, "child_session_id": childID, "child_effort": child.Effort, "load_output": loaded})
}
