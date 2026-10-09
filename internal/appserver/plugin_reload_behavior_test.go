package appserver

import (
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
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
	plugingo "github.com/blueberrycongee/wuu/packages/plugin-go"
)

// The fixture uses the public Go extension protocol in a separate process, so
// assertions cover real generation ownership and tool routing rather than a
// synthetic epoch comparison.
func TestPluginReloadBehaviorProcess(t *testing.T) {
	marker := os.Getenv("WUU_RELOAD_TEST_MARKER")
	if marker == "" {
		return
	}
	err := plugingo.Serve(context.Background(), plugingo.Handler{
		Shutdown: func(context.Context) error {
			return os.WriteFile(os.Getenv("WUU_RELOAD_TEST_SHUTDOWN"), []byte(marker), 0600)
		},
		Definition: plugingo.Definition{Tools: []plugingo.Tool{{ID: "read", Description: os.Getenv("WUU_RELOAD_TEST_DESCRIPTION"), InputSchema: map[string]any{"type": "object"}, Activity: &plugingo.ToolActivity{ReadOnly: true, ConcurrencySafe: true, Risk: "low"}}}},
		ExecuteTool: func(context.Context, plugingo.Host, plugingo.ToolCall) (plugingo.ToolResult, error) {
			return plugingo.TextResult(marker), nil
		},
	})
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
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
	item, err := pluginpkg.LoadManifest(path, "user")
	if err != nil {
		t.Fatal(err)
	}
	item.Official = true
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
	// No new turn or runtime acquisition may be needed to retire the process.
	waitPluginGenerationWatchTest(t, func() bool {
		th.mu.Lock()
		retired := th.execRuntime == nil && th.pluginExecutionLease == nil
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
