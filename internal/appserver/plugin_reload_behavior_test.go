package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// The fixture speaks the process wire protocol directly, so these assertions
// cover generation ownership and routing without a plugin authoring SDK.
func TestPluginReloadBehaviorProcess(t *testing.T) {
	marker := os.Getenv("WUU_RELOAD_TEST_MARKER")
	if marker == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	activationID := ""
	for scanner.Scan() {
		var req struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		var callErr error
		switch req.Method {
		case "initialize":
			if path := os.Getenv("WUU_RELOAD_TEST_ATTEMPTS"); path != "" {
				file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					os.Exit(2)
				}
				_, callErr = fmt.Fprintln(file, time.Now().UnixNano())
				if err := file.Close(); err != nil {
					callErr = err
				}
			}
			if path := os.Getenv("WUU_RELOAD_TEST_FAILURE"); path != "" {
				if message, err := os.ReadFile(path); err == nil {
					callErr = fmt.Errorf("%s", message)
				}
			}
			definition := pluginhost.CapabilityInitializeResult{
				ProtocolVersion:  pluginhost.CapabilityProtocolVersion,
				LifecycleVersion: pluginhost.RuntimeLifecycleVersion,
				InitializeResult: pluginhost.InitializeResult{Tools: []pluginhost.ToolRegistration{{ID: "read", Description: os.Getenv("WUU_RELOAD_TEST_DESCRIPTION"), InputSchema: map[string]any{"type": "object"}, Activity: &pluginhost.ToolActivityMetadata{ReadOnly: true, ConcurrencySafe: true, Risk: "low"}}}},
			}
			if os.Getenv("WUU_RELOAD_TEST_SESSION") != "" {
				definition.RequiredServices = []pluginhost.ServiceRequirement{{Name: pluginhost.KernelSessionSendService, MajorVersion: 1, Required: true}}
			}
			result = definition
		case "activate":
			if message := os.Getenv("WUU_RELOAD_TEST_ACTIVATE_ERROR"); message != "" {
				callErr = fmt.Errorf("%s", message)
			}
			if target := os.Getenv("WUU_RELOAD_TEST_SESSION"); target != "" && callErr == nil {
				activationID = req.ID
				_ = enc.Encode(map[string]any{"id": "fixture-session-send", "method": "host.service.call", "params": pluginhost.ServiceCallParams{Service: pluginhost.KernelSessionSendService, Method: "call", Params: json.RawMessage(fmt.Sprintf(`{"request_id":"activate-send","session_id":%q,"input":{"prompt":"activation callback"}}`, target))}})
				continue
			}
		case "":
			if req.ID != "fixture-session-send" || activationID == "" {
				continue
			}
			if len(req.Error) != 0 && string(req.Error) != "null" {
				_ = enc.Encode(map[string]any{"id": activationID, "error": req.Error})
				activationID = ""
				continue
			}
			callErr = os.WriteFile(os.Getenv("WUU_RELOAD_TEST_CALLBACK_RESULT"), req.Result, 0600)
			req.ID, activationID = activationID, ""
		case "tool.execute":
			result = map[string]any{"result": map[string]any{"content": []map[string]string{{"type": "text", "text": marker}}}}
		case "shutdown":
			callErr = os.WriteFile(os.Getenv("WUU_RELOAD_TEST_SHUTDOWN"), []byte(marker), 0600)
		}
		if callErr != nil {
			_ = enc.Encode(map[string]any{"id": req.ID, "error": map[string]string{"code": "fixture_error", "message": callErr.Error()}})
		} else {
			_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
		}
		if req.Method == "shutdown" {
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func TestPluginRefreshFailureBackoffKeepsRealRuntimeAndManualRetry(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	control := t.TempDir()
	attemptsPath := filepath.Join(control, "attempts")
	failurePath := filepath.Join(control, "failure")
	manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: "retry-fixture", Runtime: &pluginpkg.RuntimeSpec{
		Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestPluginReloadBehaviorProcess$"},
		Env: map[string]string{"WUU_RELOAD_TEST_MARKER": "last good runtime", "WUU_RELOAD_TEST_DESCRIPTION": "Read fixture",
			"WUU_RELOAD_TEST_SHUTDOWN": filepath.Join(control, "shutdown"), "WUU_RELOAD_TEST_ATTEMPTS": attemptsPath, "WUU_RELOAD_TEST_FAILURE": failurePath},
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rt.WuuHome, "plugins", manifest.ID, "plugin.json")
	writePluginPackageFile(t, path, string(raw))
	item, err := pluginpkg.LoadManifest(path, "user")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		DefaultProvider: "fake-provider",
		Providers:       map[string]config.ProviderConfig{"fake-provider": {Type: "openai-compatible", BaseURL: "https://example.test/v1", Model: "fake-model"}},
		Extensions:      &extensions.Settings{},
	}
	if err := cfg.Extensions.RecordGrant(extensions.Grant{SubjectID: item.SubjectID, Fingerprint: item.Fingerprint, Permissions: item.EffectivePermissions}); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, rt.ConfigPath, string(raw))
	if _, _, err := rt.LoadEffectiveConfig(); err != nil {
		t.Fatal(err)
	}
	if err := rt.RefreshExtensions(cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	oldHost := rt.PluginHost
	pinned := rt.RetainPluginGeneration()
	defer rt.ReleasePluginGeneration(pinned)
	toolName := ""
	for _, tool := range oldHost.ToolDefinitions() {
		if registered, ok := oldHost.Tool(tool.Name); ok && registered.PluginID == manifest.ID {
			toolName = tool.Name
		}
	}
	if toolName == "" {
		t.Fatalf("fixture tool is missing: %+v", oldHost.Statuses())
	}
	assertOldTool := func() {
		t.Helper()
		result, err := oldHost.ExecuteTool(context.Background(), toolName, pluginhost.ToolExecuteInput{
			ThreadID: "retained-thread", TurnID: "retained-turn", CallID: "read", CWD: rt.RootDir, Arguments: json.RawMessage(`{}`),
		})
		if err != nil || !strings.Contains(fmt.Sprint(result), "last good runtime") {
			t.Fatalf("retained process is unusable: %+v, %v", result, err)
		}
	}
	attempts := func() int {
		t.Helper()
		data, err := os.ReadFile(attemptsPath)
		if err != nil {
			t.Fatal(err)
		}
		return len(strings.Fields(string(data)))
	}
	out := &lockedBuffer{}
	srv := &Server{rt: rt, out: out, threads: map[string]*threadState{}}
	defer srv.Close()
	writePluginPackageFile(t, failurePath, "initialization refused")
	epoch := advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
	if err := srv.refreshPluginGenerationIfChanged(); err == nil || !strings.Contains(err.Error(), "initialization refused") {
		t.Fatalf("candidate failure was hidden: %v", err)
	}
	for range 8 {
		_ = srv.refreshPluginGenerationIfChanged()
		thread := &threadState{ID: "waiting-for-recovery"}
		thread.mu.Lock()
		acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(thread)
		thread.mu.Unlock()
		if err != nil || acquired {
			t.Fatalf("turn admission did not wait for recovery: acquired=%t err=%v", acquired, err)
		}
		srv.backgroundWG.Wait()
	}
	if attempts() != 2 || rt.PluginHost != oldHost || srv.pluginGenerationEpoch.Load() == epoch {
		t.Fatalf("failed candidate was rebuilt or published: attempts=%d epoch=%d", attempts(), srv.pluginGenerationEpoch.Load())
	}
	assertOldTool()
	// The real Refresh RPC is an explicit attempt even during automatic backoff.
	callPluginPackageRPC(t, srv, "retry-failed", MethodExtensionCatalogRefresh, struct{}{})
	response := responseByID(t, parseOutput(t, out.String()), "retry-failed")
	if response["error"] == nil || attempts() != 3 || rt.PluginHost != oldHost {
		t.Fatalf("manual retry did not attempt and report failure: %+v; attempts=%d", response, attempts())
	}
	assertOldTool()
	if err := os.Remove(failurePath); err != nil {
		t.Fatal(err)
	}
	callPluginPackageRPC(t, srv, "retry-repaired", MethodExtensionCatalogRefresh, struct{}{})
	response = responseByID(t, parseOutput(t, out.String()), "retry-repaired")
	if response["error"] != nil || attempts() != 4 || rt.PluginHost == oldHost {
		t.Fatalf("repaired manual retry did not publish: %+v; attempts=%d", response, attempts())
	}
	assertOldTool()
	t.Logf("real plugin initialize attempts=%d; failed candidates retained callable old process; manual failure and recovery reported through Refresh RPC", attempts())
}

func publishReloadBehaviorGeneration(t *testing.T, rt *runtime.Session, marker, description string) {
	t.Helper()
	if rt.Toolkit == nil {
		kit, err := tools.New(rt.RootDir)
		if err != nil {
			t.Fatal(err)
		}
		rt.Toolkit = kit
		rt.StreamRunner.Tools = kit
	}
	root := t.TempDir()
	manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: "reload-fixture", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestPluginReloadBehaviorProcess$"}, Env: map[string]string{"WUU_RELOAD_TEST_MARKER": marker, "WUU_RELOAD_TEST_DESCRIPTION": description, "WUU_RELOAD_TEST_SHUTDOWN": filepath.Join(root, "shutdown-observed")}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plugin.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	item, err := pluginpkg.LoadManifestWithOptions(path, pluginpkg.LoadOptions{Source: "user", Official: true})
	if err != nil {
		t.Fatal(err)
	}
	rt.Plugins = []pluginpkg.Plugin{item}
	candidate, err := rt.PreflightExtensionPolicy(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.ActivatePluginGeneration(candidate, nil); err != nil {
		t.Fatal(err)
	}
	if len(rt.PluginHost.ToolDefinitions()) != 1 {
		t.Fatalf("fixture did not register its tool: %+v", rt.PluginHost.Statuses())
	}
}

func TestPluginReloadAdoptsNextIdleTurnWithoutLosingConversation(t *testing.T) {
	for _, changedSurface := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed_surface_%t", changedSurface), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			client := &fakeClient{onChat: func(call int, _ providers.ChatRequest) {
				if call == 1 {
					close(entered)
					<-release
				}
			}}
			rt := newTestRuntime(t, client)
			publishReloadBehaviorGeneration(t, rt, "old implementation", "Read the fixture")
			t.Cleanup(func() { _, _ = rt.Cleanup() })
			toolName := rt.PluginHost.ToolDefinitions()[0].Name
			client.responses = []providers.ChatResponse{
				{ToolCalls: []providers.ToolCall{{ID: "old-call", Name: toolName, Arguments: `{}`}}}, {Content: "first finished"},
				{ToolCalls: []providers.ToolCall{{ID: "new-call", Name: toolName, Arguments: `{}`}}}, {Content: "second finished"},
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(func() { unblock(); srv.Close() })
			if err := srv.handleLine(context.Background(), []byte(`{"id":"create","method":"thread/start"}`)); err != nil {
				t.Fatal(err)
			}
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "create")["result"]).Thread.ID
			start := func(id string) {
				t.Helper()
				raw := []byte(fmt.Sprintf(`{"id":%q,"method":"turn/start","params":{"thread_id":%q,"prompt":%q}}`, id, threadID, id))
				if err := srv.handleLine(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				if response := responseByID(t, parseOutput(t, out.String()), id); response["error"] != nil {
					t.Fatalf("turn rejected: %+v", response)
				}
			}
			start("first")
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first turn never reached provider")
			}
			th := srv.thread(threadID)
			th.mu.Lock()
			firstRuntime := th.execRuntime
			th.mu.Unlock()
			description := "Read the fixture"
			if changedSurface {
				description = "Read the updated fixture"
			}
			publishReloadBehaviorGeneration(t, rt, "new implementation", description)
			srv.retireIdlePluginRuntimes()
			srv.pluginRuntimeRevision.Add(1)
			active, err := srv.ensureThreadRuntime(th)
			if err != nil {
				t.Fatal(err)
			}
			if active != firstRuntime {
				t.Fatal("reload replaced a running turn's runtime")
			}
			unblock()
			waitForTurnCompletedCountForThread(t, out, threadID, 1)
			start("second")
			waitForTurnCompletedCountForThread(t, out, threadID, 2)
			client.mu.Lock()
			requests := append([]providers.ChatRequest(nil), client.requests...)
			client.mu.Unlock()
			if len(requests) != 4 {
				t.Fatalf("provider requests = %d, want 4", len(requests))
			}
			assertResult := func(req providers.ChatRequest, callID, marker string) {
				t.Helper()
				for _, msg := range req.Messages {
					if msg.Role == "tool" && msg.ToolCallID == callID && strings.Contains(msg.Content, marker) {
						return
					}
				}
				t.Fatalf("request lost %s result %q: %+v", callID, marker, req.Messages)
			}
			assertResult(requests[1], "old-call", "old implementation")
			assertResult(requests[2], "old-call", "old implementation")
			assertResult(requests[3], "new-call", "new implementation")
			persisted, err := loadChatMessages(rt.SessionDir, threadID)
			if err != nil {
				t.Fatal(err)
			}
			assertResult(providers.ChatRequest{Messages: persisted}, "old-call", "old implementation")
			assertResult(providers.ChatRequest{Messages: persisted}, "new-call", "new implementation")
			if requests[0].CacheHint == nil || requests[2].CacheHint == nil || requests[0].CacheHint.PromptCacheKey == "" || requests[0].CacheHint.PromptCacheKey != requests[2].CacheHint.PromptCacheKey {
				t.Fatal("reload changed the conversation's prompt cache key")
			}
			if changedSurface == reflect.DeepEqual(requests[0].Tools, requests[2].Tools) {
				t.Fatalf("tool surface change = %t, got unchanged=%t", changedSurface, reflect.DeepEqual(requests[0].Tools, requests[2].Tools))
			}
			if !changedSurface && requests[0].Messages[0].Content != requests[2].Messages[0].Content {
				t.Fatal("implementation-only reload changed the stable system prompt")
			}
			if srv.thread(threadID) != th {
				t.Fatal("reload replaced conversation identity")
			}
		})
	}
}

