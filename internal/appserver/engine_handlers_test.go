package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Disabling a selected implementation must reject admission before it creates
// a durable thread or dispatches inference, including the native engine.
func TestUnavailableSelectedEngineRejectsNewThreads(t *testing.T) {
	for _, id := range []string{"wuu", "codex", "claude"} {
		t.Run(id, func(t *testing.T) {
			client := &fakeClient{}
			rt := newTestRuntime(t, client)
			settings := map[string]any{"default_engine": id}
			for _, engine := range []string{"codex", "claude", "cursor", "devin", "dsh", "grok", "hermes", "pi", "opencode", "antigravity"} {
				settings[engine] = map[string]bool{"enabled": false}
			}
			data, err := json.Marshal(map[string]any{
				"default_provider": "fake-provider",
				"providers":        map[string]any{"fake-provider": map[string]string{"type": "openai-compatible", "model": "fake-model", "base_url": "https://example.invalid/v1"}},
				"engines":          settings,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			if id == "wuu" {
				rt.SetEnginesForTest(agentengine.NewRegistry())
			}
			bindConfigSourceForTest(t, rt)
			if err := rt.RefreshEngineSettings(); err != nil {
				t.Fatal(err)
			}
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			for _, engine := range srv.engineInventory() {
				if engine.ID == id && engine.Enabled {
					t.Fatalf("unavailable engine advertised as enabled: %+v", engine)
				}
			}
			if err := srv.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start","params":{}}`)); err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.String()), "start")
			if response["error"] == nil || !strings.Contains(fmt.Sprint(response["error"]), id) {
				t.Fatalf("unavailable selection did not produce its diagnostic: %+v", response)
			}
			threads, err := rt.Sessions.List(100)
			if err != nil || len(threads) != 0 {
				t.Fatalf("rejected selection created threads: %+v, %v", threads, err)
			}
			client.mu.Lock()
			calls := len(client.requests)
			client.mu.Unlock()
			if calls != 0 {
				t.Fatalf("unavailable engine dispatched %d model requests", calls)
			}
		})
	}
}

func TestClaudeBackgroundContinuationPersistsOneCompletedTurn(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fakeclaude")
	build := exec.Command("go", "build", "-o", binary, "../claudeengine/testdata/fakeclaude")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{Claude: &config.EngineBinaryConfig{BinaryPath: binary}}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.handleLine(ctx, []byte(`{"id":"start","method":"thread/start","params":{"engine":"claude"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "start")
	if response["error"] != nil {
		t.Fatalf("thread/start: %+v", response)
	}
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	request, err := json.Marshal(map[string]any{"id": "run", "method": "turn/start", "params": TurnStartParams{ThreadID: threadID, Prompt: "background_parallel"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(ctx, request); err != nil {
		t.Fatal(err)
	}
	messages := waitForTurnCompletedForThread(t, out, threadID)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
	want := "Launched.\n\nFirst summary.\n\nFinal summary."
	if completed.Content != want || completed.Turn.Status != TurnStatusCompleted {
		t.Fatalf("premature or incorrect completion: %+v", completed)
	}
	terminals := 0
	for _, message := range messages {
		if message["method"] == NotificationTurnCompleted {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("completed notifications = %d, want one", terminals)
	}
	history, err := loadChatMessages(rt.Sessions, threadID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		if message.Role == "assistant" && message.Content == want {
			found = true
		}
		if strings.Contains(message.Content, "CHILD private output") {
			t.Fatal("child output leaked into durable parent history")
		}
	}
	if !found {
		t.Fatalf("final summary absent from durable history: %+v", history)
	}
	metas, err := loadMetaMessages(rt.Sessions, threadID)
	if err != nil {
		t.Fatal(err)
	}
	usageRecords := 0
	for _, meta := range metas {
		if meta.Content != "token_usage" {
			continue
		}
		usageRecords++
		if meta.InputTokens != 270 || meta.OutputTokens != 90 || meta.CacheCreationTokens != 30 || meta.CacheReadTokens != 180 {
			t.Fatalf("incomplete durable continuation usage: %+v", meta)
		}
	}
	if usageRecords != 1 {
		t.Fatalf("usage records = %d, want one combined turn record", usageRecords)
	}
}

// The native CLI sends provider diagnostics in an assistant-shaped envelope.
// Verify the real subprocess boundary, live deltas, and durable history together.
func TestClaudeProviderErrorStaysOutOfAssistantHistory(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fakeclaude")
	if output, err := exec.Command("go", "build", "-o", binary, "../claudeengine/testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	for _, scenario := range []string{"provider_error", "provider_error_partial", "provider_error_close"} {
		t.Run(scenario, func(t *testing.T) {
			rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{Claude: &config.EngineBinaryConfig{BinaryPath: binary}}})
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.handleLine(ctx, []byte(`{"id":"start","method":"thread/start","params":{"engine":"claude"}}`)); err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.String()), "start")
			if response["error"] != nil {
				t.Fatalf("thread/start: %+v", response)
			}
			threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
			if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"run","method":"turn/start","params":{"thread_id":%q,"prompt":%q}}`, threadID, scenario))); err != nil {
				t.Fatal(err)
			}
			messages := waitForMethod(t, out, NotificationTurnError)
			failed := remarshal[TurnErrorNotification](t, notificationByMethod(t, messages, NotificationTurnError)["params"])
			if failed.Turn.Status != TurnStatusFailed || failed.Turn.Error == nil || !strings.Contains(failed.Turn.Error.Message, "套餐已到期") {
				t.Fatalf("provider diagnostic lost: %+v", failed)
			}
			var liveText strings.Builder
			failures := 0
			for _, message := range messages {
				switch message["method"] {
				case NotificationAgentMessageDelta:
					liveText.WriteString(remarshal[AgentMessageDeltaNotification](t, message["params"]).Delta)
				case NotificationTurnError:
					failures++
				case NotificationTurnCompleted:
					t.Fatal("failed native request emitted successful completion")
				}
			}
			want := ""
			if scenario == "provider_error_partial" {
				want = "已检查仓库结构。"
			}
			if liveText.String() != want || failures != 1 {
				t.Fatalf("live assistant text = %q, failures = %d", liveText.String(), failures)
			}
			history, err := loadChatMessages(rt.Sessions, threadID)
			if err != nil {
				t.Fatal(err)
			}
			var storedText strings.Builder
			for _, message := range history {
				if message.Role == "assistant" {
					storedText.WriteString(message.Content)
				}
			}
			if storedText.String() != want {
				t.Fatalf("durable assistant text = %q, want %q", storedText.String(), want)
			}
			srv.Close()
			reloadedOutput := &lockedBuffer{}
			reloaded := New(rt, reloadedOutput)
			t.Cleanup(reloaded.Close)
			if err := reloaded.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"resume","method":"thread/resume","params":{"session_id":%q}}`, threadID))); err != nil {
				t.Fatal(err)
			}
			resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, reloadedOutput.String()), "resume")["result"]).Thread
			if len(resumed.Turns) != 1 || resumed.Turns[0].Error == nil || resumed.Turns[0].Error.Message != failed.Turn.Error.Message {
				t.Fatalf("reloaded diagnostic differs: %+v", resumed.Turns)
			}
			for _, turn := range []Turn{failed.Turn, resumed.Turns[0]} {
				var text strings.Builder
				for _, item := range turn.Items {
					if item.Type == "agent_message" {
						text.WriteString(item.Text)
					}
				}
				if text.String() != want {
					t.Fatalf("turn assistant content = %q, want %q", text.String(), want)
				}
			}
			if output := os.Getenv("WUU_CLAUDE_ERROR_E2E_OUTPUT"); output != "" {
				if err := os.MkdirAll(output, 0755); err != nil {
					t.Fatal(err)
				}
				data, err := json.MarshalIndent(map[string]any{"live": failed.Turn, "resumed": resumed.Turns[0], "notifications": messages}, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(output, scenario+".json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Log("provider diagnostic appears only in the turn error; live and resumed assistant text match")
		})
	}
}

// Exercise per-turn ownership through the actual server binding and child
// process, including failure, a second conversation, and native resumption.
func TestCodexPerTurnBindingSubprocessLifecycle(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "fakecodex")
	build := exec.Command("go", "build", "-o", binary, "../codexengine/testdata/fakecodex")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	t.Run("cancellation receipt", func(t *testing.T) { testCodexCancellationReceipt(t, binary) })
	t.Run("warm host ownership", func(t *testing.T) { testCodexWarmHostOwnership(t, binary) })
	t.Run("workspace fidelity", func(t *testing.T) { testCodexWorkspaceFidelity(t, binary) })
	t.Run("concurrent host replacement", func(t *testing.T) { testCodexConcurrentHostReplacement(t, binary) })
	t.Run("concurrent discovery refresh", func(t *testing.T) { testCodexConcurrentDiscoveryRefresh(t, binary) })
	lifecycle := filepath.Join(root, "lifecycle.jsonl")
	requests := filepath.Join(root, "requests.jsonl")
	t.Setenv("WUU_TEST_CODEX_LIFECYCLE", lifecycle)
	t.Setenv("WUU_TEST_CODEX_REQUESTS", requests)
	t.Setenv("WUU_TEST_CODEX_FAIL_TURN_ONCE", filepath.Join(root, "turn-failed"))
	rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{Codex: &config.EngineBinaryConfig{BinaryPath: binary}}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var threads []string
	for i := range 2 {
		id := fmt.Sprintf("thread-%d", i)
		if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":%q,"method":"thread/start","params":{"engine":"codex"}}`, id))); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("thread/start: %+v", response)
		}
		threads = append(threads, remarshal[ThreadStartResult](t, response["result"]).Thread.ID)
	}
	for index, threadID := range []string{threads[0], threads[1], threads[0]} {
		out.Reset()
		request, err := json.Marshal(map[string]any{"id": fmt.Sprintf("turn-%d", index), "method": "turn/start", "params": TurnStartParams{ThreadID: threadID, Prompt: "hello"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.handleLine(ctx, request); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			messages := waitForMethod(t, out, NotificationTurnError)
			failed := remarshal[TurnErrorNotification](t, notificationByMethod(t, messages, NotificationTurnError)["params"])
			if failed.Turn.Status != TurnStatusFailed {
				t.Fatalf("failed turn = %+v", failed)
			}
		} else {
			messages := waitForTurnCompletedForThread(t, out, threadID)
			completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
			if completed.Content != "Hello from codex." {
				t.Fatalf("completed content = %q", completed.Content)
			}
		}
	}
	data, err := os.ReadFile(requests)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request struct {
			Method string
			Params map[string]any
		}
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		counts[request.Method]++
		if request.Method == "thread/resume" && request.Params["threadId"] != "codex-thread-1" {
			t.Fatalf("lost native reference: %s", line)
		}
	}
	if counts["initialize"] > 2 || counts["thread/start"] != 2 || counts["thread/resume"] != 1 || counts["turn/start"] != 3 {
		t.Fatalf("unexpected native lifecycle or replay: %v", counts)
	}
	srv.Close()
	data, err = os.ReadFile(lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), `"method":"exited"`); got != counts["initialize"] {
		t.Fatalf("Server.Close left native subprocesses alive: %s", data)
	}
	t.Logf("two Wuu threads, three turns (failure + success + native resume), native requests=%v; Server.Close released all subprocesses", counts)
}

