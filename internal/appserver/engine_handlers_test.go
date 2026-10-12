package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
)

func TestClaudeBackgroundContinuationPersistsOneCompletedTurn(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fakeclaude")
	build := exec.Command("go", "build", "-o", binary, "../claudeengine/testdata/fakeclaude")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	rt := newTestRuntime(t, &fakeClient{})
	registry := agentengine.NewRegistry()
	if err := registry.Register(claudeengine.NewEngine(binary, rt.RootDir)); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(registry)
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
	history, err := loadChatMessages(rt.SessionDir, threadID)
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
	metas, err := loadMetaMessages(rt.SessionDir, threadID)
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

func TestCodexEngineModelCatalogCacheUsesFreshMatchingBinary(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	entry := &engineModelCatalogCacheEntry{
		binaryPath: "/usr/local/bin/codex",
		models: []EngineModelInfo{{
			ID:               "gpt-test",
			SupportedEfforts: []string{"low", "high"},
		}},
		expiresAt: now.Add(engineModelCatalogTTL),
	}

	models, ok := entry.load("/usr/local/bin/codex", now.Add(time.Minute))
	if !ok || len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("fresh matching cache = (%+v, %v), want cached model", models, ok)
	}
	models[0].SupportedEfforts[0] = "changed"
	if entry.models[0].SupportedEfforts[0] != "low" {
		t.Fatal("cache returned mutable model effort storage")
	}

	if _, ok := entry.load("/opt/codex", now.Add(time.Minute)); ok {
		t.Fatal("cache matched a different binary path")
	}
	if _, ok := entry.load("/usr/local/bin/codex", entry.expiresAt); ok {
		t.Fatal("cache remained fresh at its expiration boundary")
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
	t.Run("warm host ownership", func(t *testing.T) { testCodexWarmHostOwnership(t, binary) })
	t.Run("workspace fidelity", func(t *testing.T) { testCodexWorkspaceFidelity(t, binary) })
	t.Run("concurrent host replacement", func(t *testing.T) { testCodexConcurrentHostReplacement(t, binary) })
	lifecycle := filepath.Join(root, "lifecycle.jsonl")
	requests := filepath.Join(root, "requests.jsonl")
	t.Setenv("WUU_TEST_CODEX_LIFECYCLE", lifecycle)
	t.Setenv("WUU_TEST_CODEX_REQUESTS", requests)
	t.Setenv("WUU_TEST_CODEX_FAIL_TURN_ONCE", filepath.Join(root, "turn-failed"))
	rt := newTestRuntime(t, &fakeClient{})
	registry := agentengine.NewRegistry()
	rt.SetEnginesForTest(registry)
	rt.RebuildCodexEngine(true, binary)
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

func testCodexWarmHostOwnership(t *testing.T, binary string) {
	root := t.TempDir()
	lifecycle := filepath.Join(root, "lifecycle.jsonl")
	requests := filepath.Join(root, "requests.jsonl")
	t.Setenv("WUU_TEST_CODEX_LIFECYCLE", lifecycle)
	t.Setenv("WUU_TEST_CODEX_REQUESTS", requests)
	rt := newTestRuntime(t, &fakeClient{})
	rt.SetEnginesForTest(agentengine.NewRegistry())
	rt.RebuildCodexEngine(true, binary)
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
		if _, err := rt.CodexHost().ListModels(ctx); err == nil {
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
	rt.RebuildCodexEngine(true, binary)
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
	rt := newTestRuntime(t, &fakeClient{})
	rt.SetEnginesForTest(agentengine.NewRegistry())
	rt.RebuildCodexEngine(true, binary)
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
				rt.RebuildCodexEngine(true, binary)
				srv.retainCodexHost()
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
	rt.RebuildCodexEngine(true, binary)
	srv.retainCodexHost()
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