// A background worker can outlive its foreground turn. It retains the runtime
// and its plugin resources until finalization, even when a newer generation is live.
func TestPluginReloadWaitsForOutstandingBackgroundWork(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	publishReloadBehaviorGeneration(t, rt, "old implementation", "Read the fixture")
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	oldHost := rt.PluginHost
	shutdownPath := rt.Plugins[0].Runtime.Env["WUU_RELOAD_TEST_SHUTDOWN"]
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	worker := newBlockingStreamClient("worker finished")
	var once sync.Once
	unblock := func() { once.Do(func() { close(worker.release) }) }
	t.Cleanup(unblock)
	control, err := agentcontrol.New(agentcontrol.Config{
		Client: worker, DefaultModel: "fake-model", ParentRepo: rt.RootDir,
		WorktreeRoot: filepath.Join(rt.RootDir, ".wuu", "worktrees"), SessionID: "reload-background",
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return noopToolExecutor{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unblock(); control.StopAll(); control.Close() })
	th := newThreadState("reload-background", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()
	first, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	// Use a deterministic local worker instead of any network-backed provider.
	releaseThreadRuntimeSubscription(first, th.runtimeSubscription)
	first.AgentControl = control
	th.runtimeSubscription = srv.subscribeThreadRuntime(th.ID, first)
	// Drop terminal status fanout while leaving the reliable finalizer attached.
	// Retirement must be driven by the lease quiescence boundary, not notifications.
	control.Unsubscribe(th.runtimeSubscription.statusCh)
	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type: agentcontrol.DefaultSubagentType, TaskName: "pin_generation", Description: "Retain the plugin runtime until finalization", Prompt: "wait", Isolation: string(agentcontrol.IsolationInplace),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	th.mu.Lock()
	acquired, admissionErr := srv.tryAcquireThreadExecutionLeaseLocked(th)
	if admissionErr == nil && acquired {
		th.releaseThreadExecutionLeaseLocked()
	}
	loopStarted := th.pluginExecutionLease != nil && th.pluginLeaseReleaseLoop
	th.mu.Unlock()
	if admissionErr != nil || !acquired || !loopStarted {
		t.Fatalf("foreground lease did not wait for its background worker: acquired=%v loop=%v err=%v", acquired, loopStarted, admissionErr)
	}
	publishReloadBehaviorGeneration(t, rt, "new implementation", "Read the fixture")
	srv.retireIdlePluginRuntimes()
	retained, err := srv.ensureThreadRuntime(th)
	if err != nil {
		t.Fatal(err)
	}
	if retained != first {
		t.Fatal("reload replaced a runtime while its background worker was active")
	}
	unblock()
	waitForAgentStatus(t, control, spawned.AgentID, subagent.StatusCompleted)
	waitForWorkerFinalization(t, control)
	// Retirement must not require another user turn. The reliable worker
	// finalizer may independently deliver a completion and build a new runtime;
	// only retaining the old generation is a lifecycle failure.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		th.mu.Lock()
		current := th.execRuntime == nil || rt.IsCurrentPluginGeneration(th.execRuntime.PluginGeneration)
		t.Logf("retirement state: old_runtime_retained=%v current_generation=%v lease=%v poll=%v running=%v admission=%v outstanding=%v", th.execRuntime == first, current, th.pluginExecutionLease != nil, th.pluginLeaseReleaseLoop, th.running, th.admissionReserved, threadRuntimeHasOutstandingWork(th.ID, th.execRuntime))
		th.mu.Unlock()
		raw, err := os.ReadFile(shutdownPath)
		t.Logf("retired host statuses=%+v shutdown=%q read_error=%v", oldHost.Statuses(), raw, err)
	})
	waitPluginGenerationWatchTest(t, func() bool {
		th.mu.Lock()
		retired := th.execRuntime != first && th.pluginExecutionLease == nil
		if th.execRuntime != nil {
			retired = retired && rt.IsCurrentPluginGeneration(th.execRuntime.PluginGeneration)
		}
		th.mu.Unlock()
		shutdown, err := os.ReadFile(shutdownPath)
		return retired && err == nil && string(shutdown) == "old implementation"
	})
	if len(oldHost.Statuses()) != 0 || len(oldHost.ToolDefinitions()) != 0 {
		t.Fatal("quiescent background generation retained its host registrations")
	}
	if statuses := rt.PluginHost.Statuses(); len(statuses) != 1 || statuses[0].State != pluginhost.StateActive {
		t.Fatalf("background retirement affected the live generation: %+v", statuses)
	}
}

