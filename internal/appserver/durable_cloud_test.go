package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/durablewait"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Failure cases: a disconnected reader cancels durable work; a second client
// cannot observe a decision; process task reads lose persisted state; an unknown
// effect can be mistaken for safe-to-retry. This exercises handlers, not socket
// transport or inference. Task metadata is seeded explicitly; the provider is real.
func TestDurableCloudProcessIngress(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/durable-cloud-e2e.mjs")
	}
	provider, err := pluginpkg.LoadManifest(filepath.Join(fixture, "plugin.json"), "user")
	if err != nil {
		t.Fatal(err)
	}
	plugins := []pluginpkg.Plugin{provider}
	cfg := config.Default()
	cfg.Extensions = &extensions.Settings{}
	cfg.PTC.Enabled = false
	if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: provider.SubjectID, Fingerprint: provider.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: provider.EffectivePermissions, ApprovedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.invalid/v1", APIKey: "fixture", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{provider.ID: true, "wuu.durable-wait": true, "wuu.durable-client": true, "wuu.default-loop": false}, Services: []composition.Binding{{Service: "driver.default", Major: 2, Provider: provider.ID}}}}}
	rt, err := runtime.NewSession(runtime.Options{RootDir: t.TempDir(), HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Cleanup()
	const sessionID = "cloud-shared-session"
	if _, err = rt.Sessions.CreateWithMetadata(sessionID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Seed the public document schema, then read it through the independent plugin
	// process and authenticated appserver ingress, never directly from this store.
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	documentsFor := func(caller string) *persistence.Client {
		return persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, callErr := binding.Host.CallService(ctx, caller, persistence.ServiceName, method, raw)
			if callErr != nil {
				return nil, callErr
			}
			return result, nil
		})
	}
	taskDocuments := documentsFor(provider.ID)
	view, err := taskDocuments.Read(ctx, persistence.Query{Namespace: "example.durable-driver.sessions", Keys: []string{sessionID}})
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"schema_version": 1, "session_id": sessionID, "checkpoints": map[string]any{}, "canceled": false, "tasks": []any{map[string]any{"id": "run-cloud", "session_id": sessionID, "run_id": "run-cloud", "kind": "run", "status": "waiting", "phase": "waiting", "attempt": 1, "owner": provider.ID, "checkpoint_ref": "run-cloud", "turn_ref": "turn-cloud"}}}
	if _, err = taskDocuments.Commit(ctx, persistence.Transaction{ID: "cloud-task-seed", ExpectedRevision: view.Revision, Writes: []persistence.Write{{Namespace: "example.durable-driver.sessions", Key: sessionID, Value: mustJSON(doc)}}}); err != nil {
		t.Fatal(err)
	}
	store := durablewait.New(documentsFor(durablewait.Module), time.Now)
	owner := durablewait.Owner{PluginID: provider.ID, SessionID: sessionID, ExecutionID: "turn-cloud", CallID: "call-cloud"}
	if _, err = store.RegisterBeforeDispatch(ctx, owner, durablewait.Intent{Version: 1, ID: "approval-cloud", Kind: "approval", Question: "Run controlled effect?", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	identity, err := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "cloud-test", Tenant: "tenant", Subject: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		for _, resource := range r.Resources {
			if resource.Kind != "session" || resource.ID != sessionID {
				return access.Decision{}, nil
			}
		}
		return access.Decision{Allow: r.Principal == identity.Identity()}, nil
	})
	newClient := func() (*Server, *lockedBuffer) {
		out := &lockedBuffer{}
		s := New(rt, out, WithPrincipal(identity), WithAccessPolicy(policy))
		t.Cleanup(s.Close)
		return s, out
	}
	first, a := newClient()
	second, b := newClient()
	call := func(s *Server, out *lockedBuffer, id, method string, p any) map[string]any {
		t.Helper()
		v := durableIngressCall(t, s, out, ctx, id, method, p)
		if v["error"] != nil {
			t.Fatalf("%s: %v", method, v)
		}
		return v
	}
	query := map[string]string{"session_id": sessionID}
	taskBefore := call(first, a, "tasks-before", "durable/tasks/list", query)
	if len(taskBefore["result"].(map[string]any)["tasks"].([]any)) != 1 {
		t.Fatal(taskBefore)
	}
	call(second, b, "waits-before", "durable/waits/list", query)
	first.Close()
	record, err := store.Inspect(ctx, owner, "approval-cloud")
	if err != nil || record.Status != "pending" {
		t.Fatalf("disconnect mutated wait: %+v %v", record, err)
	}
	decision := call(second, b, "approve", "durable/waits/decide", map[string]string{"session_id": sessionID, "wait_id": "approval-cloud", "decision": "approve"})
	if decision["result"].(map[string]any)["status"] != "approved" {
		t.Fatal(decision)
	}
	reconnected, c := newClient()
	after := call(reconnected, c, "waits-after", "durable/waits/list", query)
	if after["result"].(map[string]any)["waits"].([]any)[0].(map[string]any)["status"] != "approved" {
		t.Fatal(after)
	}
	call(reconnected, c, "tasks-after", "durable/tasks/list", query)
	if _, err = store.Register(ctx, owner, durablewait.Intent{Version: 1, ID: "unknown-cloud", Kind: "approval", Question: "Uncertain previous effect", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	unknown := durableIngressCall(t, second, b, ctx, "unknown", "durable/waits/decide", map[string]string{"session_id": sessionID, "wait_id": "unknown-cloud", "decision": "approve"})
	if unknown["error"] != nil || unknown["result"].(map[string]any)["effect_state"] != "unknown" {
		t.Fatalf("approval lost unknown outcome: %v", unknown)
	}
	if _, err = store.BeginDispatch(ctx, owner, "unknown-cloud"); err == nil {
		t.Fatal("unknown effect dispatched")
	}
	if evidence := os.Getenv("WUU_DURABLE_EVIDENCE"); evidence != "" {
		if err = os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(map[string]any{"scenario": "authenticated-process-ingress", "task_source": "seeded public document", "clients": 3, "reconnect_kept_wait": true, "unknown_dispatch_rejected": true, "before": taskBefore, "after": after}, "", "  ")
		if err = os.WriteFile(filepath.Join(evidence, "cloud-ingress.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// This adapter participates in normal selected-model routing. It emits a real
// tool batch, while keeping the acceptance test independent of network services.
type durableCloudModel struct{ embeddedModelStreamFixture }

func (*durableCloudModel) ModelAdapters() []pluginhost.ModelAdapterDescriptor {
	return []pluginhost.ModelAdapterDescriptor{{ID: "fixture.cloud-model", Version: 1, Mode: "direct"}}
}
func (m *durableCloudModel) InvokeService(context.Context, pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	return json.Marshal(m.ModelAdapters()[0])
}

type durableCloudInference struct {
	calls        atomic.Int32
	marker       string
	waitToolName string
}

func (*durableCloudInference) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream required")
}
func (m *durableCloudInference) StreamChat(ctx context.Context, r providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.calls.Add(1)
	ch := make(chan providers.StreamEvent, 8)
	found := false
	for _, message := range r.Messages {
		if message.Role == "tool" {
			found = true
		}
	}
	if found {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Approved marker recorded."}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	} else {
		args, _ := json.Marshal(map[string]string{"kind": "approval", "question": "Write one marker?", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		effect, _ := json.Marshal(map[string]string{"path": m.marker, "content": "approved-once\n"})
		waitName := m.waitToolName
		if waitName == "" {
			return nil, errors.New("durable wait tool missing from selected host")
		}
		for _, call := range []providers.ToolCall{{ID: "approval-call", Name: waitName, Arguments: string(args)}, {ID: "marker-call", Name: "write_file", Arguments: string(effect)}} {
			call := call
			ch <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
			ch <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
		}
		ch <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	}
	close(ch)
	return ch, nil
}
func TestDurableCloudApprovalEffect(t *testing.T) { runDurableCloudApproval(t, false) }
func TestDurableCloudApprovalCrash(t *testing.T)  { runDurableCloudApproval(t, true) }

func runDurableCloudApproval(t *testing.T, crash bool) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/durable-cloud-e2e.mjs")
	}
	root := t.TempDir()
	pidPath := filepath.Join(root, "durable-plugin.pid")
	if crash {
		fixture = durableCloudCrashFixture(t, fixture, pidPath)
	}
	model := &durableCloudInference{marker: filepath.Join(root, "approved-marker.txt")}
	worker := os.Getenv("WUU_DURABLE_WORKER")
	workerProof := filepath.Join(root, "worker-started.txt")
	wrapper := filepath.Join(root, "start-worker.sh")
	if worker != "" {
		if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '%s\\n' \"$$\" >> \"$1\"\nshift\nexec \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := pluginpkg.LoadManifest(filepath.Join(fixture, "plugin.json"), "user")
	if err != nil {
		t.Fatal(err)
	}
	plugins := []pluginpkg.Plugin{provider}
	cfg := config.Default()
	cfg.Extensions = &extensions.Settings{}
	cfg.PTC.Enabled = false
	if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: provider.SubjectID, Fingerprint: provider.Fingerprint, Scope: extensions.GrantScopeUser, Permissions: provider.EffectivePermissions, ApprovedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if worker != "" {
		cfg.ExecutionEnvironments = executionenv.Config{Default: "durable-reference", Profiles: map[string]executionenv.Profile{"durable-reference": {Backend: "command", Command: []string{"/bin/sh", wrapper, workerProof, worker, "execution-worker"}, Workspace: root, Shared: true}}}
	}
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.invalid/v1", APIKey: "fixture", Model: "fixture"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{provider.ID: true, "wuu.durable-wait": true, "wuu.durable-client": true, "fixture.model-configuration": true, "wuu.default-loop": false}, Services: []composition.Binding{{Service: "driver.default", Major: 2, Provider: provider.ID}, {Service: "model.adapter", Major: 1, Provider: "fixture.model-configuration"}}}}}
	opts := runtime.Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true, NativeProviders: []runtime.NativeProviderFactory{{ID: "fixture.model-configuration", Build: func(c runtime.NativeProviderContext) (pluginhost.Client, error) {
		return &durableCloudModel{embeddedModelStreamFixture: embeddedModelStreamFixture{client: model, host: c.Host}}, nil
	}}}}
	rt, err := runtime.NewSession(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { rt.Cleanup() }()
	binding, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	registered, ok := binding.Host.DeclaredTool(provider.ID, "durable_wait")
	if !ok {
		t.Fatal("wait tool not registered")
	}
	model.waitToolName = registered.PublicName
	const id = "cloud-approval-effect"
	if _, err = rt.Sessions.CreateWithMetadata(id, root); err != nil {
		t.Fatal(err)
	}
	if err = rt.PinExecutionEnvironment(id, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Sessions.UpdateTitle(id, "Approval integration"); err != nil {
		t.Fatal(err)
	}
	identity, err := principal.Authenticated(principal.Identity{Kind: "user", Issuer: "cloud-test", Tenant: "tenant", Subject: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	policy := access.PolicyFunc(func(_ context.Context, r access.Request) (access.Decision, error) {
		return access.Decision{Allow: r.Principal == identity.Identity()}, nil
	})
	out := &lockedBuffer{}
	s := New(rt, out, WithPrincipal(identity), WithAccessPolicy(policy))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	response := durableIngressCall(t, s, out, ctx, "start-effect", "turn/start", map[string]string{"thread_id": id, "prompt": "Write the marker only after approval."})
	if response["error"] != nil {
		t.Fatal(response)
	}
	deadline := time.Now().Add(20 * time.Second)
	for turnCompletedCountForThread(t, out, id) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if turnCompletedCountForThread(t, out, id) != 1 {
		t.Fatalf("first turn did not suspend: %s", out.String())
	}
	if _, err = os.Stat(model.marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("effect ran before approval: %v", err)
	}
	waits := durableIngressCall(t, s, out, ctx, "wait-read", "durable/waits/list", map[string]string{"session_id": id})
	if waits["error"] != nil {
		t.Fatal(waits)
	}
	records := waits["result"].(map[string]any)["waits"].([]any)
	if len(records) != 1 {
		t.Fatalf("expected approval: %v output=%s", waits, out.String())
	}
	waitID := records[0].(map[string]any)["id"].(string)
	if crash {
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
		if err = process.Kill(); err != nil {
			t.Fatal(err)
		}
	}
	s.Close() // Disconnect the first transport while the logical task is waiting.
	if crash {
		binding.Close()
		rt.Cleanup()
		rt, err = runtime.NewSession(opts)
		if err != nil {
			t.Fatal(err)
		}
	}
	secondOut := &lockedBuffer{}
	second := New(rt, secondOut, WithPrincipal(identity), WithAccessPolicy(policy))
	defer second.Close()
	pending := durableIngressCall(t, second, secondOut, ctx, "reconnected-waits", "durable/waits/list", map[string]string{"session_id": id})
	if pending["error"] != nil {
		t.Fatal(pending)
	}
	restored := pending["result"].(map[string]any)["waits"].([]any)
	if len(restored) != 1 || restored[0].(map[string]any)["id"] != waitID || restored[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("lost pending wait on reconnect/restart: %v", pending)
	}
	decision := durableIngressCall(t, second, secondOut, ctx, "approve-effect", "durable/waits/decide", map[string]string{"session_id": id, "wait_id": waitID, "decision": "approve"})
	if decision["error"] != nil {
		t.Fatal(decision)
	}
	deadline = time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if raw, e := os.ReadFile(model.marker); e == nil && string(raw) == "approved-once\n" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	raw, err := os.ReadFile(model.marker)
	if err != nil || string(raw) != "approved-once\n" {
		tasks := durableIngressCall(t, second, secondOut, ctx, "failed-tasks", "durable/tasks/list", map[string]string{"session_id": id})
		t.Fatalf("approved effect absent: %s %v tasks=%v output=%s", raw, err, tasks, out.String()+secondOut.String())
	}
	deadline = time.Now().Add(10 * time.Second)
	history, err := rt.Sessions.LoadProviderHistorySnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	for time.Now().Before(deadline) {
		for _, record := range history.Records {
			if record.Role == "assistant" && record.Content == "Approved marker recorded." {
				completed = true
			}
		}
		if completed {
			break
		}
		time.Sleep(10 * time.Millisecond)
		history, err = rt.Sessions.LoadProviderHistorySnapshot(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !completed {
		t.Fatalf("approved turn did not produce its final answer: %s", secondOut.String())
	}
	receipts := 0
	for _, record := range history.Records {
		if record.Role == "tool" && record.ToolCallID == "marker-call" {
			receipts++
			if record.ToolInvocationID == "" {
				t.Fatalf("marker result missing receipt identity: %+v", record)
			}
		}
	}
	if receipts != 1 {
		t.Fatalf("marker receipt count=%d", receipts)
	}
	if calls := model.calls.Load(); calls != 2 {
		t.Fatalf("approval resampled the held tool intent: model requests=%d", calls)
	}
	backend := "local"
	if worker != "" {
		started, err := os.ReadFile(workerProof)
		if err != nil || len(started) == 0 {
			t.Fatalf("reference worker did not start: %v", err)
		}
		backend = "reference-worker-stdio"
	}

	if evidence := os.Getenv("WUU_DURABLE_EVIDENCE"); evidence != "" {
		if err = os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		value, _ := json.MarshalIndent(map[string]any{"scenario": "model-approval-effect", "execution_backend": backend, "effect_before_approval": false, "marker": string(raw), "model_requests": model.calls.Load(), "final_answer": "Approved marker recorded.", "marker_receipts": receipts, "reconnect_kept_wait": true, "plugin_killed_while_waiting": crash}, "", "  ")
		if err = os.WriteFile(filepath.Join(evidence, map[bool]string{false: "cloud-approval-effect.json", true: "cloud-approval-crash.json"}[crash]), value, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// A new host-admitted user turn, not plugin-provided response JSON, resolves an
// input/event wait. The held tool call must settle without resampling its plan.
func TestDurableCloudInputWaitResolution(t *testing.T) {
	for _, kind := range []string{"input", "event"} {
		t.Run(kind, func(t *testing.T) {
			j := newDurableControlJourney(t, "wait-"+kind)
			binding, err := j.rt.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close()
			declared, ok := binding.Host.DeclaredTool("example.durable-driver", "durable_wait")
			if !ok {
				t.Fatal("durable wait tool missing")
			}
			j.names["durable_wait"] = declared.PublicName
			j.start(t, "durable_wait", map[string]string{"kind": kind, "question": "Provide the next instruction.", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "Wait for my next instruction")
			j.completed(t, 1)
			waits := durableIngressCall(t, j.server, j.out, j.ctx, "input-waits", "durable/waits/list", map[string]string{"session_id": j.id})
			if waits["error"] != nil {
				t.Fatal(waits)
			}
			records := waits["result"].(map[string]any)["waits"].([]any)
			if len(records) != 1 || records[0].(map[string]any)["status"] != "pending" {
				t.Fatalf("wait did not suspend: %v", waits)
			}
			waitID := records[0].(map[string]any)["id"].(string)
			if j.model.count() != 1 {
				t.Fatal("pending wait continued model inference")
			}
			const answer = "Use the blue folder for the completed work."
			j.start(t, "", nil, answer)
			j.completed(t, 2)
			if j.model.count() != 2 {
				t.Fatalf("wait intent was resampled: %d requests", j.model.count())
			}
			documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				result, callErr := binding.Host.CallService(ctx, durablewait.Module, persistence.ServiceName, method, raw)
				if callErr != nil {
					return nil, callErr
				}
				return result, nil
			})
			record, err := durablewait.New(documents, time.Now).Lookup(j.ctx, waitID)
			if err != nil {
				t.Fatal(err)
			}
			if record.Status != "resolved" || record.ResponseRef == nil || record.ResponseRef.SessionID != j.id {
				t.Fatalf("missing host input provenance: %+v", record)
			}
			history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			receipts := 0
			for _, message := range history {
				if message.Seq == record.ResponseRef.Seq && message.Role == "user" && message.Content == answer {
					found = true
				}
				if message.Role == "tool" && message.ToolCallID == "control-call" {
					if message.ToolInvocationID == "" {
						t.Fatalf("synthetic wait result: %+v", message)
					}
					receipts++
				}
			}
			if !found || receipts != 1 {
				t.Fatalf("resolved source or exact tool receipt missing: source=%v receipts=%d record=%+v", found, receipts, record)
			}
			j.evidence(t, "cloud-wait-"+kind)
		})
	}
}

// A background child's real plugin outbox supplies the external event. Its
// read-only source must resolve an event wait without masquerading as a user.
func TestDurableCloudPluginEventResolution(t *testing.T) {
	model := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	j := newDurableControlJourney(t, "plugin-event-wait", model)
	t.Cleanup(func() { model.released.Do(func() { close(model.release) }) })
	binding, err := j.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	declared, ok := binding.Host.DeclaredTool("example.durable-driver", "durable_wait")
	if !ok {
		t.Fatal("wait tool missing")
	}
	j.names["durable_wait"] = declared.PublicName
	enqueue := func(prompt, tool string, args any) {
		model.mu.Lock()
		defer model.mu.Unlock()
		model.calls[prompt] = &providers.ToolCall{ID: prompt, Name: j.names[tool], Arguments: string(mustJSON(args))}
	}
	enqueue("launch-event-source", "durable_subagent", map[string]any{"prompt": "child-private-prompt-marker", "background": true})
	j.start(t, "", nil, "launch-event-source")
	select {
	case <-model.entered:
	case <-j.ctx.Done():
		t.Fatal("event source child did not start")
	}
	j.completed(t, 1)
	enqueue("wait-for-plugin-event", "durable_wait", map[string]string{"kind": "event", "question": "Wait for the child result.", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	j.start(t, "", nil, "wait-for-plugin-event")
	j.completed(t, 2)
	waits := durableIngressCall(t, j.server, j.out, j.ctx, "plugin-event-pending", "durable/waits/list", map[string]string{"session_id": j.id})
	if waits["error"] != nil {
		t.Fatal(waits)
	}
	records := waits["result"].(map[string]any)["waits"].([]any)
	if len(records) != 1 || records[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("event wait did not suspend: %v", waits)
	}
	waitID := records[0].(map[string]any)["id"].(string)
	model.released.Do(func() { close(model.release) })
	j.completed(t, 3)
	documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, callErr := binding.Host.CallService(ctx, durablewait.Module, persistence.ServiceName, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	})
	record, err := durablewait.New(documents, time.Now).Lookup(j.ctx, waitID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "resolved" || record.ResponseRef == nil {
		t.Fatalf("real plugin event did not resolve wait: %+v events=%s", record, j.out.String())
	}
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	receipts := 0
	for _, message := range history {
		if message.Seq == record.ResponseRef.Seq {
			found = message.Role == "user" && message.ReadOnly && message.Origin == "plugin" && message.OriginID != "" && message.ClientID != "" && strings.Contains(message.Content, "child-private-result-marker")
		}
		if message.Role == "tool" && message.ToolCallID == "wait-for-plugin-event" {
			if message.ToolInvocationID == "" {
				t.Fatalf("synthetic event result: %+v", message)
			}
			receipts++
		}
	}
	if !found || receipts != 1 {
		t.Fatalf("event source provenance or receipt missing: source=%v receipts=%d record=%+v", found, receipts, record)
	}
	if dir := os.Getenv("WUU_DURABLE_EVIDENCE"); dir != "" {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		model.mu.Lock()
		requests := len(model.prompts)
		model.mu.Unlock()
		raw, err := json.MarshalIndent(map[string]any{"scenario": "cloud-wait-plugin-event", "model_requests": requests, "wait": record, "event_source_verified": found, "receipts": receipts, "events": j.out.String()}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "cloud-wait-plugin-event.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func durableCloudCrashFixture(t *testing.T, fixture, pidPath string) string {
	t.Helper()
	installed := t.TempDir()
	if err := os.MkdirAll(filepath.Join(installed, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "package.json", "dist/runtime.js"} {
		raw, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(installed, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	wrapper := []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$1\"\nshift\nexec \"$@\"\n")
	if err := os.WriteFile(filepath.Join(installed, "plugin-process.sh"), wrapper, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(installed, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["runtime"] = map[string]any{"protocol": pluginhost.ProtocolName, "command": "/bin/sh", "args": []string{"plugin-process.sh", pidPath, "node", "dist/runtime.js"}}
	if err = os.WriteFile(filepath.Join(installed, "plugin.json"), mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return installed
}

type durableCloudEventSequence struct {
	mu      sync.Mutex
	names   map[string]string
	issued  map[string]bool
	entered [2]chan struct{}
	release [2]chan struct{}
}

func (*durableCloudEventSequence) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "Summary"}, nil
}
func (m *durableCloudEventSequence) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	prompt := ""
	firstEventSeen := false
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "external-event-one") {
			firstEventSeen = true
		}
		if message.Role == "user" && !strings.HasPrefix(message.Content, "<system-reminder>") {
			prompt = message.Content
		}
	}
	out := make(chan providers.StreamEvent, 3)
	for i, childPrompt := range []string{"event-source-one", "event-source-two"} {
		if prompt == childPrompt {
			select {
			case m.entered[i] <- struct{}{}:
			default:
			}
			go func() {
				defer close(out)
				select {
				case <-ctx.Done():
					return
				case <-m.release[i]:
				}
				out <- providers.StreamEvent{Type: providers.EventContentDelta, Content: []string{"external-event-one", "external-event-two"}[i]}
				out <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
			}()
			return out, nil
		}
	}
	m.mu.Lock()
	var call *providers.ToolCall
	for i, launch := range []string{"launch-first-source", "launch-second-source"} {
		if prompt == launch && !m.issued[launch] {
			m.issued[launch] = true
			call = &providers.ToolCall{ID: launch, Name: m.names["durable_subagent"], Arguments: string(mustJSON(map[string]any{"prompt": []string{"event-source-one", "event-source-two"}[i], "background": true}))}
		}
	}
	if firstEventSeen && !m.issued["input-wait"] {
		m.issued["input-wait"] = true
		call = &providers.ToolCall{ID: "input-wait", Name: m.names["durable_wait"], Arguments: string(mustJSON(map[string]string{"kind": "input", "question": "A human response is required.", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}))}
	}
	m.mu.Unlock()
	if call != nil {
		out <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: call}
		out <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: call}
		out <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	} else {
		out <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Event sequence complete."}
		out <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	}
	close(out)
	return out, nil
}

func TestDurableCloudPluginEventCannotAnswerInput(t *testing.T) {
	model := &durableCloudEventSequence{issued: map[string]bool{}, entered: [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)}, release: [2]chan struct{}{make(chan struct{}), make(chan struct{})}}
	release := func(index int) {
		select {
		case <-model.release[index]:
		default:
			close(model.release[index])
		}
	}
	t.Cleanup(func() { release(0); release(1) })
	j := newDurableControlJourney(t, "plugin-input-boundary", model)
	binding, err := j.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	declared, ok := binding.Host.DeclaredTool("example.durable-driver", "durable_wait")
	if !ok {
		t.Fatal("wait tool missing")
	}
	j.names["durable_wait"] = declared.PublicName
	model.names = j.names
	for i, prompt := range []string{"launch-first-source", "launch-second-source"} {
		j.start(t, "", nil, prompt)
		select {
		case <-model.entered[i]:
		case <-j.ctx.Done():
			t.Fatal("event source did not start")
		}
		j.completed(t, i+1)
	}
	release(0)
	j.completed(t, 3)
	waits := durableIngressCall(t, j.server, j.out, j.ctx, "event-created-input", "durable/waits/list", map[string]string{"session_id": j.id})
	if waits["error"] != nil {
		t.Fatal(waits)
	}
	records := waits["result"].(map[string]any)["waits"].([]any)
	if len(records) != 1 || records[0].(map[string]any)["status"] != "pending" {
		t.Fatalf("plugin-triggered input wait not pending: %v", waits)
	}
	waitID := records[0].(map[string]any)["id"].(string)
	documents := persistence.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, callErr := binding.Host.CallService(ctx, durablewait.Module, persistence.ServiceName, method, raw)
		if callErr != nil {
			return nil, callErr
		}
		return result, nil
	})
	store := durablewait.New(documents, time.Now)
	before, err := store.Lookup(j.ctx, waitID)
	if err != nil {
		t.Fatal(err)
	}
	if before.InputBoundary == nil {
		t.Fatalf("missing plugin input baseline: %+v", before)
	}
	release(1)
	j.completed(t, 4)
	still, err := store.Lookup(j.ctx, waitID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Status != "pending" || still.ResponseRef != nil {
		t.Fatalf("plugin event answered human input wait: %+v", still)
	}
	const answer = "This is the actual human answer."
	j.start(t, "", nil, answer)
	j.completed(t, 5)
	after, err := store.Lookup(j.ctx, waitID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "resolved" || after.ResponseRef == nil {
		t.Fatalf("human input did not resolve: %+v", after)
	}
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, true)
	if err != nil {
		t.Fatal(err)
	}
	baseline, human := false, false
	for _, message := range history {
		if message.Seq == before.InputBoundary.Seq {
			baseline = message.ReadOnly && message.Origin == "plugin" && strings.Contains(message.Content, "external-event-one")
		}
		if message.Seq == after.ResponseRef.Seq {
			human = message.Role == "user" && !message.ReadOnly && message.Content == answer
		}
	}
	if !baseline || !human {
		t.Fatalf("wrong input provenance: plugin baseline=%v human answer=%v", baseline, human)
	}
	if dir := os.Getenv("WUU_DURABLE_EVIDENCE"); dir != "" {
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(map[string]any{"scenario": "cloud-input-plugin-boundary", "before": before, "after_plugin_event": still, "after_human_input": after, "events": j.out.String()}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "cloud-input-plugin-boundary.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
