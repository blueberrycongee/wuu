package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

// These scenarios exercise an independently packed public-SDK plugin in real
// OS processes. Checkpoint persistence is the crash barrier, never a timed sleep.
func TestDurableDriverProcess(t *testing.T) {
	fixture := os.Getenv("WUU_DURABLE_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/durable-driver-e2e.mjs")
	}
	if _, err := os.Stat(filepath.Join(fixture, "dist", "runtime.js")); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, crashPhase   string
		unknown, toolError bool
	}{
		{"complete", "", false, false},
		{"killed-before-tools", "tools_pending", false, false},
		{"killed-after-model-archive", "archive:model_pending", false, false},
		{"killed-after-settled-effect", "tools_settled", false, false},
		{"killed-after-tool-archive", "archive:tools_running", false, false},
		{"killed-after-committed-tool-error", "tools_settled", false, true},
		{"killed-before-effect-receipt", "tools_running", true, false},
		{"killed-finalizing", "finalizing", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newDurableProcessHarness(t, fixture, scenario.name)
			initial := []providers.ChatMessage{{Role: "user", Content: "Record exactly one marker, then report the result."}}
			tools := &durableMarkerTools{path: filepath.Join(h.root, "effects.txt"), fail: scenario.toolError}
			model := &durableControlledModel{}
			store := sessionDriverCheckpointStore{sessions: testSessionStore(t, h.sessions), sessionID: h.sessionID}
			ledger, err := toolledger.New(testSessionDocuments(t, h.sessions), h.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			barrier := &durableCheckpointRecorder{CheckpointStore: store, directory: h.evidence, killPhase: scenario.crashPhase}
			runner := &agent.StreamRunner{Client: model, Model: "controlled-durable", Tools: tools, ToolLedger: ledger, DriverBindingStore: store, DriverCheckpointStore: barrier}
			attachWorkflowHistory(t, runner, h.sessions, h.sessionID, initial)
			archive := runner.ArchiveHistory
			runner.ArchiveHistory = func(ctx context.Context, messages []providers.ChatMessage) (agent.HistoryArchive, error) {
				receipt, err := archive(ctx, messages)
				if err == nil && len(messages) > 0 && barrier.killPhase == "archive:"+barrier.phase && !barrier.killed {
					err = barrier.kill()
				}
				return receipt, err
			}
			acquire, stop, pid := h.start(t, 1)
			barrier.pid = pid
			runner.AcquireLoopDriver = acquire
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, runErr := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "durable-first"}), initial, nil)
			stop()
			if scenario.crashPhase != "" {
				if runErr == nil || !barrier.killed {
					t.Fatalf("crash barrier not reached: killed=%v result=%+v err=%v", barrier.killed, result, runErr)
				}
				saved, found, err := store.Load(ctx)
				if err != nil || !found {
					t.Fatalf("checkpoint=%+v found=%v err=%v", saved, found, err)
				}
				h.write(t, "checkpoint-before-recovery.json", saved)
				if err := ledger.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
				history, err := testSessionStore(t, h.sessions).LoadProviderHistorySnapshot(h.sessionID)
				if err != nil {
					t.Fatal(err)
				}
				restored := workflowRecordsToMessages(t, history.Records)
				acquire, stop, _ = h.start(t, 2)
				runner.AcquireLoopDriver = acquire
				barrier.killPhase = ""
				result, runErr = runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "durable-recovered"}), restored, nil)
				stop()
			}
			count := tools.count(t)
			if scenario.unknown {
				if runErr == nil || !strings.Contains(runErr.Error(), "uncertain_tool_effect") {
					t.Fatalf("ambiguous batch did not fail closed: result=%+v err=%v", result, runErr)
				}
				if toolerrors.Kind(runErr) != toolerrors.UncertainEffect {
					t.Fatalf("external driver lost typed unknown outcome: %v", runErr)
				}
				if count != 0 {
					t.Fatalf("ambiguous tool batch replayed: effects=%d", count)
				}
			} else if runErr != nil || result.Content != "Marker recorded once." || count != 1 {
				t.Fatalf("durable result=%+v effects=%d err=%v", result, count, runErr)
			}
			receipts, err := testSessionDocuments(t, h.sessions).Read(ctx, persistence.Query{Namespace: toolstate.Invocations, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if !scenario.unknown {
				if len(receipts.Documents) != 1 {
					t.Fatalf("wanted one authoritative receipt, got %d", len(receipts.Documents))
				}
				var receipt toolstate.Invocation
				if err := json.Unmarshal(receipts.Documents[0].Value, &receipt); err != nil {
					t.Fatal(err)
				}
				expected := "succeeded"
				if scenario.toolError {
					expected = "failed"
				}
				if receipt.State != expected {
					t.Fatalf("receipt=%+v, expected %s", receipt, expected)
				}
			}
			h.write(t, "receipts.json", receipts)
			h.write(t, "result.json", map[string]any{"scenario": scenario.name, "effect_count": count, "final_answer": result.Content, "driver_status": result.DriverStatus, "error": fmt.Sprint(runErr), "process_killed": barrier.killed, "model_requests": model.calls})
			saved, _, err := store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			h.write(t, "checkpoint-after.json", saved)
			t.Logf("scenario=%s effect_count=%d process_killed=%v final=%q", scenario.name, count, barrier.killed, result.Content)
		})
	}
	t.Run("reject-incompatible-checkpoints", func(t *testing.T) {
		h := newDurableProcessHarness(t, fixture, "checkpoint-validation")
		acquire, stop, _ := h.start(t, 1)
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		driver, _, release, err := acquire(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		for _, test := range []struct {
			name, expected string
			change         func(*loopdriver.Checkpoint, map[string]any)
		}{
			{"contract", "unsupported driver checkpoint contract", func(cp *loopdriver.Checkpoint, _ map[string]any) { cp.ContractVersion = 2 }},
			{"driver-version", "driver checkpoint belongs", func(cp *loopdriver.Checkpoint, _ map[string]any) { cp.DriverVersion = "2.0.0" }},
			{"state-version", "incompatible_checkpoint", func(_ *loopdriver.Checkpoint, s map[string]any) { s["schema_version"] = 2 }},
			{"unknown-payload", "incompatible_checkpoint", func(_ *loopdriver.Checkpoint, s map[string]any) {
				s["transcript"] = "must not enter durable checkpoint"
			}},
			{"oversized", "checkpoint_too_large", func(_ *loopdriver.Checkpoint, s map[string]any) {
				s["input"] = map[string]any{"digest": strings.Repeat("x", 33000)}
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				state := map[string]any{"schema_version": 1, "session_id": h.sessionID, "run_id": "validation", "turn_id": "first", "phase": "ready", "attempt": 1, "restart_count": 0, "round": 0}
				cp := loopdriver.Checkpoint{ContractVersion: 1, DriverID: "example.durable-driver", DriverVersion: "1.0.0"}
				test.change(&cp, state)
				cp.State, err = json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				instance, err := driver.Resume(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "validation"}, loopdriver.PersistedInput{}, cp)
				if instance != nil {
					instance.Shutdown()
				}
				if err == nil || !strings.Contains(err.Error(), test.expected) {
					t.Fatalf("unsafe resume accepted or wrong error: %v", err)
				}
			})
		}
	})
	t.Run("restart-limit-stops-before-inference", func(t *testing.T) {
		h := newDurableProcessHarness(t, fixture, "restart-limit")
		acquire, stop, _ := h.start(t, 1)
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, binding, release, err := acquire(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		release()
		store := sessionDriverCheckpointStore{sessions: testSessionStore(t, h.sessions), sessionID: h.sessionID}
		if err := store.Bind(ctx, binding); err != nil {
			t.Fatal(err)
		}
		state, _ := json.Marshal(map[string]any{"schema_version": 1, "session_id": h.sessionID, "run_id": "restart-limited", "turn_id": "previous", "phase": "ready", "attempt": 6, "restart_count": 5, "round": 0})
		if err := store.Save(ctx, loopdriver.Checkpoint{ContractVersion: 1, DriverID: "example.durable-driver", DriverVersion: "1.0.0", State: state}); err != nil {
			t.Fatal(err)
		}
		model := &durableControlledModel{}
		marker := &durableMarkerTools{path: filepath.Join(h.root, "effects.txt")}
		initial := []providers.ChatMessage{{Role: "user", Content: "Do not restart beyond the configured bound."}}
		runner := &agent.StreamRunner{Client: model, Model: "controlled-durable", Tools: marker, DriverBindingStore: store, DriverCheckpointStore: store, AcquireLoopDriver: acquire}
		attachWorkflowHistory(t, runner, h.sessions, h.sessionID, initial)
		result, err := runner.RunWithCallback(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: h.sessionID, ExecutionID: "limit-check"}), initial, nil)
		if err == nil || !strings.Contains(err.Error(), "restart_limit") || model.calls != 0 || marker.count(t) != 0 {
			t.Fatalf("limit did not fence dispatch: result=%+v model=%d err=%v", result, model.calls, err)
		}
		h.write(t, "result.json", map[string]any{"scenario": "restart-limit", "model_requests": model.calls, "effect_count": marker.count(t), "error": err.Error()})
	})
}