// The public mutation transaction must retire idle consumers immediately;
// waiting for another turn leaves plugin processes alive indefinitely.
func TestPluginReloadRetiresIdleGenerationOnMutationCommit(t *testing.T) {
	client := &fakeClient{response: providers.ChatResponse{Content: "saved answer"}}
	rt := newTestRuntime(t, client)
	rt.WuuHome = t.TempDir()
	publishReloadBehaviorGeneration(t, rt, "old implementation", "Read the fixture")
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	oldHost := rt.PluginHost
	shutdownPath := rt.Plugins[0].Runtime.Env["WUU_RELOAD_TEST_SHUTDOWN"]
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"create","method":"thread/start"}`)); err != nil {
		t.Fatal(err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "create")["result"]).Thread.ID
	request := fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"save this conversation"}}`, threadID)
	if err := srv.handleLine(context.Background(), []byte(request)); err != nil {
		t.Fatal(err)
	}
	waitForTurnCompletedCountForThread(t, out, threadID, 1)
	th := srv.thread(threadID)
	th.mu.Lock()
	oldRuntime := th.execRuntime
	history := cloneHistory(th.History)
	th.mu.Unlock()
	if oldRuntime == nil {
		t.Fatal("completed turn did not retain its current runtime")
	}
	persistedBefore, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	// An epoch advance with no actual replacement must not churn the runtime.
	release, err := srv.beginPluginGenerationMutation("catalog test", pluginGenerationMutationCatalog)
	if err != nil {
		t.Fatal(err)
	}
	release()
	th.mu.Lock()
	retained := th.execRuntime
	th.mu.Unlock()
	if retained != oldRuntime || len(oldHost.Statuses()) != 1 {
		t.Fatal("catalog-only commit retired the still-current generation")
	}
	if _, err := os.Stat(shutdownPath); !os.IsNotExist(err) {
		t.Fatalf("old process shutdown before replacement: %v", err)
	}
	release, err = srv.beginPluginGenerationMutation("replace test", pluginGenerationMutationLive)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer release()
		publishReloadBehaviorGeneration(t, rt, "new implementation", "Read the fixture")
	}()
	th.mu.Lock()
	current := th.execRuntime
	historyAfter := cloneHistory(th.History)
	th.mu.Unlock()
	if current != nil {
		t.Fatal("superseded idle runtime survived the completed mutation")
	}
	if len(oldHost.Statuses()) != 0 || len(oldHost.ToolDefinitions()) != 0 {
		t.Fatal("old host retained a process or callable tools after idle retirement")
	}
	shutdown, err := os.ReadFile(shutdownPath)
	if err != nil || string(shutdown) != "old implementation" {
		t.Fatalf("old subprocess did not acknowledge shutdown: %q, %v", shutdown, err)
	}
	if !reflect.DeepEqual(history, historyAfter) || srv.thread(threadID) != th {
		t.Fatal("idle retirement changed conversation identity or in-memory history")
	}
	persistedAfter, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persistedBefore, persistedAfter) {
		t.Fatal("idle retirement rewrote persisted conversation history")
	}
	if len(rt.PluginHost.Statuses()) != 1 || rt.PluginHost.Statuses()[0].State != pluginhost.StateActive {
		t.Fatal("idle retirement closed the newly published process")
	}
}

