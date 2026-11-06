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

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Escaped failures: namespaced control names were ignored, context intent was
// lost on save, timer wake lacked invocation scope, and cancel raced dispatch.
// These journeys start real model turns in an independently installed process.
type durableControlModel struct {
	mu                  sync.Mutex
	next                *providers.ToolCall
	calls               int
	childResultObserved bool
}

func (m *durableControlModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "A compact summary of the previous exchange."}, nil
}
func (m *durableControlModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.calls++
	content := "Control journey complete."
	for _, message := range request.Messages {
		if message.Role == "user" && message.Content == "Finish the child task" {
			content = "Unique child control result."
		}
		if message.Role == "user" && strings.Contains(message.Content, "Unique child control result.") {
			m.childResultObserved = true
		}
	}
	call := m.next
	m.next = nil
	m.mu.Unlock()
	ch := make(chan providers.StreamEvent, 3)
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
func (m *durableControlModel) count() int { m.mu.Lock(); defer m.mu.Unlock(); return m.calls }

type durableControlJourney struct {
	rt           *runtime.Session
	server       *Server
	out          *lockedBuffer
	model        *durableControlModel
	ctx          context.Context
	names        map[string]string
	id           string
	queries      int
	options      runtime.Options
	closeRuntime func() error
}

func newDurableControlJourney(t *testing.T, id string, clients ...providers.StreamClient) *durableControlJourney {
	t.Helper()
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/durable-cloud-e2e.mjs")
	}
	p, err := pluginpkg.LoadManifest(filepath.Join(fixture, "plugin.json"), "user")
	if err != nil {
		t.Fatal(err)
	}
	plugins := []pluginpkg.Plugin{p}
	cfg := config.Default()
	cfg.PTC.Enabled = false
	cfg.Extensions = &extensions.Settings{}
	cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: p.SubjectID, Fingerprint: p.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: p.EffectivePermissions, ApprovedAt: time.Now().UTC()})
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.invalid/v1", APIKey: "fixture", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{p.ID: true, "wuu.durable-wait": true, "wuu.durable-client": true, "fixture.model-configuration": true, "wuu.default-loop": false}, Services: []composition.Binding{{Service: "driver.default", Major: 2, Provider: p.ID}, {Service: "model.adapter", Major: 1, Provider: "fixture.model-configuration"}}}}}
	model := &durableControlModel{}
	var client providers.StreamClient = model
	if len(clients) > 0 {
		client = clients[0]
	}
	options := runtime.Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true, NativeProviders: []runtime.NativeProviderFactory{{ID: "fixture.model-configuration", Build: func(c runtime.NativeProviderContext) (pluginhost.Client, error) {
		return &durableCloudModel{embeddedModelStreamFixture: embeddedModelStreamFixture{client: client, host: c.Host}}, nil
	}}}}
	rt, err := runtime.NewSession(options)
	if err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	var cleanupErr error
	closeRuntime := func() error { closeOnce.Do(func() { _, cleanupErr = rt.Cleanup() }); return cleanupErr }
	t.Cleanup(func() { closeRuntime() })
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	names := map[string]string{}
	for _, name := range []string{"durable_sleep", "durable_cancel", "durable_context", "durable_subagent"} {
		declared, ok := binding.Host.DeclaredTool(p.ID, name)
		if !ok {
			t.Fatalf("missing declared %s", name)
		}
		names[name] = declared.PublicName
	}
	if _, err = rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Sessions.UpdateTitle(id, "Control acceptance"); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	return &durableControlJourney{rt: rt, server: server, out: out, model: model, ctx: ctx, names: names, id: id, options: options, closeRuntime: closeRuntime}
}
func (j *durableControlJourney) start(t *testing.T, tool string, args any, prompt string) {
	t.Helper()
	if tool != "" {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		j.model.mu.Lock()
		j.model.next = &providers.ToolCall{ID: "control-call", Name: j.names[tool], Arguments: string(raw)}
		j.model.mu.Unlock()
	}
	result := durableIngressCall(t, j.server, j.out, j.ctx, "start-"+prompt, "turn/start", map[string]string{"thread_id": j.id, "prompt": prompt})
	if result["error"] != nil {
		t.Fatal(result)
	}
}
func (j *durableControlJourney) completed(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if turnCompletedCountForThread(t, j.out, j.id) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d completed physical turns: %s", n, j.out.String())
}
func (j *durableControlJourney) tasks(t *testing.T) []any {
	t.Helper()
	j.queries++
	v := durableIngressCall(t, j.server, j.out, j.ctx, "tasks-"+strconv.Itoa(j.queries), "durable/tasks/list", map[string]string{"session_id": j.id})
	if v["error"] != nil {
		t.Fatal(v)
	}
	return v["result"].(map[string]any)["tasks"].([]any)
}
func (j *durableControlJourney) evidence(t *testing.T, name string) {
	t.Helper()
	dir := os.Getenv("WUU_DURABLE_EVIDENCE")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(map[string]any{"scenario": name, "model_requests": j.model.count(), "tasks": j.tasks(t), "events": j.out.String()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, name+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDurableCloudSleepAutomaticWake(t *testing.T) {
	j := newDurableControlJourney(t, "sleep-auto")
	j.start(t, "durable_sleep", map[string]any{"duration_ms": 4000}, "Sleep then finish")
	j.completed(t, 1)
	found := false
	for _, value := range j.tasks(t) {
		task := value.(map[string]any)
		if task["kind"] == "sleep" {
			found = true
			if task["phase"] != "sleeping" || task["status"] != "waiting" {
				t.Fatalf("sleep did not suspend: %v", task)
			}
		}
	}
	if !found {
		t.Fatalf("no real sleep task: %s", j.out.String())
	}
	if j.model.count() != 1 {
		t.Fatalf("physical suspension continued inference: %d", j.model.count())
	}
	deadline := time.Now().Add(12 * time.Second)
	for j.model.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if j.model.count() != 2 {
		t.Fatalf("scheduler did not automatically resume inference: tasks=%v events=%s", j.tasks(t), j.out.String())
	}
	j.completed(t, 2)
	j.evidence(t, "cloud-sleep-auto")
}
func TestDurableCloudCancelSleepingRun(t *testing.T) {
	j := newDurableControlJourney(t, "sleep-cancel")
	j.start(t, "durable_sleep", map[string]any{"duration_ms": 6000}, "Sleep to be canceled")
	j.completed(t, 1)
	run := ""
	var wakeAt time.Time
	for _, value := range j.tasks(t) {
		task := value.(map[string]any)
		if task["kind"] == "sleep" {
			run = task["run_id"].(string)
			wakeAt, _ = time.Parse(time.RFC3339Nano, task["wake_at"].(string))
		}
	}
	if run == "" {
		t.Fatal("no sleeping run")
	}
	j.start(t, "durable_cancel", map[string]any{"run_id": run}, "Cancel the sleeping run")
	// Self-cancellation ends with the public interrupted terminal, not success.
	interrupted := false
	terminalDeadline := time.Now().Add(12 * time.Second)
	for !interrupted && time.Now().Before(terminalDeadline) {
		for _, event := range parseOutput(t, j.out.String()) {
			if event["method"] != "turn/error" {
				continue
			}
			params := event["params"].(map[string]any)
			if params["thread_id"] == j.id && params["category"] == "cancelled" {
				interrupted = true
			}
		}
		if !interrupted {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !interrupted {
		t.Fatalf("cancel did not terminate the physical turn: %s", j.out.String())
	}
	before := j.model.count()
	// Observe beyond the persisted timer deadline and several scheduler ticks.
	deadline := wakeAt.Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if j.model.count() != before {
			t.Fatalf("canceled run resumed: %s", j.out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, value := range j.tasks(t) {
		task := value.(map[string]any)
		if task["run_id"] == run && task["kind"] == "sleep" && task["status"] != "aborted" {
			t.Fatalf("sleep not durably canceled: %v", task)
		}
	}
	j.evidence(t, "cloud-sleep-cancel")
}
func TestDurableCloudContextReplacement(t *testing.T) {
	for _, action := range []string{"reset", "handoff", "compact"} {
		t.Run(action, func(t *testing.T) {
			j := newDurableControlJourney(t, "context-"+action)
			j.start(t, "", nil, "Original history must remain auditable")
			j.completed(t, 1)
			before, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
			if err != nil {
				t.Fatal(err)
			}
			j.start(t, "durable_context", map[string]string{"action": action}, "Change active context safely")
			j.completed(t, 2)
			checkpoint, ok, err := j.rt.Sessions.LatestHistoryCheckpoint(j.id)
			if err != nil || !ok {
				t.Fatalf("public context replacement did not create checkpoint: %+v %v events=%s", checkpoint, err, j.out.String())
			}
			if checkpoint.Version < 1 || checkpoint.ThroughSeq < 1 || len(checkpoint.Replacement) == 0 {
				t.Fatalf("empty replacement checkpoint: %+v", checkpoint)
			}
			if action != "compact" {
				for _, record := range checkpoint.Replacement {
					if record.Role == "user" && record.Content == "Original history must remain auditable" {
						t.Fatalf("%s retained old user input in active replacement", action)
					}
				}
			}
			after, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, old := range before {
				found := false
				for _, record := range after {
					if record.Seq == old.Seq && record.Content == old.Content && record.Role == old.Role {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("original audit history removed: %+v", old)
				}
			}
			j.evidence(t, "cloud-context-"+action)
		})
	}
}

func TestDurableCloudChildLifecycle(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(strconv.FormatBool(background), func(t *testing.T) {
			j := newDurableControlJourney(t, "child-"+strconv.FormatBool(background))
			j.start(t, "durable_subagent", map[string]any{"prompt": "Finish the child task", "background": background}, "Launch one child")
			j.completed(t, 1)
			var child map[string]any
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				for _, value := range j.tasks(t) {
					task := value.(map[string]any)
					if task["kind"] == "subagent" {
						child = task
					}
				}
				if child != nil && child["status"] == "completed" && child["delivered"] == true {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if child == nil || child["status"] != "completed" || child["delivered"] != true {
				var childHistory any
				if child != nil {
					if id, ok := child["child_session_id"].(string); ok {
						childHistory, _ = j.rt.Sessions.LoadHistoryRecords(id, true)
					}
				}
				t.Fatalf("child result not delivered: %v child_history=%+v events=%s", child, childHistory, j.out.String())
			}
			id, ok := child["child_session_id"].(string)
			if !ok || id == "" {
				t.Fatalf("no real child session: %v", child)
			}
			records, err := j.rt.Sessions.LoadHistoryRecords(id, true)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, record := range records {
				if record.Role == "assistant" && record.Content == "Unique child control result." {
					found = true
				}
			}
			if !found {
				t.Fatalf("child never completed actual inference: %+v", records)
			}
			// Delivery acknowledges admission; the continuation executes asynchronously.
			j.completed(t, 2)
			if j.model.count() < 3 {
				t.Fatalf("parent did not receive child result: %d model calls", j.model.count())
			}
			j.model.mu.Lock()
			observed := j.model.childResultObserved
			j.model.mu.Unlock()
			if !observed {
				t.Fatalf("parent continuation omitted actual child result body: %s", j.out.String())
			}
			j.evidence(t, "cloud-child-"+strconv.FormatBool(background))
		})
	}
}

// Persisted timer intent survives complete host teardown, including a deadline
// elapsed while no host or plugin process is running. No task state is seeded.
func TestDurableCloudSleepHostRestart(t *testing.T) {
	j := newDurableControlJourney(t, "sleep-host-restart")
	j.start(t, "durable_sleep", map[string]any{"duration_ms": 6000}, "Sleep across host restart")
	j.completed(t, 1)
	var taskID string
	var wakeAt time.Time
	for _, value := range j.tasks(t) {
		task := value.(map[string]any)
		if task["kind"] == "sleep" {
			if task["status"] != "waiting" {
				t.Fatalf("sleep already settled before shutdown: %v", task)
			}
			taskID = task["id"].(string)
			var err error
			wakeAt, err = time.Parse(time.RFC3339Nano, task["wake_at"].(string))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if taskID == "" || j.model.count() != 1 {
		t.Fatalf("missing suspended logical sleep: %s", j.out.String())
	}
	j.server.Close()
	if err := j.closeRuntime(); err != nil {
		t.Fatal(err)
	}
	// Use the actual persisted deadline rather than guessing scheduler latency.
	if delay := time.Until(wakeAt.Add(200 * time.Millisecond)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-j.ctx.Done():
			t.Fatal(j.ctx.Err())
		}
	}
	if j.model.count() != 1 {
		t.Fatal("closed host continued inference")
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
	if j.model.count() != 2 {
		t.Fatalf("expected exactly one post-restart continuation: %d", j.model.count())
	}
	verify := func() bool {
		t.Helper()
		found, delivered := false, false
		for _, value := range j.tasks(t) {
			task := value.(map[string]any)
			if task["kind"] == "sleep" {
				if task["id"] != taskID || found {
					t.Fatalf("restart duplicated sleep intent: %v", task)
				}
				found = true
				delivered = task["status"] == "completed" && task["delivered"] == true
			}
		}
		if !found {
			t.Fatal("restart lost sleep intent")
		}
		return delivered
	}
	// Physical completion and the scheduler's persisted consumption acknowledgment
	// are separate operations. Await the latter without permitting another turn.
	acknowledged := false
	ackDeadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(ackDeadline) {
		if j.model.count() != 2 {
			t.Fatalf("restart redelivered before acknowledgment: %d", j.model.count())
		}
		if verify() {
			acknowledged = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !acknowledged {
		t.Fatalf("persisted sleep never acknowledged consumption: %v", j.tasks(t))
	}
	verifyWake := func() {
		t.Helper()
		history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
		if err != nil {
			t.Fatal(err)
		}
		clientID := pluginSessionRequestClientID("example.durable-driver", taskID+":result")
		count := 0
		for _, record := range history {
			if record.Role != "user" {
				continue
			}
			if record.ClientID == clientID || record.Content == "Durable sleep finished. Continue the pending task." {
				count++
				if record.ClientID != clientID || record.Content != "Durable sleep finished. Continue the pending task." {
					t.Fatalf("wake identity/body mismatch: %+v", record)
				}
			}
		}
		if count != 1 {
			t.Fatalf("expected one persisted wake input, got %d", count)
		}
	}
	verifyWake()
	// Observe several scheduler scans after completion for duplicate delivery.
	until := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(until) {
		if j.model.count() != 2 {
			t.Fatalf("restart redelivered completed sleep: %d", j.model.count())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !verify() {
		t.Fatal("acknowledged delivery regressed")
	}
	verifyWake()
	j.evidence(t, "cloud-sleep-restart")
}