func testCodexConcurrentDiscoveryRefresh(t *testing.T, binary string) {
	rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{DefaultEngine: "codex", Codex: &config.EngineBinaryConfig{BinaryPath: binary}}})
	kit, err := tools.New(rt.RootDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.Toolkit = kit
	settings := map[string]any{
		"default_engine": "codex",
		"codex":          map[string]any{"binary_path": binary},
	}
	for _, id := range []string{"claude", "cursor", "devin", "dsh", "grok", "hermes", "pi", "opencode", "antigravity"} {
		settings[id] = map[string]any{"enabled": false}
	}
	data, err := json.Marshal(map[string]any{
		"default_provider": "fake-provider",
		"providers": map[string]any{"fake-provider": map[string]any{
			"type": "openai-compatible", "model": "fake-model", "base_url": "https://example.test/v1",
		}},
		"engines": settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	bindConfigSourceForTest(t, rt)
	journal, err := rt.Sessions.NewInferenceJournalRuntime("engine-refresh")
	if err != nil {
		t.Fatal(err)
	}
	rt.InferenceJournalRuntime = journal
	t.Cleanup(func() { _ = journal.Close() })
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	if err := rt.RefreshEngineSettings(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var threadID string
	for i := range 16 {
		// The real dispatcher backgrounds discovery while thread creation stays
		// on the request loop. Every default-selected thread must remain Codex.
		if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"refresh-%d","method":"engine/list","params":{"refresh_models":true}}`, i))); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("thread-%d", i)
		if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":%q,"method":"thread/start","params":{}}`, id))); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("thread/start during refresh: %+v", response)
		}
		thread := remarshal[ThreadStartResult](t, response["result"]).Thread
		if thread.EngineID != "codex" {
			t.Fatalf("refresh changed default-selected engine: %+v", thread)
		}
		threadID = thread.ID
	}
	for i := range 16 {
		if response := waitForResponseByID(t, out, fmt.Sprintf("refresh-%d", i)); response["error"] != nil {
			t.Fatalf("refresh: %+v", response)
		}
	}
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"hello"}}`, threadID))); err != nil {
		t.Fatal(err)
	}
	waitForTurnCompletedForThread(t, out, threadID)
	// Gate the real Claude version probe after a refresh has loaded its config.
	// A newer disable must wait to publish, and unrelated thread creation must
	// keep using the current registry/default while discovery is stalled.
	claude := filepath.Join(t.TempDir(), "fakeclaude")
	if output, err := exec.Command("go", "build", "-o", claude, "../claudeengine/testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build Claude fixture: %v\n%s", err, output)
	}
	settings["default_engine"] = "claude"
	settings["claude"] = map[string]any{"binary_path": claude}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["engines"] = settings
	data, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := rt.RefreshEngineSettings(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	ready, gate := filepath.Join(directory, "ready"), filepath.Join(directory, "release")
	t.Setenv("WUU_TEST_CLAUDE_VERSION_READY", ready)
	t.Setenv("WUU_TEST_CLAUDE_VERSION_GATE", gate)
	release := sync.OnceFunc(func() {
		if err := os.WriteFile(gate, nil, 0600); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { release(); srv.Close() })
	if err := srv.handleLine(ctx, []byte(`{"id":"gated-refresh","method":"engine/list","params":{"refresh_models":true}}`)); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	admission := make(chan error, 1)
	go func() {
		admission <- srv.handleLine(ctx, []byte(`{"id":"gated-thread","method":"thread/start","params":{}}`))
	}()
	select {
	case err := <-admission:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("thread creation waited for an unrelated engine version probe")
	}
	admitted := responseByID(t, parseOutput(t, out.String()), "gated-thread")
	update := make(chan error, 1)
	go func() {
		update <- srv.handleLine(ctx, []byte(`{"id":"disable","method":"engine/update","params":{"default_engine":"wuu","claude":{"enabled":false}}}`))
	}()
	select {
	case err := <-update:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("settings update waited for an unrelated engine version probe")
	}
	release()
	if response := waitForResponseByID(t, out, "gated-refresh"); response["error"] != nil {
		t.Fatalf("gated refresh: %+v", response)
	}
	if response := waitForResponseByID(t, out, "disable"); response["error"] != nil {
		t.Fatalf("disable: %+v", response)
	}
	if admitted["error"] != nil || remarshal[ThreadStartResult](t, admitted["result"]).Thread.EngineID != "claude" {
		t.Errorf("refresh interrupted admission to an enabled engine: %+v", admitted)
	}
	if rt.EngineAvailable("claude") || rt.DefaultEngine() != "wuu" {
		t.Fatal("an older discovery snapshot undid the explicit disable/default edit")
	}
	t.Log("concurrent refresh preserved native turns and thread admission; a gated older snapshot could not undo the newer explicit disable/default edit")
}

func testCodexWarmHostOwnership(t *testing.T, binary string) {
	root := t.TempDir()
	lifecycle := filepath.Join(root, "lifecycle.jsonl")
	requests := filepath.Join(root, "requests.jsonl")
	t.Setenv("WUU_TEST_CODEX_LIFECYCLE", lifecycle)
	t.Setenv("WUU_TEST_CODEX_REQUESTS", requests)
	rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{Codex: &config.EngineBinaryConfig{BinaryPath: binary}}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var servers []*Server
	var outputs []*lockedBuffer
	var threads []string
	for range 2 {
		out := &lockedBuffer{}
		srv := New(rt, out)
		t.Cleanup(srv.Close)
		servers = append(servers, srv)
		outputs = append(outputs, out)
		if err := srv.handleLine(ctx, []byte(`{"id":"thread","method":"thread/start","params":{"engine":"codex"}}`)); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), "thread")
		if response["error"] != nil {
			t.Fatalf("thread/start: %+v", response)
		}
		threads = append(threads, remarshal[ThreadStartResult](t, response["result"]).Thread.ID)
	}
	if _, err := os.Stat(lifecycle); !os.IsNotExist(err) {
		t.Fatalf("retaining an unused host started a subprocess: %v", err)
	}
	run := func(owner int) {
		t.Helper()
		out := outputs[owner]
		out.Reset()
		request, err := json.Marshal(map[string]any{"id": "turn", "method": "turn/start", "params": TurnStartParams{ThreadID: threads[owner], Prompt: "hello"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := servers[owner].handleLine(ctx, request); err != nil {
			t.Fatal(err)
		}
		messages := waitForTurnCompletedForThread(t, out, threads[owner])
		completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
		if completed.Content != "Hello from codex." {
			t.Fatalf("completed content = %q", completed.Content)
		}
	}
	readCounts := func() map[string]int {
		t.Helper()
		data, err := os.ReadFile(requests)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var request struct {
				Method string
				Params map[string]any
			}
			if err := json.Unmarshal([]byte(line), &request); err != nil {
				t.Fatal(err)
			}
			counts[request.Method]++
		}
		return counts
	}
	run(0)
	run(1)
	run(0)
	counts := readCounts()
	if counts["initialize"] != 1 || counts["turn/start"] != 3 || counts["thread/resume"] != 1 {
		t.Fatalf("normal turns did not reuse the warm process/native reference: %v", counts)
	}
	data, err := os.ReadFile(lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"method":"exited"`) {
		t.Fatalf("idle owned host exited: %s", data)
	}
	var first struct{ PID int }
	if err := json.Unmarshal([]byte(strings.Split(string(data), "\n")[0]), &first); err != nil {
		t.Fatal(err)
	}
	t.Logf("warm checkpoint: three normal turns, initialize=%d, thread/resume=%d, native PID=%d", counts["initialize"], counts["thread/resume"], first.PID)
	process, err := os.FindProcess(first.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	// A catalog acquisition may race the EOF notification; it is safe to
	// retry this read without submitting/replaying any turn.
	for {
		var result pluginprotocol.EngineQuotaResult
		if err := rt.CallEngine(ctx, "codex", "quota", struct{}{}, &result); err == nil && result.ErrorCode != "network" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	run(0)
	servers[0].Close()
	run(1)
	counts = readCounts()
	if counts["initialize"] != 2 || counts["turn/start"] != 5 || counts["thread/resume"] != 3 {
		t.Fatalf("crash recovery or one-owner shutdown disturbed peer/native reuse: %v", counts)
	}
	// Settings install a new Host. The next binding transfers this server's
	// lifetime ownership and closes the now-idle previous process.
	if err := rt.CallEngine(context.Background(), "codex", "refresh", struct{}{}, nil); err != nil {
		t.Error(err)
	}
	run(1)
	counts = readCounts()
	if counts["initialize"] != 3 || counts["turn/start"] != 6 || counts["thread/resume"] != 4 {
		t.Fatalf("host replacement did not preserve native identity: %v", counts)
	}
	servers[1].Close()
	data, err = os.ReadFile(lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), `"method":"exited"`); got != 2 {
		t.Fatalf("host switch/final Server.Close did not stop replacements (first process was killed): %s", data)
	}
	t.Logf("three normal turns initialized once; crash recovered once; one Server.Close preserved peer; host switch and final Close stopped replacements: %v", counts)
}