func TestPluginGrantActivationFailurePublishesCommittedInventory(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = retryingTempDir(t)
	configPath, err := statepath.ConfigPath(rt.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, configPath, `{"default_provider":"fake-provider","providers":{"fake-provider":{"type":"openai-compatible","base_url":"https://example.test/v1","model":"fake-model"}}}`)
	root := t.TempDir()
	manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: "activate-failure", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestPluginReloadBehaviorProcess$"}, Env: map[string]string{"WUU_RELOAD_TEST_MARKER": "failed", "WUU_RELOAD_TEST_DESCRIPTION": "read fixture", "WUU_RELOAD_TEST_ACTIVATE_ERROR": "activation refused", "WUU_RELOAD_TEST_SHUTDOWN": filepath.Join(t.TempDir(), "shutdown")}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writePluginPackageFile(t, filepath.Join(root, "plugin.json"), string(raw))
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	callPluginPackageRPC(t, srv, "install", MethodPluginPackageInstall, PluginPackageInstallParams{Path: root})
	installed := remarshal[PluginPackageInstallResult](t, responseByID(t, parseOutput(t, out.String()), "install")["result"])
	record := pluginPackageRecord(t, installed.ExtensionInventory, "activate-failure")
	callPluginPackageRPC(t, srv, "grant", MethodExtensionPackageUpdate, ExtensionPackageUpdateParams{ID: record.ID, Fingerprint: record.Fingerprint, Action: ExtensionPackageGrant})
	response := responseByID(t, parseOutput(t, out.String()), "grant")
	if !strings.Contains(fmt.Sprint(response["error"]), "committed") {
		t.Fatalf("expected explicit committed activation error: %+v", response)
	}
	current := pluginPackageRecord(t, srv.currentExtensionInventory(), "activate-failure")
	if current.RuntimeState != ExtensionRuntimeFailed {
		t.Fatalf("runtime state = %+v", current)
	}
	cfg, _, err := config.LoadPath(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Extensions.Grants) == 0 || len(rt.ExtensionSettings.Grants) == 0 {
		t.Fatal("committed grants must remain in durable and live settings")
	}
	found := false
	for _, message := range parseOutput(t, out.String()) {
		if message["method"] != NotificationPluginInventoryChanged {
			continue
		}
		notification := remarshal[PluginInventoryChangedNotification](t, message["params"])
		failed := pluginPackageRecord(t, notification.ExtensionInventory, "activate-failure")
		if failed.RuntimeState == ExtensionRuntimeFailed {
			found = true
		}
	}
	if !found {
		t.Fatal("committed failure must notify the UI of the new failed inventory")
	}
}

