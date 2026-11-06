package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Failure cases: an ordinary owner failure or restart-limit abandonment must
// stop foreground children, keep the owner nonterminal while cleanup is pending,
// preserve background boundaries, and never deliver a canceled child's result.
// The existing child crash journey separately verifies actual OS-kill recovery
// and exactly-once delivery. These checks use real installed plugin processes.
type durableFailedOwnerModel struct{ *durableChildGate }

func (m *durableFailedOwnerModel) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	prompt := ""
	launched := false
	for _, message := range req.Messages {
		if message.Role == "user" && !strings.HasPrefix(message.Content, "<system-reminder>") {
			prompt = message.Content
		}
		if message.Role == "tool" && message.ToolCallID == "background-before-failure" {
			launched = true
		}
	}
	if prompt == "fail-owner-now" || (prompt == "launch-background-then-fail" && launched) {
		m.mu.Lock()
		m.prompts = append(m.prompts, prompt)
		m.mu.Unlock()
		return nil, errors.New("owner-failed-after-child-start")
	}
	return m.durableChildGate.StreamChat(ctx, req)
}
func TestDurableCloudFailedOwnerCleanup(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	model := &durableFailedOwnerModel{gate}
	j := newDurableControlJourney(t, "failed-owner-cleanup", model)
	t.Cleanup(func() { gate.released.Do(func() { close(gate.release) }) })
	raw, _ := json.Marshal(map[string]any{"prompt": "child-private-prompt-marker"})
	gate.calls["launch-owned-child"] = &providers.ToolCall{ID: "owned-child", Name: j.names["durable_subagent"], Arguments: string(raw)}
	j.start(t, "", nil, "launch-owned-child")
	select {
	case <-gate.entered:
	case <-j.ctx.Done():
		t.Fatalf("child did not start: events=%s tasks=%v", j.out.String(), durableChildStoredTasks(t, j))
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
		t.Fatal("missing owned child")
	}
	ownerID := child["run_id"]
	j.start(t, "", nil, "fail-owner-now")
	deadline := time.Now().Add(15 * time.Second)
	settled := false
	for time.Now().Before(deadline) {
		var owner map[string]any
		for _, value := range durableChildStoredTasks(t, j) {
			task := value.(map[string]any)
			if task["id"] == ownerID {
				owner = task
			}
			if task["id"] == child["id"] {
				child = task
			}
		}
		if owner != nil && owner["status"] == "failed" {
			if child["status"] != "aborted" || child["cancel_done"] != true {
				t.Fatalf("owner became terminal before owned cleanup: owner=%v child=%v", owner, child)
			}
			settled = true
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("failed owner cleanup did not settle: %v", durableChildStoredTasks(t, j))
	}
	select {
	case <-gate.canceled:
	default:
		t.Fatal("owner failure did not cancel child model")
	}
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range history {
		if record.Role == "user" && strings.Contains(record.Content, "child-private-result-marker") {
			t.Fatal("failed owner received canceled child result")
		}
	}
	durableCompletionEvidence(t, j, gate, "cloud-failed-owner-cleanup", nil)
}

// A killed plugin has no in-memory cancellation queue. This journey records the
// public Documents completion intent while the process is stopped, then verifies
// startup performs bottom-up cleanup without resuming the sleeping child model.
// It deliberately seeds only metadata at the persisted crash boundary; child
// creation, sleep, process kill, scheduler recovery and cleanup are real.
func TestDurableCloudHeldCompletionRecovery(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/durable-cloud-e2e.mjs")
	}
	pidPath := filepath.Join(t.TempDir(), "cleanup-driver.pid")
	t.Setenv("WUU_DURABLE_FIXTURE", durableCloudCrashFixture(t, fixture, pidPath))
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}}
	j := newDurableControlJourney(t, "held-completion-recovery", gate)
	launch, _ := json.Marshal(map[string]any{"prompt": "cleanup-sleeping-child"})
	sleep, _ := json.Marshal(map[string]any{"duration_ms": 3600000})
	gate.calls["launch-cleanup-child"] = &providers.ToolCall{ID: "cleanup-launch", Name: j.names["durable_subagent"], Arguments: string(launch)}
	gate.calls["cleanup-sleeping-child"] = &providers.ToolCall{ID: "cleanup-sleep", Name: j.names["durable_sleep"], Arguments: string(sleep)}
	j.start(t, "", nil, "launch-cleanup-child")
	j.completed(t, 1)
	var child map[string]any
	for _, value := range durableChildStoredTasks(t, j) {
		task := value.(map[string]any)
		if task["kind"] == "subagent" {
			child = task
		}
	}
	if child == nil {
		t.Fatal("missing child")
	}
	childView := *j
	childView.id = child["child_session_id"].(string)
	deadline := time.Now().Add(12 * time.Second)
	asleep := false
	for time.Now().Before(deadline) {
		for _, value := range childView.tasks(t) {
			task := value.(map[string]any)
			if task["kind"] == "sleep" && task["status"] == "waiting" {
				asleep = true
			}
		}
		lifecycle, found, err := j.rt.Sessions.FindPluginTurnLifecycle(child["owner"].(string), child["id"].(string)+":input", childView.id, "")
		if err != nil {
			t.Fatal(err)
		}
		var done struct {
			CompletedAt *time.Time `json:"completed_at"`
		}
		if found {
			if err := json.Unmarshal(lifecycle.Payload, &done); err != nil {
				t.Fatal(err)
			}
		}
		if asleep && done.CompletedAt != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !asleep {
		t.Fatal("child did not suspend")
	}
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
	// Stop execution without revoking its Documents authority, record the crash
	// boundary, then kill it. A killed generation cannot write as an active caller.
	if output, err := exec.Command("kill", "-STOP", strconv.Itoa(pid)).CombinedOutput(); err != nil {
		t.Fatalf("stop plugin: %v %s", err, output)
	}
	t.Cleanup(func() { process.Kill() })
	binding, err := j.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	store := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := binding.Host.CallService(ctx, "example.durable-driver", persistence.ServiceName, method, raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	ownerID := child["run_id"].(string)
	var commitErr error
	for attempt := 0; attempt < 12; attempt++ {
		view, err := store.Read(j.ctx, persistence.Query{Namespace: "example.durable-driver.sessions", Keys: []string{j.id}})
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(view.Documents[0].Value, &doc); err != nil {
			t.Fatal(err)
		}
		for _, value := range doc["tasks"].([]any) {
			task := value.(map[string]any)
			if task["run_id"] != ownerID || task["status"] == "completed" || task["status"] == "failed" || task["status"] == "aborted" {
				continue
			}
			outcome := "aborted"
			if task["id"] == ownerID {
				outcome = "failed"
			}
			task["status"] = "completing"
			task["phase"] = "completing"
			task["canceled"] = true
			task["completion"] = map[string]any{"status": outcome, "error_code": "owner-failed-before-crash"}
		}
		cp := doc["checkpoints"].(map[string]any)[ownerID].(map[string]any)["state"].(map[string]any)
		cp["phase"] = "completing"
		cp["terminal_outcome"] = "failed"
		cp["error_code"] = "owner-failed-before-crash"
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		_, commitErr = store.Commit(j.ctx, persistence.Transaction{ID: "held-cleanup-intent-" + strconv.Itoa(attempt), ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "example.durable-driver.sessions", Key: j.id, Value: encoded}}})
		if !errors.Is(commitErr, persistence.ErrConflict) {
			break
		}
	}
	binding.Close()
	if commitErr != nil {
		t.Fatal(commitErr)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	j.server.Close()
	if err := j.closeRuntime(); err != nil {
		t.Logf("killed plugin cleanup: %v", err)
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
	childView.rt = reopened
	childView.server = j.server
	childView.out = j.out
	deadline = time.Now().Add(12 * time.Second)
	settled := false
	for time.Now().Before(deadline) {
		for _, value := range durableChildStoredTasks(t, j) {
			task := value.(map[string]any)
			if task["id"] == ownerID && task["status"] == "failed" {
				settled = true
			}
			if task["id"] == child["id"] {
				child = task
			}
		}
		if settled {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !settled || child["status"] != "aborted" || child["cancel_done"] != true || child["delivered"] == true {
		t.Fatalf("held cleanup not recovered: %v", durableChildStoredTasks(t, j))
	}
	for _, value := range durableChildStoredTasks(t, &childView) {
		task := value.(map[string]any)
		if task["kind"] == "run" || task["kind"] == "sleep" {
			if task["status"] != "aborted" {
				t.Fatalf("owner ended before sleeping descendant: %v", task)
			}
		}
	}
	gate.mu.Lock()
	prompts := append([]string(nil), gate.prompts...)
	gate.mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("cleanup replayed model work: %q", prompts)
	}
	durableCompletionEvidence(t, j, gate, "cloud-held-completion-recovery", map[string]any{"killed_pid": pid, "seeded_boundary": "held failure persisted while plugin stopped", "child_tasks": durableChildStoredTasks(t, &childView)})
}

// Inclusive cancellation must cross a completed foreground edge to reach a
// background grandchild, even after both foreground logical runs have ended.
func TestDurableCloudInclusiveCompletedOwner(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	j := newDurableControlJourney(t, "inclusive-completed-owner", gate)
	t.Cleanup(func() { gate.released.Do(func() { close(gate.release) }) })
	launch, _ := json.Marshal(map[string]any{"prompt": "launch-background-grandchild"})
	nested, _ := json.Marshal(map[string]any{"prompt": "child-private-prompt-marker", "background": true})
	gate.calls["launch-foreground-owner"] = &providers.ToolCall{ID: "nested-owner", Name: j.names["durable_subagent"], Arguments: string(launch)}
	gate.calls["launch-background-grandchild"] = &providers.ToolCall{ID: "background-descendant", Name: j.names["durable_subagent"], Arguments: string(nested)}
	j.start(t, "", nil, "launch-foreground-owner")
	select {
	case <-gate.entered:
	case <-j.ctx.Done():
		t.Fatal("grandchild did not start")
	}
	j.completed(t, 2)
	var child map[string]any
	for _, value := range durableChildStoredTasks(t, j) {
		task := value.(map[string]any)
		if task["kind"] == "subagent" {
			child = task
		}
	}
	if child == nil {
		t.Fatal("missing foreground owner")
	}
	childView := *j
	childView.id = child["child_session_id"].(string)
	args, _ := json.Marshal(map[string]any{"run_id": child["run_id"], "include_background": true})
	gate.mu.Lock()
	gate.calls["cancel-entire-owned-tree"] = &providers.ToolCall{ID: "cancel-nested", Name: j.names["durable_cancel"], Arguments: string(args)}
	gate.mu.Unlock()
	j.start(t, "", nil, "cancel-entire-owned-tree")
	j.completed(t, 3)
	select {
	case <-gate.canceled:
	case <-time.After(10 * time.Second):
		t.Fatalf("inclusive abort did not reach grandchild: parent=%v child=%v events=%s", durableChildStoredTasks(t, j), durableChildStoredTasks(t, &childView), j.out.String())
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		for _, value := range durableChildStoredTasks(t, &childView) {
			task := value.(map[string]any)
			if task["kind"] == "subagent" && task["status"] == "aborted" && task["cancel_done"] == true {
				durableCompletionEvidence(t, j, gate, "cloud-inclusive-completed-owner", map[string]any{"child_tasks": durableChildStoredTasks(t, &childView)})
				return
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("nested background cleanup did not settle: %v", durableChildStoredTasks(t, &childView))
}

func durableCompletionEvidence(t *testing.T, j *durableControlJourney, model *durableChildGate, name string, details map[string]any) {
	t.Helper()
	dir := os.Getenv("WUU_DURABLE_EVIDENCE")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	model.mu.Lock()
	calls := len(model.prompts)
	model.mu.Unlock()
	data := map[string]any{"scenario": name, "model_requests": calls, "tasks": durableChildStoredTasks(t, j), "events": j.out.String(), "details": details}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

// Normal process shutdown interrupts execution but must not commit a business
// abort for its owner or child. The preserved checkpoint can be resumed later.
func TestDurableCloudActiveChildShutdown(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	j := newDurableControlJourney(t, "active-child-shutdown", gate)
	t.Cleanup(func() { gate.released.Do(func() { close(gate.release) }) })
	launch, _ := json.Marshal(map[string]any{"prompt": "child-private-prompt-marker"})
	gate.calls["launch-before-shutdown"] = &providers.ToolCall{ID: "shutdown-owned-child", Name: j.names["durable_subagent"], Arguments: string(launch)}
	j.start(t, "", nil, "launch-before-shutdown")
	select {
	case <-gate.entered:
	case <-j.ctx.Done():
		t.Fatalf("child did not start: events=%s tasks=%v", j.out.String(), durableChildStoredTasks(t, j))
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
		t.Fatal("missing foreground child")
	}
	childView := *j
	childView.id = child["child_session_id"].(string)
	j.server.Close()
	for _, view := range []*durableControlJourney{j, &childView} {
		for _, value := range durableChildStoredTasks(t, view) {
			task := value.(map[string]any)
			if task["status"] == "aborted" || task["canceled"] == true {
				t.Fatalf("shutdown became business cancellation: session=%s task=%v", view.id, task)
			}
		}
	}
	before := durableChildStoredTasks(t, &childView)
	if err := j.closeRuntime(); err != nil {
		t.Fatal(err)
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
	childView.rt = reopened
	childView.out = j.out
	childView.server = j.server
	for _, value := range durableChildStoredTasks(t, &childView) {
		task := value.(map[string]any)
		if task["status"] == "aborted" || task["canceled"] == true {
			t.Fatalf("restart lost recoverable child: %v", task)
		}
	}
	gate.released.Do(func() { close(gate.release) })
	deadline := time.Now().Add(25 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		for _, value := range durableChildStoredTasks(t, &childView) {
			task := value.(map[string]any)
			if task["kind"] == "run" && task["status"] == "completed" {
				recovered = true
			}
		}
		if recovered {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !recovered {
		t.Fatalf("active child was never automatically resumed: %v", durableChildStoredTasks(t, &childView))
	}
	gate.mu.Lock()
	calls := 0
	for _, prompt := range gate.prompts {
		if prompt == "child-private-prompt-marker" {
			calls++
		}
	}
	gate.mu.Unlock()
	if calls != 2 {
		t.Fatalf("child model admissions=%d, want original plus one recovery", calls)
	}
	durableCompletionEvidence(t, j, gate, "cloud-active-child-shutdown", map[string]any{"child_tasks_before_restart": before, "child_tasks_after_restart": durableChildStoredTasks(t, &childView)})
}

// A background boundary also survives owner failure, and its later result is a
// new admitted follow-up rather than revival of the failed logical run.
func TestDurableCloudBackgroundSurvivesFailure(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	j := newDurableControlJourney(t, "background-owner-failure", &durableFailedOwnerModel{gate})
	t.Cleanup(func() { gate.released.Do(func() { close(gate.release) }) })
	launch, _ := json.Marshal(map[string]any{"prompt": "child-private-prompt-marker", "background": true})
	gate.calls["launch-background-then-fail"] = &providers.ToolCall{ID: "background-before-failure", Name: j.names["durable_subagent"], Arguments: string(launch)}
	j.start(t, "", nil, "launch-background-then-fail")
	select {
	case <-gate.entered:
	case <-j.ctx.Done():
		t.Fatal("background child did not start")
	}
	deadline := time.Now().Add(12 * time.Second)
	var child map[string]any
	failed := false
	for time.Now().Before(deadline) {
		for _, value := range durableChildStoredTasks(t, j) {
			task := value.(map[string]any)
			if task["kind"] == "run" && task["status"] == "failed" {
				failed = true
			}
			if task["kind"] == "subagent" {
				child = task
			}
		}
		if failed && child != nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !failed || child == nil || child["canceled"] == true {
		t.Fatalf("background boundary lost on failure: %v", durableChildStoredTasks(t, j))
	}
	select {
	case <-gate.canceled:
		t.Fatal("owner failure canceled background child")
	default:
	}
	gate.released.Do(func() { close(gate.release) })
	deadline = time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		for _, value := range durableChildStoredTasks(t, j) {
			task := value.(map[string]any)
			if task["id"] == child["id"] {
				child = task
			}
		}
		if child["delivered"] == true {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if child["delivered"] != true || child["status"] != "completed" {
		t.Fatalf("surviving child did not deliver: %v", child)
	}
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, record := range history {
		if record.Role == "user" && strings.Contains(record.Content, "child-private-result-marker") {
			results++
		}
	}
	if results != 1 {
		t.Fatalf("background failure follow-up count=%d", results)
	}
	durableCompletionEvidence(t, j, gate, "cloud-background-survives-failure", map[string]any{"parent_result_count": results})
}
