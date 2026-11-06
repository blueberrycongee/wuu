package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Escaped boundaries: cancellation must reach an active owned child, must not
// reach an independent child, and canceled parent outboxes must not revive work.
// The model gate holds real inference open without wall-clock sleeps or seeded tasks.
type durableChildGate struct {
	mu       sync.Mutex
	calls    map[string]*providers.ToolCall
	outputs  map[string]string
	entered  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	once     sync.Once
	released sync.Once
	prompts  []string
}

func (m *durableChildGate) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "Summary"}, nil
}
func (m *durableChildGate) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	prompt := ""
	for _, message := range req.Messages {
		if message.Role == "user" && !strings.HasPrefix(message.Content, "<system-reminder>") {
			prompt = message.Content
		}
	}
	m.mu.Lock()
	m.prompts = append(m.prompts, prompt)
	m.mu.Unlock()
	ch := make(chan providers.StreamEvent, 3)
	if prompt == "child-private-prompt-marker" {
		m.once.Do(func() { close(m.entered) })
		go func() {
			defer close(ch)
			select {
			case <-ctx.Done():
				close(m.canceled)
			case <-m.release:
				ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "child-private-result-marker"}
				ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
			}
		}()
		return ch, nil
	}
	m.mu.Lock()
	call := m.calls[prompt]
	delete(m.calls, prompt)
	content := m.outputs[prompt]
	if content == "" {
		content = "Parent finished"
	}
	m.mu.Unlock()
	if call != nil {
		ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: call}
		ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: call}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	} else {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: content}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(ch)
	return ch, nil
}
func TestDurableCloudChildCancellation(t *testing.T) {
	for _, independent := range []bool{false, true} {
		t.Run(strconv.FormatBool(independent), func(t *testing.T) {
			model := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
			j := newDurableControlJourney(t, "cancel-child-"+strconv.FormatBool(independent), model)
			t.Cleanup(func() { model.released.Do(func() { close(model.release) }) })
			enqueue := func(prompt, tool string, args any) {
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				model.mu.Lock()
				model.calls[prompt] = &providers.ToolCall{ID: prompt, Name: j.names[tool], Arguments: string(raw)}
				model.mu.Unlock()
			}
			enqueue("launch-child", "durable_subagent", map[string]any{"prompt": "child-private-prompt-marker", "background": true, "independent": independent})
			j.start(t, "", nil, "launch-child")
			select {
			case <-model.entered:
			case <-j.ctx.Done():
				model.mu.Lock()
				prompts := append([]string(nil), model.prompts...)
				model.mu.Unlock()
				t.Fatalf("child inference never started: prompts=%q events=%s", prompts, j.out.String())
			}
			j.completed(t, 1)
			var child map[string]any
			for _, value := range durableChildStoredTasks(t, j) {
				task := value.(map[string]any)
				if task["kind"] == "subagent" {
					child = task
				}
			}
			if child == nil {
				t.Fatal("missing child task")
			}
			childID := child["child_session_id"].(string)
			owner := child["owner"].(string)
			managed, found, err := j.rt.Sessions.FindManagedByRequest("plugin:"+owner, child["request_id"].(string))
			if err != nil || !found || managed.ID != childID || managed.ParentID != j.id {
				t.Fatalf("creation receipt mismatch: %+v %v %v", managed, found, err)
			}
			records, err := j.rt.Sessions.LoadHistoryRecords(childID, true)
			if err != nil {
				t.Fatal(err)
			}
			inputs := 0
			for _, r := range records {
				if r.Role == "user" && r.Content == "child-private-prompt-marker" {
					inputs++
				}
			}
			if inputs != 1 {
				t.Fatalf("child input delivered %d times: %+v", inputs, records)
			}
			enqueue("cancel-child", "durable_cancel", map[string]any{"run_id": child["run_id"]})
			j.start(t, "", nil, "cancel-child")
			j.completed(t, 2)
			if !independent {
				select {
				case <-model.canceled:
				case <-j.ctx.Done():
					t.Fatal("explicit cancellation never reached child inference")
				}
			} else {
				select {
				case <-model.canceled:
					t.Fatal("independent child canceled with parent")
				default:
				}
				model.released.Do(func() { close(model.release) })
			}
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				for _, value := range durableChildStoredTasks(t, j) {
					task := value.(map[string]any)
					if task["id"] == child["id"] {
						child = task
					}
				}
				if (!independent && child["status"] == "aborted" && child["cancel_done"] == true) || (independent && child["status"] == "completed" && child["delivered"] == true) {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !independent && (child["status"] != "aborted" || child["delivered"] == true || child["cancel_done"] != true) {
				t.Fatalf("canceled child outbox not settled: %v", child)
			}
			if independent && (child["status"] != "completed" || child["delivered"] != true) {
				t.Fatalf("independent child did not complete: %v", child)
			}
			if independent {
				j.completed(t, 3)
				parentHistory, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
				if err != nil {
					t.Fatal(err)
				}
				results := 0
				for _, record := range parentHistory {
					if record.Role == "user" && strings.Contains(record.Content, "child-private-result-marker") {
						results++
					}
				}
				if results != 1 {
					t.Fatalf("parent received child result %d times: history=%+v child=%v", results, parentHistory, child)
				}
			}
			lifecycle, found, err := j.rt.Sessions.FindPluginTurnLifecycle(owner, child["id"].(string)+":input", childID, "")
			if err != nil || !found || lifecycle.SessionID != childID || lifecycle.TurnID == "" {
				t.Fatalf("child input lifecycle receipt missing: %+v %v %v", lifecycle, found, err)
			}
			raw, err := json.Marshal(durableChildStoredTasks(t, j))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "child-private-prompt-marker") || strings.Contains(string(raw), "child-private-result-marker") {
				t.Fatalf("task metadata duplicated transcript: %s", raw)
			}
			if dir := os.Getenv("WUU_DURABLE_EVIDENCE"); dir != "" {
				evidence, _ := json.MarshalIndent(map[string]any{"independent": independent, "creation_receipt": managed, "input_count": inputs, "tasks": json.RawMessage(raw), "events": j.out.String()}, "", "  ")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "cloud-child-cancel-"+strconv.FormatBool(independent)+".json"), evidence, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// A completed physical child turn is not a completed logical child when its
// driver suspended on a durable sleep. Parent delivery must await final output.
func TestDurableCloudSleepingChildCompletion(t *testing.T) {
	model := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	j := newDurableControlJourney(t, "sleeping-child-parent", model)
	launch, _ := json.Marshal(map[string]any{"prompt": "sleeping-child-input"})
	sleep, _ := json.Marshal(map[string]any{"duration_ms": 2500})
	model.calls["launch-sleeping-child"] = &providers.ToolCall{ID: "launch-sleep", Name: j.names["durable_subagent"], Arguments: string(launch)}
	model.calls["sleeping-child-input"] = &providers.ToolCall{ID: "child-sleep", Name: j.names["durable_sleep"], Arguments: string(sleep)}
	j.start(t, "", nil, "launch-sleeping-child")
	j.completed(t, 1)
	var child map[string]any
	for _, v := range j.tasks(t) {
		task := v.(map[string]any)
		if task["kind"] == "subagent" {
			child = task
		}
	}
	if child == nil {
		t.Fatal("missing launched child")
	}
	childID := child["child_session_id"].(string)
	var wakeAt time.Time
	for i := 0; i < 100 && wakeAt.IsZero(); i++ {
		query := durableIngressCall(t, j.server, j.out, j.ctx, "child-sleep-"+strconv.Itoa(i), "durable/tasks/list", map[string]string{"session_id": childID})
		if query["error"] != nil {
			t.Fatal(query)
		}
		for _, v := range query["result"].(map[string]any)["tasks"].([]any) {
			task := v.(map[string]any)
			if task["kind"] == "sleep" {
				wakeAt, _ = time.Parse(time.RFC3339Nano, task["wake_at"].(string))
			}
		}
		if wakeAt.IsZero() {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if wakeAt.IsZero() {
		t.Fatal("child did not suspend")
	}
	for time.Now().Before(wakeAt) {
		for _, v := range j.tasks(t) {
			task := v.(map[string]any)
			if task["id"] == child["id"] && task["delivered"] == true {
				t.Fatalf("physical suspension delivered premature child result: %v", task)
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range j.tasks(t) {
			task := v.(map[string]any)
			if task["id"] == child["id"] {
				child = task
			}
		}
		if child["delivered"] == true && child["status"] == "completed" {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("resumed logical child never completed: %v", child)
}

func durableChildStoredTasks(t *testing.T, j *durableControlJourney) []any {
	t.Helper()
	binding, err := j.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	client := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		value, err := binding.Host.CallService(ctx, "example.durable-driver", persistence.ServiceName, method, raw)
		if err != nil {
			return nil, err
		}
		return value, nil
	})
	view, err := client.Read(j.ctx, persistence.Query{Namespace: "example.durable-driver.sessions", Keys: []string{j.id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Documents) != 1 {
		t.Fatalf("missing persisted parent document: %+v", view)
	}
	var doc struct {
		Tasks []any `json:"tasks"`
	}
	if err := json.Unmarshal(view.Documents[0].Value, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Tasks
}

// Recovery must retain the child identity and sleep deadline, resume its logical
// run after an OS process kill, and deliver the final result only once.
func TestDurableCloudChildCrashRecovery(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/durable-cloud-e2e.mjs")
	}
	pidPath := filepath.Join(t.TempDir(), "child-driver.pid")
	t.Setenv("WUU_DURABLE_FIXTURE", durableCloudCrashFixture(t, fixture, pidPath))
	const childPrompt = "sleeping-crash-child-input"
	const childResult = "unique-crash-child-final-result"
	model := &durableChildGate{calls: map[string]*providers.ToolCall{}, outputs: map[string]string{"Durable sleep finished. Continue the pending task.": childResult}}
	j := newDurableControlJourney(t, "child-crash-parent", model)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	j.ctx = ctx
	launch, _ := json.Marshal(map[string]any{"prompt": childPrompt})
	sleep, _ := json.Marshal(map[string]any{"duration_ms": 10000})
	model.calls["launch-crash-child"] = &providers.ToolCall{ID: "crash-launch", Name: j.names["durable_subagent"], Arguments: string(launch)}
	model.calls[childPrompt] = &providers.ToolCall{ID: "crash-sleep", Name: j.names["durable_sleep"], Arguments: string(sleep)}
	j.start(t, "", nil, "launch-crash-child")
	j.completed(t, 1)
	var child map[string]any
	for _, v := range durableChildStoredTasks(t, j) {
		task := v.(map[string]any)
		if task["kind"] == "subagent" {
			child = task
		}
	}
	if child == nil {
		t.Fatal("no persisted child intent")
	}
	childID := child["child_session_id"].(string)
	childTaskID := child["id"].(string)
	childView := *j
	childView.id = childID
	var wakeAt time.Time
	ready := false
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range childView.tasks(t) {
			task := v.(map[string]any)
			if task["kind"] == "sleep" && task["status"] == "waiting" {
				wakeAt, _ = time.Parse(time.RFC3339Nano, task["wake_at"].(string))
			}
		}
		receipt, found, err := j.rt.Sessions.FindPluginTurnLifecycle(child["owner"].(string), childTaskID+":input", childID, "")
		if err != nil {
			t.Fatal(err)
		}
		var terminal struct {
			State       string     `json:"state"`
			CompletedAt *time.Time `json:"completed_at"`
		}
		if found {
			if err := json.Unmarshal(receipt.Payload, &terminal); err != nil {
				t.Fatal(err)
			}
		}
		if !wakeAt.IsZero() && terminal.State == "completed" && terminal.CompletedAt != nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready || !time.Now().Before(wakeAt) {
		t.Fatalf("child never reached committed sleeping safe boundary: ready=%v wake=%v", ready, wakeAt)
	}
	before, _ := json.Marshal(durableChildStoredTasks(t, j))
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	j.server.Close()
	if err := j.closeRuntime(); err != nil {
		t.Logf("killed process cleanup: %v", err)
	}
	timer := time.NewTimer(time.Until(wakeAt.Add(200 * time.Millisecond)))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	reopened, err := runtime.NewSession(j.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Cleanup() })
	j.rt = reopened
	j.out = &lockedBuffer{}
	j.server = New(reopened, j.out)
	t.Cleanup(j.server.Close)
	j.completed(t, 1)
	deadline = time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		for _, v := range durableChildStoredTasks(t, j) {
			task := v.(map[string]any)
			if task["id"] == childTaskID {
				child = task
			}
		}
		if child["status"] == "completed" && child["delivered"] == true {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if child["status"] != "completed" || child["delivered"] != true || child["child_session_id"] != childID {
		t.Fatalf("restarted child result not consumed: %v", child)
	}
	// Several scheduler scans must retain one child and one delivered result.
	until := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(until) {
		time.Sleep(25 * time.Millisecond)
	}
	managed, found, err := j.rt.Sessions.FindManagedByRequest("plugin:"+child["owner"].(string), child["request_id"].(string))
	if err != nil || !found || managed.ID != childID {
		t.Fatalf("recovery replaced child: %+v %v %v", managed, found, err)
	}
	childHistory, err := j.rt.Sessions.LoadHistoryRecords(childID, true)
	if err != nil {
		t.Fatal(err)
	}
	parentHistory, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	inputs, outputs, deliveries := 0, 0, 0
	for _, record := range childHistory {
		if record.Role == "user" && record.Content == childPrompt {
			inputs++
		}
		if record.Role == "assistant" && record.Content == childResult {
			outputs++
		}
	}
	for _, record := range parentHistory {
		if record.Role == "user" && strings.Contains(record.Content, childResult) {
			deliveries++
		}
	}
	model.mu.Lock()
	observed := 0
	for _, prompt := range model.prompts {
		if strings.Contains(prompt, childResult) {
			observed++
		}
	}
	model.mu.Unlock()
	if inputs != 1 || outputs != 1 || deliveries != 1 || observed != 1 {
		t.Fatalf("crash duplicated/lost work: child_inputs=%d child_outputs=%d parent_deliveries=%d parent_model_observations=%d", inputs, outputs, deliveries, observed)
	}
	after, err := json.Marshal(durableChildStoredTasks(t, j))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), childPrompt) || strings.Contains(string(after), childResult) {
		t.Fatal("persisted task metadata duplicated child transcript")
	}
	if dir := os.Getenv("WUU_DURABLE_EVIDENCE"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		evidence, _ := json.MarshalIndent(map[string]any{"scenario": "child-process-kill-recovery", "killed_pid": pid, "safe_boundary": "durably completed physical turn with pending child sleep", "child_session_id": childID, "wake_at": wakeAt, "child_inputs": inputs, "child_outputs": outputs, "parent_deliveries": deliveries, "parent_model_observations": observed, "before": json.RawMessage(before), "after": json.RawMessage(after), "events": j.out.String()}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "cloud-child-crash.json"), evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