// Exercise a real plugin-to-host RPC while the app-server owns its refresh
// boundary. The callback must be queued, not time out waiting for that boundary.
func TestPluginRefreshActivationCanQueueSessionSend(t *testing.T) {
	for _, admission := range []bool{false, true} {
		t.Run(fmt.Sprintf("admission=%v", admission), func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{response: providers.ChatResponse{Content: "done"}})
			// Start without a watcher; drive the exact refresh boundary deterministically.
			rt.WuuHome = ""
			rt.PluginSessionRouter = runtime.NewPluginSessionRouter()
			out := &lockedBuffer{}
			srv := New(rt, out)
			defer srv.Close()
			rt.WuuHome = retryingTempDir(t)
			callPluginPackageRPC(t, srv, "thread", MethodThreadStart, ThreadStartParams{})
			threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "thread")["result"]).Thread.ID
			root := t.TempDir()
			resultPath := filepath.Join(root, "callback-result")
			manifest := pluginpkg.Manifest{SchemaVersion: 1, ID: "reload-callback", Runtime: &pluginpkg.RuntimeSpec{Protocol: pluginhost.ProtocolName, Command: os.Args[0], Args: []string{"-test.run=^TestPluginReloadBehaviorProcess$"}, Env: map[string]string{"WUU_RELOAD_TEST_MARKER": "callback", "WUU_RELOAD_TEST_DESCRIPTION": "read fixture", "WUU_RELOAD_TEST_SHUTDOWN": filepath.Join(root, "shutdown"), "WUU_RELOAD_TEST_SESSION": threadID, "WUU_RELOAD_TEST_CALLBACK_RESULT": resultPath}}}
			raw, _ := json.Marshal(manifest)
			path := filepath.Join(root, "plugin.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			item, err := pluginpkg.LoadManifestWithOptions(path, pluginpkg.LoadOptions{Source: "user", Official: true})
			if err != nil {
				t.Fatal(err)
			}
			rt.Plugins = []pluginpkg.Plugin{item}
			srv.refreshExtensionsForTest = func(cfg config.Config) error {
				candidate, err := rt.PreflightExtensionPolicy(cfg)
				if err != nil {
					return err
				}
				err = rt.ActivatePluginGeneration(candidate, nil)
				if runtime.PluginGenerationWasCommitted(err) {
					return nil
				}
				return err
			}
			epoch := advancePluginGenerationWatchTestEpoch(t, rt.WuuHome)
			if admission {
				th, err := srv.ensureThreadLoaded(threadID)
				if err != nil {
					t.Fatal(err)
				}
				th.mu.Lock()
				acquired, err := srv.tryAcquireThreadExecutionLeaseLocked(th)
				if acquired {
					th.releaseThreadExecutionLeaseLocked()
				}
				th.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			} else if err := srv.refreshPluginGenerationIfChanged(); err != nil {
				t.Fatal(err)
			}
			waitPluginGenerationWatchTest(t, func() bool { return srv.pluginGenerationEpoch.Load() == epoch })
			raw, err = os.ReadFile(resultPath)
			if err != nil {
				t.Fatalf("activation callback did not complete: %v; statuses=%+v", err, rt.PluginHost.Statuses())
			}
			var result pluginhost.SessionSendResult
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.State != pluginhost.TurnLifecycleQueued {
				t.Fatalf("activation callback state=%+v", result)
			}
			if statuses := rt.PluginHost.Statuses(); len(statuses) != 1 || statuses[0].State != pluginhost.StateActive {
				t.Fatalf("activation failed: %+v", statuses)
			}
			waitForTurnCompletedForThread(t, out, threadID)
		})
	}
}