// A settings change may run while the admitted turn acquires its binding. Keep
// the first native startup gated so replacing the host also exercises a live
// old-generation lease, rather than only the idle process path.
func testCodexConcurrentHostReplacement(t *testing.T, binary string) {
	root := t.TempDir()
	lifecycle := filepath.Join(root, "lifecycle.jsonl")
	requests := filepath.Join(root, "requests.jsonl")
	gate := filepath.Join(root, "initialized")
	t.Setenv("WUU_TEST_CODEX_LIFECYCLE", lifecycle)
	t.Setenv("WUU_TEST_CODEX_REQUESTS", requests)
	t.Setenv("WUU_TEST_CODEX_INITIALIZE_GATE", gate)
	rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{Codex: &config.EngineBinaryConfig{BinaryPath: binary}}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0600) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.handleLine(ctx, []byte(`{"id":"thread","method":"thread/start","params":{"engine":"codex"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "thread")
	if response["error"] != nil {
		t.Fatalf("thread/start: %+v", response)
	}
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	submit := func() {
		t.Helper()
		out.Reset()
		request, err := json.Marshal(map[string]any{"id": "turn", "method": "turn/start", "params": TurnStartParams{ThreadID: threadID, Prompt: "hello"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.handleLine(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	complete := func() {
		t.Helper()
		messages := waitForTurnCompletedForThread(t, out, threadID)
		completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
		if completed.Content != "Hello from codex." {
			t.Fatalf("completed content = %q", completed.Content)
		}
	}
	count := func(path, method string) int {
		t.Helper()
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), `"method":"`+method+`"`)
	}
	settingsStop, settingsDone := make(chan struct{}), make(chan struct{})
	stopSettings := sync.OnceFunc(func() { close(settingsStop); <-settingsDone })
	defer stopSettings()
	go func() {
		defer close(settingsDone)
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-settingsStop:
				return
			case <-tick.C:
				if err := rt.CallEngine(context.Background(), "codex", "refresh", struct{}{}, nil); err != nil {
					t.Error(err)
				}
			}
		}
	}()
	submit()
	for count(lifecycle, "initialize") == 0 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	stopSettings()
	// Force the final replacement after the native child has started, while
	// its acquisition still owns the pending initialization.
	if err := rt.CallEngine(context.Background(), "codex", "refresh", struct{}{}, nil); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	complete()
	for range 3 {
		submit()
		complete()
	}
	if starts, turns, resumes := count(requests, "initialize"), count(requests, "turn/start"), count(requests, "thread/resume"); starts != 2 || turns != 4 || resumes != 3 {
		t.Fatalf("replacement lost a running turn or warm/native reuse: initialize=%d, turn/start=%d, thread/resume=%d", starts, turns, resumes)
	}
	srv.Close()
	if starts, exits := count(lifecycle, "started"), count(lifecycle, "exited"); starts != 2 || exits != 2 {
		t.Fatalf("host replacement left subprocesses alive: started=%d, exited=%d", starts, exits)
	}
	t.Log("concurrent settings preserved the admitted turn; three subsequent turns reused one replacement; both native processes exited")
}

// Failure cases: selected-state I/O holding the stream projection lock, losing
// native items during that stall, settings prepending or renumbering the admitted
// user row, and clearing admission before durable settlement.
func TestCodexLongTurnProgressesDuringBoundaryPersistence(t *testing.T) {
	binary := os.Getenv("WUU_E2E_CODEX")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "fakecodex")
		build := exec.Command("go", "build", "-o", binary, "../codexengine/testdata/fakecodex")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v\n%s", err, output)
		}
	}
	for _, scenario := range []struct {
		name                       string
		reset, noPrompt, priorTurn bool
	}{
		{name: "boundary_io"},
		{name: "settings_prepend", reset: true, noPrompt: true},
		{name: "settings_after_prior_turn", reset: true, priorTurn: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("WUU_TEST_CODEX_HISTORY", filepath.Join(root, "native.json"))
			control := filepath.Join(root, "control.json")
			t.Setenv("WUU_TEST_CODEX_HISTORY_CONTROL", control)
			blocked := &nativeBoundaryDocuments{entered: make(chan struct{}), release: make(chan struct{}), progress: make(chan struct{})}
			storeDir := filepath.Join(t.TempDir(), "documents")
			var selectedDocuments *nativeBoundaryProvider
			factory := runtime.NativeProviderFactory{ID: "fixture.boundary-documents", DefaultEnabled: true, Build: func(runtime.NativeProviderContext) (pluginhost.Client, error) {
				selectedDocuments = &nativeBoundaryProvider{Service: persistence.NewService("fixture.boundary-documents", storeDir, "sqlite"), gate: blocked}
				return selectedDocuments, nil
			}}
			rt := newEngineAdminRuntimeAt(t, t.TempDir(), config.Config{Engines: &config.EnginesConfig{Codex: &config.EngineBinaryConfig{BinaryPath: binary}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{persistence.SQLiteModule: false, factory.ID: true}}}}}, factory)
			// Observe the selected fixture's committed snapshot without waiting for
			// the intentionally stalled authority writer. Runtime operations still
			// use the selected authority; the observer cannot commit mutations.
			observer := session.NewStore(context.Background(), persistence.NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
				if method != "read" {
					return nil, fmt.Errorf("fixture observer cannot %s", method)
				}
				return selectedDocuments.Service.InvokeService(ctx, pluginhost.ServiceInvokeParams{Service: "state.documents", Method: method, Params: params})
			}))
			if scenario.noPrompt {
				rt.StreamRunner.SystemPrompt = ""
			}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(blocked.release) }) }
			defer release()
			out := &nativeBurstOutput{documents: blocked, terminal: make(chan Turn, 1)}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := srv.handleLine(ctx, []byte(`{"id":"native-burst-thread","method":"thread/start","params":{"engine":"codex"}}`)); err != nil {
				t.Fatal(err)
			}
			response := responseByID(t, parseOutput(t, out.buffer.String()), "native-burst-thread")
			if response["error"] != nil {
				t.Fatalf("thread/start: %+v", response)
			}
			threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
			startTurn := func(prompt string) {
				t.Helper()
				request, _ := json.Marshal(map[string]any{"id": prompt, "method": "turn/start", "params": TurnStartParams{ThreadID: threadID, Prompt: prompt}})
				if err := srv.handleLine(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.priorTurn {
				startTurn("first prompt")
				select {
				case turn := <-out.terminal:
					if turn.Status != TurnStatusCompleted {
						t.Fatalf("prior native turn: %+v", turn)
					}
				case <-ctx.Done():
					t.Fatal("prior native turn did not settle")
				}
			}
			streamGate := filepath.Join(root, "stream-ready")
			controlData, err := json.Marshal(map[string]string{"mode": "long-turn", "gate": streamGate})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(control, controlData, 0600); err != nil {
				t.Fatal(err)
			}
			blocked.armed.Store(true)
			startTurn("many-large-tool-results")
			select {
			case <-blocked.entered:
			case <-ctx.Done():
				t.Fatal("native boundary persistence was not reached")
			}
			// The burst must begin while the selected commit is blocked. Otherwise
			// fast native output can finish before the boundary writer is scheduled,
			// leaving this progress assertion waiting for output that already arrived.
			if err := os.WriteFile(streamGate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			stalled := false
			started := time.Now()
			select {
			case <-blocked.progress:
			case <-time.After(2 * time.Second):
				stalled = true
				release()
			}
			th := srv.thread(threadID)
			th.mu.Lock()
			admittedSeq := 0
			for _, msg := range th.History {
				if msg.Role == "user" && !msg.Steered {
					admittedSeq = msg.Seq
				}
			}
			if scenario.noPrompt && th.History[0].Role != "user" {
				t.Error("fixture did not reach native admission without a base system prompt")
			}
			th.mu.Unlock()
			if scenario.reset {
				before, err := observer.LoadHistoryRecords(threadID, true)
				if err != nil {
					t.Fatal(err)
				}
				srv.resetThreadRuntimesForGeneralSettings("updated runtime system prompt")
				after, err := observer.LoadHistoryRecords(threadID, true)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) {
					t.Error("runtime settings changed durable history while native admission was pending")
				}
			}
			boundaryWait := time.Since(started)
			release()
			var terminal Turn
			select {
			case terminal = <-out.terminal:
			case <-ctx.Done():
				t.Fatal("native turn did not settle")
			}
			tools := 0
			for _, item := range terminal.Items {
				if item.Type == ThreadItemToolCall {
					tools++
				}
			}
			t.Logf("boundary_wait=%s through_terminal=%s stream_stalled=%v terminal=%s visible_tools=%d", boundaryWait, time.Since(started), stalled, terminal.Status, tools)
			if stalled {
				t.Error("selected Documents persistence blocked stream projection")
			}
			if terminal.Status != TurnStatusCompleted || tools != 302 {
				t.Errorf("long native turn lost output: status=%s tools=%d want completed/302", terminal.Status, tools)
			}
			history, err := rt.Sessions.LoadHistoryRecords(threadID, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range history {
				if record.Role != "user" {
					continue
				}
				if record.EngineTurnID == "" || record.EngineTurnPending {
					t.Errorf("native completion not durably settled: seq=%d native=%q pending=%v", record.Seq, record.EngineTurnID, record.EngineTurnPending)
				}
				if record.Content == "many-large-tool-results" && record.Seq != admittedSeq {
					t.Errorf("settings changed durable prompt identity: seq=%d want %d", record.Seq, admittedSeq)
				}
			}
			th.mu.Lock()
			defer th.mu.Unlock()
			for _, msg := range th.History {
				if msg.Role == "system" && (msg.EngineTurnID != "" || msg.EngineTurnPending) {
					t.Errorf("native user boundary moved to runtime system prompt: native=%q pending=%v", msg.EngineTurnID, msg.EngineTurnPending)
				}
			}
		})
	}
}

type nativeBoundaryDocuments struct {
	entered, release, progress chan struct{}
	once, progressOnce         sync.Once
	armed, active              atomic.Bool
}

type nativeBoundaryProvider struct {
	*persistence.Service
	gate *nativeBoundaryDocuments
}

func (p *nativeBoundaryProvider) InvokeService(ctx context.Context, in pluginhost.ServiceInvokeParams) (json.RawMessage, error) {
	if in.Method == "commit" {
		var tx persistence.Transaction
		if err := json.Unmarshal(in.Params, &tx); err != nil {
			return nil, err
		}
		for _, write := range tx.Writes {
			if write.Namespace != "wuu.session_messages.v1" {
				continue
			}
			var record session.HistoryRecord
			if err := json.Unmarshal(write.Value, &record); err != nil {
				return nil, err
			}
			if p.gate.armed.Load() && record.EngineTurnID != "" && record.EngineTurnPending {
				p.gate.once.Do(func() { p.gate.active.Store(true); close(p.gate.entered); <-p.gate.release; p.gate.active.Store(false) })
			}
		}
	}
	return p.Service.InvokeService(ctx, in)
}

type nativeBurstOutput struct {
	buffer    lockedBuffer
	documents *nativeBoundaryDocuments
	terminal  chan Turn
}

func (w *nativeBurstOutput) Write(data []byte) (int, error) {
	var envelope struct {
		Method string
		Params json.RawMessage
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0, err
	}
	if w.documents.active.Load() && bytes.Contains(data, []byte(`"type":"tool_call"`)) {
		w.documents.progressOnce.Do(func() { close(w.documents.progress) })
	}
	if envelope.Method == NotificationTurnCompleted || envelope.Method == NotificationTurnError {
		var params struct{ Turn Turn }
		if err := json.Unmarshal(envelope.Params, &params); err != nil {
			return 0, err
		}
		w.terminal <- params.Turn
	}
	return w.buffer.Write(data)
}

// Admin contract tests use the production selected graph with fixture binaries;
// no model turns or real account requests are admitted by this helper.
func newEngineAdminRuntime(t *testing.T, cfg config.Config) *runtime.Session {
	t.Helper()
	return newEngineAdminRuntimeAt(t, t.TempDir(), cfg)
}

func newEngineAdminRuntimeAt(t *testing.T, root string, cfg config.Config, factories ...runtime.NativeProviderFactory) *runtime.Session {
	t.Helper()
	// Native engine turns can trigger background titles. Keep that entire model
	// path on a deterministic loopback fixture, never an external endpoint.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Fixture title\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	if len(cfg.Providers) == 0 {
		cfg.DefaultProvider = "fixture"
		cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", APIKey: "fixture", Model: "fixture"}}
	}
	for name, provider := range cfg.Providers {
		provider.BaseURL = upstream.URL + "/v1"
		cfg.Providers[name] = provider
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(root, ".wuu.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.NewSession(runtime.Options{RootDir: root, WuuHome: t.TempDir(), HomeDir: home, Config: cfg, ConfigPath: path, ConfigLoadMode: runtime.ConfigLoadFile, NonInteractive: true, NativeProviders: factories})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := rt.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	return rt
}

// Cancellation must settle the already-admitted native identity through the
// captured provider's cleanup channel, never leave a replayable pending turn.
func testCodexCancellationReceipt(t *testing.T, binary string) {
	rt := newEngineAdminRuntime(t, config.Config{Engines: &config.EnginesConfig{Codex: &config.EngineBinaryConfig{BinaryPath: binary}}})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.handleLine(ctx, []byte(`{"id":"thread","method":"thread/start","params":{"engine":"codex"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "thread")
	if response["error"] != nil {
		t.Fatalf("thread: %+v", response)
	}
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"turn","method":"turn/start","params":{"thread_id":%q,"prompt":"e2e:pending"}}`, threadID))); err != nil {
		t.Fatal(err)
	}
	waitForMethod(t, out, NotificationAgentMessageDelta)
	if err := srv.handleLine(ctx, []byte(fmt.Sprintf(`{"id":"interrupt","method":"turn/interrupt","params":{"thread_id":%q}}`, threadID))); err != nil {
		t.Fatal(err)
	}
	messages := waitForMethod(t, out, NotificationTurnError)
	terminal := remarshal[TurnErrorNotification](t, notificationByMethod(t, messages, NotificationTurnError)["params"])
	if terminal.Turn.Status != TurnStatusInterrupted {
		t.Fatalf("cancellation terminal: %+v", terminal)
	}
	history, err := rt.Sessions.LoadHistoryRecords(threadID, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range history {
		if record.Role == "user" {
			found = true
			if record.EngineTurnID == "" || record.EngineTurnPending {
				t.Fatalf("canceled native input not settled: %+v", record)
			}
		}
	}
	if !found {
		t.Fatal("native input was not durably admitted")
	}
	t.Log("interrupted native process turn durably cleared pending identity via captured cleanup gateway")
}