type durableProcessHarness struct{ root, home, state, sessions, sessionID, fixture, evidence string }

func newDurableProcessHarness(t *testing.T, fixture, name string) *durableProcessHarness {
	t.Helper()
	root := t.TempDir()
	home, state := testWorkspaceState(t, root)
	evidence := os.Getenv("WUU_DURABLE_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	evidence = filepath.Join(evidence, name)
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	h := &durableProcessHarness{root: root, home: home, state: state, sessions: t.TempDir(), sessionID: "durable-" + name, fixture: fixture, evidence: evidence}
	if _, err := testSessionStore(t, h.sessions).Create(h.sessionID); err != nil {
		t.Fatal(err)
	}
	return h
}
func (h *durableProcessHarness) write(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.evidence, name), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func (h *durableProcessHarness) start(t *testing.T, generation int) (loopdriver.AcquireFunc, func(), string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(h.root, "plugin-process.sh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$1\"\nshift\nexec \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pid := filepath.Join(h.root, fmt.Sprintf("plugin-%d.pid", generation))
	provider := testRuntimePlugin("example.durable-driver")
	provider.Root = h.fixture
	provider.Runtime.Command = "/bin/sh"
	provider.Runtime.Args = []string{wrapper, pid, node, filepath.Join(h.fixture, "dist", "runtime.js")}
	modules := append(testStateModules(), kernelHostModule, sessionExecutionConsumer, "wuu.prompt.default", "wuu.compaction.default", "wuu.agent.policy.default", "wuu.model.native", "wuu.tools.default", provider.ID, "wuu.durable-wait")
	selection := &composition.Config{Version: composition.Version, Modules: &modules, Services: []composition.Binding{{Service: "driver.default", Major: 2, Provider: provider.ID}}}
	host, kernel, err := buildPluginHost([]pluginpkg.Plugin{provider}, h.root, "durable", h.home, h.state, nil, startPluginClient, nil, nil, selection, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := activatePluginHost(context.Background(), host); err != nil {
		_ = host.Close(context.Background())
		t.Fatal(err)
	}
	for _, status := range host.Statuses() {
		if status.ID == loopdriver.DefaultDriverID {
			t.Fatal("desktop default loop was loaded in durable composition")
		}
		if status.ID == provider.ID && status.State != pluginhost.StateActive {
			t.Fatalf("durable provider is not active: %+v", status)
		}
	}
	g := &PluginGeneration{host: host, driverGateways: kernel.driverGateways}
	g.retain()
	owner := &Session{pluginGeneration: g}
	var once sync.Once
	stop := func() { once.Do(func() { owner.releasePluginGeneration(g); _ = host.Close(context.Background()) }) }
	t.Cleanup(stop)
	return owner.driverResolver(g), stop, pid
}

type durableCheckpointRecorder struct {
	loopdriver.CheckpointStore
	directory, killPhase, pid, phase string
	sequence                         int
	killed                           bool
}

func (s *durableCheckpointRecorder) Save(ctx context.Context, checkpoint loopdriver.Checkpoint) error {
	if err := s.CheckpointStore.Save(ctx, checkpoint); err != nil {
		return err
	}
	var state struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(checkpoint.State, &state); err != nil {
		return err
	}
	s.phase = state.Phase
	s.sequence++
	raw, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.directory, fmt.Sprintf("checkpoint-%03d-%s.json", s.sequence, state.Phase)), raw, 0600); err != nil {
		return err
	}
	if state.Phase == s.killPhase && !s.killed {
		return s.kill()
	}
	return nil
}

func (s *durableCheckpointRecorder) kill() error {
	raw, err := os.ReadFile(s.pid)
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Kill(); err != nil {
		return err
	}
	s.killed = true
	return nil
}

type durableMarkerTools struct {
	path string
	fail bool
}

func (*durableMarkerTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "record_effect", InputSchema: map[string]any{"type": "object"}}}
}
func (*durableMarkerTools) ToolMetadata(providers.ToolCall) (agent.ToolMetadata, bool) {
	return agent.ToolMetadata{}, true
}
func (t *durableMarkerTools) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if call.Name != "record_effect" {
		return "", errors.New("unexpected tool")
	}
	f, err := os.OpenFile(t.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString("effect\n"); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if t.fail {
		return "", errors.New("recorded effect, returned a definite tool error")
	}
	return "recorded", nil
}
func (t *durableMarkerTools) count(test *testing.T) int {
	test.Helper()
	data, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		test.Fatal(err)
	}
	return strings.Count(string(data), "effect\n")
}

type durableControlledModel struct{ calls int }

func (*durableControlledModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("stream only")
}
func (m *durableControlledModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.calls++
	stream := make(chan providers.StreamEvent, 3)
	found := false
	for _, message := range request.Messages {
		if message.Role == "tool" && message.ToolInvocationID != "" {
			found = true
		}
	}
	if found {
		stream <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "Marker recorded once."}
		stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonStop}
	} else {
		call := providers.ToolCall{ID: "marker-call", Name: "record_effect", Arguments: "{}"}
		stream <- providers.StreamEvent{Type: providers.EventToolUseStart, ToolCall: &call}
		stream <- providers.StreamEvent{Type: providers.EventToolUseEnd, ToolCall: &call}
		stream <- providers.StreamEvent{Type: providers.EventDone, FinishReason: providers.FinishReasonToolCalls}
	}
	close(stream)
	return stream, nil
}