// A finalizer can release the execution lease before the background release
// loop observes quiescence. That interleaving must still retire the old host.
func TestPluginReloadRetiresAfterLeaseReleasedBeforePoll(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	publishReloadBehaviorGeneration(t, rt, "old implementation", "Read fixture")
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	shutdownPath := rt.Plugins[0].Runtime.Env["WUU_RELOAD_TEST_SHUTDOWN"]
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	th := newThreadState("released-before-poll", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	srv.mu.Lock()
	srv.threads[th.ID] = th
	srv.mu.Unlock()
	if _, err := srv.ensureThreadRuntime(th); err != nil {
		t.Fatal(err)
	}
	publishReloadBehaviorGeneration(t, rt, "new implementation", "Read fixture")
	lease, acquired, err := session.TryAcquirePluginGenerationExecutionLease(rt.WuuHome)
	if err != nil || !acquired {
		t.Fatalf("execution lease acquired=%v err=%v", acquired, err)
	}
	th.mu.Lock()
	th.pluginExecutionLease = lease
	th.schedulePluginGenerationLeaseReleaseLocked()
	// Hold th.mu across both operations, so the poll can only observe nil.
	th.releasePluginGenerationExecutionLeaseLocked()
	th.mu.Unlock()
	waitPluginGenerationWatchTest(t, func() bool {
		th.mu.Lock()
		retired := th.execRuntime == nil && !th.pluginLeaseReleaseLoop
		th.mu.Unlock()
		raw, err := os.ReadFile(shutdownPath)
		return retired && err == nil && string(raw) == "old implementation"
	})
}
