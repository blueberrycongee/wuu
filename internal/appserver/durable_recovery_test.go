package appserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Proof contracts: old/no-checkpoint rows, ordinary error, explicit cancel,
// another driver, a newer input, stale checkpoint and held lease cannot recover.
// This isolates host admission proof; the cloud cases exercise real execution.
func TestPluginDriverRecoveryProof(t *testing.T) {
	for _, mode := range []string{"shutdown", "crash", "legacy", "cancel", "error", "other-driver", "new-input", "missing-checkpoint", "stale-checkpoint", "crash-after-recovery-admission", "cancel-before-terminal", "history-capability-upgrade"} {
		t.Run(mode, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
			owner := &pluginTurnLifecycleClient{id: "proof-fixture", calls: make(chan pluginhost.AgentTurnLifecycleInput, 2)}
			attachTestModelGeneration(t, rt, owner)
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			ctx := pluginInvocationContext(t, rt, context.Background())
			created, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "proof", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
			if err != nil {
				t.Fatal(err)
			}
			id := created.SessionID
			turn := id + "-turn-0001"
			now := time.Now().UTC()
			selected := loopdriver.Binding{Profile: "test", ProviderID: owner.id, ContractVersion: 1, DriverID: "proof-driver", DriverVersion: "1"}
			if mode == "other-driver" {
				selected.ProviderID = "someone-else"
			}
			if err = rt.Sessions.BindDriver(id, selected); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(selected)
			rows := []session.HistoryRecord{{Role: "user", Content: "original", At: now}}
			if mode != "legacy" {
				rows = append(rows, session.HistoryRecord{Role: "meta", Content: driverExecutionHistoryRecord, ClientID: turn, Cause: string(encoded), At: now})
			}
			if mode != "crash" && mode != "legacy" {
				cause := TurnError{Message: "interrupted"}
				if mode != "cancel" && mode != "error" {
					cause.Interruption = "shutdown"
				}
				raw, _ := json.Marshal(cause)
				rows = append(rows, session.HistoryRecord{Role: "meta", Content: turnTerminalHistoryRecord, ClientID: turn, StopReason: "interrupted", DisplayContent: cause.Message, Cause: string(raw), At: now})
			}
			if mode == "cancel-before-terminal" {
				rows = rows[:len(rows)-1]
				rows = append(rows, session.HistoryRecord{Role: "meta", Content: driverCancellationHistoryRecord, ClientID: turn, At: now.Add(time.Second)})
			}
			if mode == "new-input" {
				rows = append(rows, session.HistoryRecord{Role: "user", Content: "newer", At: now.Add(time.Second)})
			}
			if err = rt.Sessions.AppendHistoryRecords(id, rows); err != nil {
				t.Fatal(err)
			}
			if mode != "missing-checkpoint" {
				updated := now.Add(time.Second)
				if mode == "stale-checkpoint" {
					updated = now.Add(-time.Second)
				}
				if err = rt.Sessions.SaveDriverCheckpoint(id, session.DriverCheckpointRecord{ContractVersion: 1, DriverID: selected.DriverID, DriverVersion: selected.DriverVersion, State: json.RawMessage(`{"phase":"model_pending"}`), UpdatedAt: updated}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "crash-after-recovery-admission" {
				cp, _, readErr := rt.Sessions.LoadDriverCheckpoint(id)
				if readErr != nil {
					t.Fatal(readErr)
				}
				raw, _ := json.Marshal(driverRecoveryAdmission{Binding: selected, CheckpointHash: checkpointRecoveryHash(cp), SourceTurnID: turn})
				if err = rt.Sessions.AppendHistoryRecord(id, session.HistoryRecord{Role: "meta", Content: turnContinueHistoryRecord, ClientID: id + "-turn-0002", Cause: string(raw), At: now.Add(2 * time.Second)}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "history-capability-upgrade" {
				selected.HistoryState = true
				if err := rt.Sessions.BindDriver(id, selected); err != nil {
					t.Fatal(err)
				}
			}
			result, err := rt.PluginSessionRouter.Inspect(ctx, owner.id, pluginhost.SessionInspectParams{SessionID: id, TurnID: turn})
			if err != nil {
				t.Fatal(err)
			}
			expected := mode == "shutdown" || mode == "crash" || mode == "crash-after-recovery-admission" || mode == "history-capability-upgrade"
			if (result.Recovery != nil) != expected {
				t.Fatalf("mode=%s recovery=%+v", mode, result.Recovery)
			}
			if expected {
				lease := acquireExternalThreadLease(t, rt.Sessions, rt.SessionDir, id)
				busy, inspectErr := rt.PluginSessionRouter.Inspect(ctx, owner.id, pluginhost.SessionInspectParams{SessionID: id, TurnID: turn})
				if inspectErr != nil || busy.Recovery != nil {
					t.Fatalf("held lease exposed recovery: %+v %v", busy, inspectErr)
				}
				_, sendErr := rt.PluginSessionRouter.Send(ctx, owner.id, pluginhost.SessionSendParams{RequestID: "busy-proof", SessionID: id, RecoverAfterTurnID: turn, RecoveryCheckpointToken: result.Recovery.CheckpointToken})
				if sendErr == nil {
					t.Fatal("held lease admitted recovery")
				}
				if err = lease.Release(); err != nil {
					t.Fatal(err)
				}
				if err = rt.Sessions.AppendHistoryRecord(id, session.HistoryRecord{Role: "user", Content: "input arriving after inspect", At: now.Add(2 * time.Second)}); err != nil {
					t.Fatal(err)
				}
				_, sendErr = rt.PluginSessionRouter.Send(ctx, owner.id, pluginhost.SessionSendParams{RequestID: "stale-after-input", SessionID: id, RecoverAfterTurnID: turn, RecoveryCheckpointToken: result.Recovery.CheckpointToken})
				if sendErr == nil {
					t.Fatal("new user input did not fence previously issued proof")
				}
			}
			_, err = rt.PluginSessionRouter.Send(ctx, owner.id, pluginhost.SessionSendParams{RequestID: "stale", SessionID: id, RecoverAfterTurnID: turn, RecoveryCheckpointToken: "not-the-current-token"})
			if err == nil {
				t.Fatal("stale token admitted")
			}
		})
	}
}

func TestDurableCloudActiveRootRecovery(t *testing.T) {
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	j := newDurableControlJourney(t, "active-root-recovery", gate)
	t.Cleanup(func() { gate.released.Do(func() { close(gate.release) }) })
	j.start(t, "", nil, "child-private-prompt-marker")
	select {
	case <-gate.entered:
	case <-j.ctx.Done():
		t.Fatalf("root did not enter model: %s tasks=%v", j.out.String(), durableChildStoredTasks(t, j))
	}
	before := durableChildStoredTasks(t, j)
	run := ""
	for _, value := range before {
		task := value.(map[string]any)
		if task["kind"] == "run" {
			run = task["id"].(string)
		}
	}
	j.server.Close()
	proofState, err := j.rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := j.server.driverRecoveryCandidate(proofState, "example.durable-driver", j.id, "", false)
	_ = proofState.Close()
	if err != nil || proof == nil {
		t.Fatalf("shutdown did not produce recovery proof: %+v %v", proof, err)
	}
	if err := j.closeRuntime(); err != nil {
		t.Fatal(err)
	}
	gate.released.Do(func() { close(gate.release) })
	reopened, err := runtime.NewSession(j.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Cleanup() })
	j.rt = reopened
	j.out = &lockedBuffer{}
	j.server = New(reopened, j.out)
	t.Cleanup(j.server.Close)
	deadline := time.Now().Add(25 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		for _, value := range durableChildStoredTasks(t, j) {
			task := value.(map[string]any)
			if task["id"] == run && task["status"] == "completed" {
				completed = true
			}
		}
		if completed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !completed {
		t.Fatalf("root did not recover original run: %v; events=%s", durableChildStoredTasks(t, j), j.out.String())
	}
	history, err := j.rt.Sessions.LoadHistoryRecords(j.id, false)
	if err != nil {
		t.Fatal(err)
	}
	inputs := 0
	for _, row := range history {
		if row.Role == "user" && strings.Contains(row.Content, "child-private-prompt-marker") {
			inputs++
		}
	}
	if inputs != 1 {
		t.Fatalf("recovery duplicated original input: %d", inputs)
	}
	replayCtx := pluginInvocationContext(t, reopened, context.Background())
	_, err = reopened.PluginSessionRouter.Send(replayCtx, "example.durable-driver", pluginhost.SessionSendParams{RequestID: "duplicate-original-recovery", SessionID: j.id, RecoverAfterTurnID: proof.TurnID, RecoveryCheckpointToken: proof.CheckpointToken})
	if err == nil {
		t.Fatal("duplicate recovery admitted after its successor completed")
	}
	durableCompletionEvidence(t, j, gate, "cloud-active-root-recovery", map[string]any{"run_id": run, "original_inputs": inputs, "tasks_before": before})
	if dir := os.Getenv("WUU_DURABLE_EVIDENCE"); dir != "" {
		raw, _ := json.MarshalIndent(history, "", "  ")
		if err = os.WriteFile(filepath.Join(dir, "cloud-active-root-recovery-history.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// The app-server itself is killed, not merely its plugin. Recovery therefore
// relies on the abandoned execution lease plus the opt-in admission history.
func TestDurableCloudKilledHostRecovery(t *testing.T) {
	for _, mode := range []string{"root", "child"} {
		t.Run(mode, func(t *testing.T) {
			gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
			gate.released.Do(func() { close(gate.release) })
			j := newDurableControlJourney(t, "killed-host-"+mode, gate)
			j.server.Close()
			if err := j.closeRuntime(); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			saved := durableRecoveryProcessConfig{Root: j.options.RootDir, Home: j.options.HomeDir, Wuu: j.options.WuuHome, Config: j.options.Config, Plugins: *j.options.DiscoveredPlugins, Session: j.id, Mode: mode, Names: j.names}
			raw, err := json.Marshal(saved)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.json")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			logFile, err := os.Create(filepath.Join(dir, "host.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer logFile.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestDurableRecoveryProcess$")
			cmd.Env = append(os.Environ(), "WUU_DURABLE_RECOVERY_PROCESS="+path)
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			})
			ready := filepath.Join(dir, "ready.json")
			deadline := time.Now().Add(30 * time.Second)
			var proof struct {
				Session string `json:"session"`
				Run     string `json:"run"`
			}
			for time.Now().Before(deadline) {
				if raw, err = os.ReadFile(ready); err == nil {
					if err = json.Unmarshal(raw, &proof); err != nil {
						t.Fatal(err)
					}
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if proof.Run == "" {
				log, _ := os.ReadFile(filepath.Join(dir, "host.log"))
				t.Fatalf("child host did not reach active checkpoint: %s", log)
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = cmd.Wait(); err == nil {
				t.Fatal("host was not killed")
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
			view := *j
			view.id = proof.Session
			done := false
			deadline = time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				for _, value := range durableChildStoredTasks(t, &view) {
					task := value.(map[string]any)
					if task["id"] == proof.Run && task["status"] == "completed" {
						done = true
					}
				}
				if done {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !done {
				t.Fatalf("killed host work did not recover: %v; events=%s", durableChildStoredTasks(t, &view), j.out.String())
			}
			durableCompletionEvidence(t, j, gate, "cloud-killed-host-"+mode, map[string]any{"signal": "SIGKILL", "session_id": proof.Session, "run_id": proof.Run, "recovered_tasks": durableChildStoredTasks(t, &view)})
		})
	}
}

type durableRecoveryProcessConfig struct {
	Root, Home, Wuu, Session, Mode string
	Config                         config.Config
	Plugins                        []pluginpkg.Plugin
	Names                          map[string]string
}

func TestDurableRecoveryProcess(t *testing.T) {
	path := os.Getenv("WUU_DURABLE_RECOVERY_PROCESS")
	if path == "" {
		t.Skip("subprocess fixture")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved durableRecoveryProcessConfig
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	gate := &durableChildGate{calls: map[string]*providers.ToolCall{}, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
	opts := runtime.Options{RootDir: saved.Root, HomeDir: saved.Home, WuuHome: saved.Wuu, Config: saved.Config, DiscoveredPlugins: &saved.Plugins, NonInteractive: true, NativeProviders: []runtime.NativeProviderFactory{{ID: "fixture.model-configuration", Build: func(c runtime.NativeProviderContext) (pluginhost.Client, error) {
		return &durableCloudModel{embeddedModelStreamFixture: embeddedModelStreamFixture{client: gate, host: c.Host}}, nil
	}}}}
	rt, err := runtime.NewSession(opts)
	if err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	j := &durableControlJourney{rt: rt, server: srv, out: out, ctx: ctx, id: saved.Session, names: saved.Names}
	prompt := "child-private-prompt-marker"
	if saved.Mode == "child" {
		prompt = "launch-before-kill"
		args, _ := json.Marshal(map[string]any{"prompt": "child-private-prompt-marker"})
		gate.calls[prompt] = &providers.ToolCall{ID: "killed-host-child", Name: j.names["durable_subagent"], Arguments: string(args)}
	}
	j.start(t, "", nil, prompt)
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("model did not start")
	}
	target := j.id
	if saved.Mode == "child" {
		j.completed(t, 1)
		for _, value := range durableChildStoredTasks(t, j) {
			task := value.(map[string]any)
			if task["kind"] == "subagent" {
				target = task["child_session_id"].(string)
			}
		}
	}
	view := *j
	view.id = target
	run := ""
	for _, value := range durableChildStoredTasks(t, &view) {
		task := value.(map[string]any)
		if task["kind"] == "run" {
			run = task["id"].(string)
		}
	}
	if run == "" {
		t.Fatal("missing active run")
	}
	proof, _ := json.Marshal(map[string]string{"session": target, "run": run})
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), "ready.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	t.Fatal("parent did not kill the host")
}

func TestDefaultLoopHasNoRecoveryAdmission(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "default response"}})
	id := "default-no-recovery"
	if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	response := durableIngressCall(t, srv, out, context.Background(), "default-start", "turn/start", map[string]string{"thread_id": id, "prompt": "ordinary default loop"})
	if response["error"] != nil {
		t.Fatal(response)
	}
	waitForTurnCompletedForThread(t, out, id)
	history, err := rt.Sessions.LoadHistoryRecords(id, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range history {
		if row.Content == driverExecutionHistoryRecord {
			t.Fatal("default loop acquired durable recovery state")
		}
	}
}

// Audit probe: interrupt acknowledged while driver terminal persistence has not run.
func TestPluginDriverRecoveryCancelBeforeTerminal(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "proof-fixture", calls: make(chan pluginhost.AgentTurnLifecycleInput, 2)}
	attachTestModelGeneration(t, rt, owner)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, context.Background())
	created, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "cancel-window", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	id := created.SessionID
	turn := id + "-turn-0001"
	now := time.Now().UTC()
	selected := loopdriver.Binding{Profile: "test", ProviderID: owner.id, ContractVersion: 1, DriverID: "proof-driver", DriverVersion: "1"}
	if err := rt.Sessions.BindDriver(id, selected); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(selected)
	if err := rt.Sessions.AppendHistoryRecords(id, []session.HistoryRecord{{Role: "user", Content: "original", At: now}, {Role: "meta", Content: driverExecutionHistoryRecord, ClientID: turn, Cause: string(raw), At: now}}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.SaveDriverCheckpoint(id, session.DriverCheckpointRecord{ContractVersion: 1, DriverID: selected.DriverID, DriverVersion: selected.DriverVersion, State: json.RawMessage(`{"phase":"model_pending"}`), UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	state, err := rt.RetainState()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	th, err := srv.ensureThreadLoaded(state, id)
	if err != nil {
		t.Fatal(err)
	}
	canceled := false
	th.mu.Lock()
	th.cancel = func() { canceled = true }
	th.currentTurn = turn
	th.running = true
	th.mu.Unlock()
	if err := recordDriverRecoveryAdmission(context.Background(), th, turn, selected, loopdriver.Descriptor{Capabilities: []string{durableRecoveryCapability}}); err != nil {
		t.Fatal(err)
	}
	activeProof, proofErr := srv.driverRecoveryCandidate(state, owner.id, id, turn, false)
	if proofErr != nil || activeProof == nil {
		t.Fatalf("fixture lacked live recovery proof before cancel: %+v %v", activeProof, proofErr)
	}
	result, err := rt.PluginSessionRouter.Cancel(ctx, owner.id, pluginhost.SessionCancelParams{SessionID: id, TurnID: turn})
	th.mu.Lock()
	th.cancel = nil
	th.running = false
	th.mu.Unlock()
	if err != nil || !result.Cancelled || !canceled {
		t.Fatalf("cancel result=%+v called=%v err=%v", result, canceled, err)
	}
	// The process dies before its asynchronous runTurn has persisted terminal state.
	// Candidate depends only on persisted state and a now-released OS lease.
	proof, err := srv.driverRecoveryCandidate(state, owner.id, id, turn, false)
	if err != nil {
		t.Fatal(err)
	}
	if proof != nil {
		t.Fatalf("successful explicit cancellation remained automatically recoverable: %+v", proof)
	}
}

func TestPluginDriverRecoveryIntermediateTurnChain(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
	owner := &pluginTurnLifecycleClient{id: "proof-fixture", calls: make(chan pluginhost.AgentTurnLifecycleInput, 2)}
	attachTestModelGeneration(t, rt, owner)
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	ctx := pluginInvocationContext(t, rt, context.Background())
	created, err := rt.PluginSessionRouter.Create(ctx, owner.id, pluginhost.SessionCreateParams{RequestID: "cancel-window", Visibility: pluginhost.SessionVisibilityPlugin, ContextSource: pluginhost.SessionContextFresh})
	if err != nil {
		t.Fatal(err)
	}
	id := created.SessionID
	turn := id + "-turn-0001"
	now := time.Now().UTC()
	selected := loopdriver.Binding{Profile: "test", ProviderID: owner.id, ContractVersion: 1, DriverID: "proof-driver", DriverVersion: "1"}
	if err := rt.Sessions.BindDriver(id, selected); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(selected)
	if err := rt.Sessions.AppendHistoryRecords(id, []session.HistoryRecord{{Role: "user", Content: "original", At: now}, {Role: "meta", Content: driverExecutionHistoryRecord, ClientID: turn, Cause: string(raw), At: now}}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.SaveDriverCheckpoint(id, session.DriverCheckpointRecord{ContractVersion: 1, DriverID: selected.DriverID, DriverVersion: selected.DriverVersion, State: json.RawMessage(`{"phase":"model_pending"}`), UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	cp, _, err := rt.Sessions.LoadDriverCheckpoint(id)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := json.Marshal(driverRecoveryAdmission{Binding: selected, CheckpointHash: checkpointRecoveryHash(cp), SourceTurnID: turn})
	turn2 := id + "-turn-0002"
	if err = rt.Sessions.AppendHistoryRecord(id, session.HistoryRecord{Role: "meta", Content: turnContinueHistoryRecord, ClientID: turn2, Cause: string(p2), At: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	// The first recovery has run and durably moved the plugin root's turn_ref to turn2.
	cp.State = json.RawMessage(`{"phase":"model_pending","turn_ref":"second-recovery"}`)
	cp.UpdatedAt = now.Add(3 * time.Second)
	if err = rt.Sessions.SaveDriverCheckpoint(id, cp); err != nil {
		t.Fatal(err)
	}
	p3, _ := json.Marshal(driverRecoveryAdmission{Binding: selected, CheckpointHash: checkpointRecoveryHash(cp), SourceTurnID: turn})
	turn3 := id + "-turn-0003"
	if err = rt.Sessions.AppendHistoryRecord(id, session.HistoryRecord{Role: "meta", Content: turnContinueHistoryRecord, ClientID: turn3, Cause: string(p3), At: now.Add(4 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	// The second recovery crashes before replacing that checkpoint/root turn_ref.
	got, err := rt.PluginSessionRouter.Inspect(ctx, owner.id, pluginhost.SessionInspectParams{SessionID: id, TurnID: turn2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Recovery == nil {
		t.Fatal("latest interrupted admission lost the intermediate checkpoint-owning physical turn")
	}
}
