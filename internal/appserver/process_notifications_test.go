package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

func TestProcessCompletionChatMessageIncludesOutputTail(t *testing.T) {
	root := t.TempDir()
	manager, err := process.NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan process.Event, 8)
	manager.Subscribe(events)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "printf 'hello-tail\\n'",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   "thread-1",
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}

	var terminal process.Event
	deadline := time.After(5 * time.Second)
	for terminal.Process.ID == "" {
		select {
		case event := <-events:
			if event.Process.ID == started.ID && event.Cause == process.EventCauseNaturalExit {
				terminal = event
			}
		case <-deadline:
			t.Fatal("timed out waiting for natural process exit")
		}
	}
	logOutput := strings.Repeat("x", processCompletionOutputBytes+512) + "hello-tail\n"
	if err := os.WriteFile(started.LogPath, []byte(logOutput), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := processCompletionChatMessage(manager, terminal)
	if msg.Role != "user" || msg.Name != wuucontext.ProcessNotificationMessageName {
		t.Fatalf("unexpected process completion message metadata: %+v", msg)
	}
	if !strings.HasPrefix(msg.Content, "<process_notification>\n") || !strings.HasSuffix(msg.Content, "\n</process_notification>") {
		t.Fatalf("completion lost its envelope tags: %q", msg.Content)
	}
	for _, want := range []string{
		"Background process " + started.ID + " exited with code 0: printf 'hello-tail\\n'",
		"hello-tail\n[last 2048 of " + strconv.Itoa(len(logOutput)) + " output bytes; read earlier output with process action=read process_id=" + started.ID + " offset_bytes=0]",
		"without polling",
	} {
		if !strings.Contains(strings.ToLower(msg.Content), strings.ToLower(want)) {
			t.Fatalf("completion notification missing %q:\n%s", want, msg.Content)
		}
	}
	if strings.Contains(msg.Content, `"process_id"`) {
		t.Fatalf("completion notification should be plain text: %s", msg.Content)
	}
}

func TestForwardProcessNotificationsQueuesOnlyNaturalOwnerExit(t *testing.T) {
	threadID := "thread-owner"
	thread := &threadState{ID: threadID, running: true}
	server := &Server{
		threads:                      map[string]*threadState{threadID: thread},
		pendingAgentCompletionTurns:  make(map[string][]agentCompletionTurn),
		drainingAgentCompletionTurns: make(map[string]bool),
	}
	events := make(chan process.Event, 4)
	done := make(chan struct{})
	forwardDone := make(chan struct{})
	go func() {
		server.forwardProcessNotifications(threadID, nil, nil, events, done)
		close(forwardDone)
	}()

	events <- process.Event{
		Type:  process.EventStopped,
		Cause: process.EventCauseRequestedStop,
		Process: process.Process{
			ID: "proc-requested", OwnerKind: process.OwnerMainAgent, OwnerID: threadID, Status: process.StatusStopped,
		},
	}
	events <- process.Event{
		Type:  process.EventStopped,
		Cause: process.EventCauseNaturalExit,
		Process: process.Process{
			ID: "proc-other", OwnerKind: process.OwnerMainAgent, OwnerID: "other-thread", Status: process.StatusStopped,
		},
	}
	events <- process.Event{
		Type:  process.EventStopped,
		Cause: process.EventCauseNaturalExit,
		Process: process.Process{
			ID: "proc-owned", OwnerKind: process.OwnerMainAgent, OwnerID: threadID, Status: process.StatusStopped,
		},
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		server.agentCompletionMu.Lock()
		pending := cloneAgentCompletionTurns(server.pendingAgentCompletionTurns[threadID])
		server.agentCompletionMu.Unlock()
		if len(pending) == 1 {
			if pending[0].processID != "proc-owned" || pending[0].msg.Name != wuucontext.ProcessNotificationMessageName {
				t.Fatalf("unexpected queued completion: %+v", pending[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("natural owner exit was not queued; pending=%+v", pending)
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(done)
	<-forwardDone
	server.backgroundWG.Wait()
}

func TestForwardProcessNotificationsPullsPersistedCompletionAfterMissedEvent(t *testing.T) {
	root := t.TempDir()
	manager, err := process.NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-replay"
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "exit 0",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   threadID,
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pending, pendingErr := manager.CompletionPending(started.ID)
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process did not create a persisted completion obligation")
		}
		time.Sleep(10 * time.Millisecond)
	}

	thread := &threadState{ID: threadID, running: true}
	server := &Server{
		threads:                      map[string]*threadState{threadID: thread},
		pendingAgentCompletionTurns:  make(map[string][]agentCompletionTurn),
		drainingAgentCompletionTurns: make(map[string]bool),
	}
	events := make(chan process.Event, 1)
	done := make(chan struct{})
	forwardDone := make(chan struct{})
	go func() {
		server.forwardProcessNotifications(threadID, nil, manager, events, done)
		close(forwardDone)
	}()
	// A full channel can drop the terminal event itself, but at least one
	// retained event remains as a wake hint. Every hint pulls durable pending
	// completions instead of trusting its event payload.
	events <- process.Event{Type: process.EventStarted, Process: process.Process{ID: "retained-hint"}}

	deadline = time.Now().Add(2 * time.Second)
	for {
		server.agentCompletionMu.Lock()
		pending := cloneAgentCompletionTurns(server.pendingAgentCompletionTurns[threadID])
		server.agentCompletionMu.Unlock()
		if len(pending) == 1 && pending[0].processID == started.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("persisted completion was not pulled after event loss: %+v", pending)
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(done)
	<-forwardDone
	server.backgroundWG.Wait()
}

func TestThreadHasOutstandingProcessCompletionIgnoresDetachedProcesses(t *testing.T) {
	root := t.TempDir()
	manager, err := process.NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-outstanding"
	resumed, err := manager.Start(context.Background(), process.StartOptions{
		Command:        "sleep 30",
		OwnerKind:      process.OwnerMainAgent,
		OwnerID:        threadID,
		Lifecycle:      process.LifecycleManaged,
		CompletionMode: process.CompletionModeResume,
	})
	if err != nil {
		t.Fatal(err)
	}
	detached, err := manager.Start(context.Background(), process.StartOptions{
		Command:        "sleep 30",
		OwnerKind:      process.OwnerMainAgent,
		OwnerID:        "thread-detached",
		Lifecycle:      process.LifecycleManaged,
		CompletionMode: process.CompletionModeDetached,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = manager.Stop(resumed.ID)
		_, _ = manager.Stop(detached.ID)
	})

	if !threadHasOutstandingProcessCompletion(threadID, nil, manager) {
		t.Fatal("resume process should keep automatic continuation outstanding")
	}
	if threadHasOutstandingProcessCompletion("thread-detached", nil, manager) {
		t.Fatal("detached process should not keep automatic continuation outstanding")
	}
}

func TestProcessCompletionHistoryMarkerPreventsDuplicateModelTurn(t *testing.T) {
	processID := "proc-history"
	result := &agent.LoopResult{NewMessages: []providers.ChatMessage{
		{Role: "user", ClientID: processCompletionClientID([]string{processID}), Content: "done"},
		{Role: "assistant", Content: "continued"},
	}}
	if !markProcessCompletionAnswer(result, []string{processID}) {
		t.Fatal("assistant response was not marked as the completion answer")
	}
	if !processCompletionMarkerAnswered(result.NewMessages, processID) {
		t.Fatalf("persisted history did not recognize answered completion: %+v", result.NewMessages)
	}
	if processCompletionMarkerAnswered(result.NewMessages, "proc-other") {
		t.Fatal("history marker matched an unrelated process")
	}
}

func TestQueuedProcessCompletionAlreadyAnsweredSkipsProvider(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "should not run"}}
	rt := newTestRuntime(t, mainClient)
	manager, err := process.NewManager(rt.RootDir, filepath.Join(rt.RootDir, "process-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-queued-process-already-answered"
	threadRuntime := &runtime.ThreadRuntime{StreamRunner: rt.StreamRunner, ProcessManager: manager}
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "printf 'done\\n'",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   threadID,
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := manager.CompletionPending(started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	history := []providers.ChatMessage{
		{Role: "user", Content: "run and continue"},
		{Role: "user", ClientID: processCompletionClientID([]string{started.ID}), Content: "process done"},
		{Role: "assistant", ClientID: processCompletionAnswerClientIDPrefix + started.ID, Content: "already handled"},
	}
	rootThread := newThreadState(threadID, history, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = threadRuntime
	server.mu.Lock()
	server.threads[threadID] = rootThread
	server.mu.Unlock()
	rootThread.runtimeSubscription = server.subscribeThreadRuntime(threadID, threadRuntime)
	t.Cleanup(func() { releaseThreadRuntime(rootThread) })

	queuedID := processCompletionClientID([]string{started.ID})
	startedTurn, err := server.startQueuedTurn(context.Background(), threadID, queuedTurn{
		id:       queuedID,
		msg:      providers.ChatMessage{Role: "user", ClientID: queuedID, Content: "process done"},
		snapshot: turnRuntimeSnapshot{ProcessCompletionIDs: []string{started.ID}},
	})
	if err != nil {
		t.Fatalf("startQueuedTurn: %v", err)
	}
	if !startedTurn {
		t.Fatal("already answered queued completion should be settled")
	}
	assertFakeClientRequestCount(t, mainClient, 0)
	pending, err := manager.CompletionPending(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("answered process completion was not marked delivered")
	}
}

func TestProcessCompletionDrainYieldsToQueuedUserWork(t *testing.T) {
	threadID := "thread-user-priority"
	server := &Server{
		threads: map[string]*threadState{
			threadID: {ID: threadID},
		},
		pendingAgentCompletionTurns: map[string][]agentCompletionTurn{
			threadID: {{processID: "proc-later", msg: providers.ChatMessage{Role: "user", Content: "completed"}}},
		},
		drainingAgentCompletionTurns: map[string]bool{threadID: true},
		pendingQueuedTurns: map[string][]queuedTurn{
			threadID: {{id: "user-first", msg: providers.ChatMessage{Role: "user", Content: "new instruction"}}},
		},
		drainingQueuedTurns: map[string]bool{threadID: true},
	}

	server.drainAgentCompletionTurns(threadID)

	server.agentCompletionMu.Lock()
	pending := cloneAgentCompletionTurns(server.pendingAgentCompletionTurns[threadID])
	draining := server.drainingAgentCompletionTurns[threadID]
	server.agentCompletionMu.Unlock()
	if len(pending) != 1 || pending[0].processID != "proc-later" {
		t.Fatalf("automatic completion should remain queued behind user work: %+v", pending)
	}
	if draining {
		t.Fatal("automatic completion drain should release ownership to queued user work")
	}
}

func TestServerAutoResumesAndAcknowledgesNaturalProcessCompletion(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "continued after process"}}
	rt := newTestRuntime(t, mainClient)
	manager, err := process.NewManager(rt.RootDir, filepath.Join(rt.RootDir, "process-runtime"))
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-process-auto-resume"
	threadRuntime := &runtime.ThreadRuntime{
		StreamRunner:   rt.StreamRunner,
		ProcessManager: manager,
	}
	out := &lockedBuffer{}
	server := New(rt, out)
	rootThread := newThreadState(threadID, []providers.ChatMessage{
		{Role: "user", Content: "run the build and continue when it finishes"},
	}, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	rootThread.execRuntime = threadRuntime
	server.mu.Lock()
	server.threads[threadID] = rootThread
	server.mu.Unlock()
	rootThread.runtimeSubscription = server.subscribeThreadRuntime(threadID, threadRuntime)
	t.Cleanup(func() { releaseThreadRuntime(rootThread) })

	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "printf 'build complete\\n'",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   threadID,
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTurnCompletedForThread(t, out, threadID)

	mainClient.mu.Lock()
	requests := append([]providers.ChatRequest(nil), mainClient.requests...)
	mainClient.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("natural process exit should trigger one model turn, got %d", len(requests))
	}
	foundNotification := false
	for _, msg := range requests[0].Messages {
		if msg.Name == wuucontext.ProcessNotificationMessageName &&
			strings.Contains(msg.Content, started.ID) && strings.Contains(msg.Content, "build complete") {
			foundNotification = true
			break
		}
	}
	if !foundNotification {
		t.Fatalf("model request missing process completion and output: %+v", requests[0].Messages)
	}
	pending, err := manager.CompletionPending(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("persisted completion remained pending after the assistant answer was saved")
	}
	processes, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, stored := range processes {
		if stored.ID == started.ID {
			if stored.CompletionConsumedBy != "auto_completion" || stored.CompletionDeliveredAt.IsZero() {
				t.Fatalf("completion acknowledgement was not persisted: %+v", stored)
			}
			return
		}
	}
	t.Fatalf("process %q disappeared from the registry", started.ID)
}

func TestThreadResumeReplaysProcessCompletionAcrossManagerRestart(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "continued after restart"}}
	rt := newTestRuntime(t, mainClient)
	threadID := "thread-process-restart"
	if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := rewriteChatHistory(rt.SessionDir, threadID, []providers.ChatMessage{
		{Role: "user", Content: "continue after the background build"},
		{Role: "assistant", Content: "I will continue when it finishes."},
	}); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(rt.RootDir, "process-runtime")
	manager, err := process.NewManager(rt.RootDir, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan process.Event, 8)
	manager.Subscribe(events)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "printf 'restart build complete\\n'",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   threadID,
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Process.ID == started.ID && event.Cause == process.EventCauseNaturalExit {
				goto terminal
			}
		case <-deadline:
			t.Fatal("timed out waiting for process completion before restart")
		}
	}

terminal:
	restarted, err := process.NewManager(rt.RootDir, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.ProcessManager = restarted
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	request := map[string]any{
		"id":     "resume",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: threadID},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}
	waitForTurnCompletedForThread(t, out, threadID)

	mainClient.mu.Lock()
	requests := append([]providers.ChatRequest(nil), mainClient.requests...)
	mainClient.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("restart replay should trigger one model turn, got %d", len(requests))
	}
	foundNotification := false
	for _, msg := range requests[0].Messages {
		if msg.Name == wuucontext.ProcessNotificationMessageName && strings.Contains(msg.Content, started.ID) {
			foundNotification = true
			break
		}
	}
	if !foundNotification {
		t.Fatalf("restart replay omitted process completion: %+v", requests[0].Messages)
	}
	pending, err := restarted.CompletionPending(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("completion obligation remained pending after restart replay")
	}
}

func TestThreadResumeAcknowledgesPersistedAnswerWithoutDuplicateModelTurn(t *testing.T) {
	mainClient := &fakeClient{response: providers.ChatResponse{Content: "should not run"}}
	rt := newTestRuntime(t, mainClient)
	threadID := "thread-process-answer-crash"
	if _, err := session.CreateWithMetadata(rt.SessionDir, threadID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(rt.RootDir, "process-runtime")
	manager, err := process.NewManager(rt.RootDir, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan process.Event, 8)
	manager.Subscribe(events)
	started, err := manager.Start(context.Background(), process.StartOptions{
		Command:   "exit 0",
		OwnerKind: process.OwnerMainAgent,
		OwnerID:   threadID,
		Lifecycle: process.LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Process.ID == started.ID && event.Cause == process.EventCauseNaturalExit {
				goto terminal
			}
		case <-deadline:
			t.Fatal("timed out waiting for process completion")
		}
	}

terminal:
	if err := rewriteChatHistory(rt.SessionDir, threadID, []providers.ChatMessage{
		{Role: "user", ClientID: processCompletionClientID([]string{started.ID}), Content: "<process_notification>done</process_notification>"},
		{Role: "assistant", ClientID: processCompletionAnswerClientIDPrefix + started.ID, Content: "already continued"},
	}); err != nil {
		t.Fatal(err)
	}
	restarted, err := process.NewManager(rt.RootDir, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	rt.ProcessManager = restarted
	out := &lockedBuffer{}
	server := New(rt, out)
	t.Cleanup(server.Close)
	request := map[string]any{
		"id":     "resume",
		"method": MethodThreadResume,
		"params": ThreadResumeParams{SessionID: threadID},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.handleLine(context.Background(), raw); err != nil {
		t.Fatalf("thread/resume: %v", err)
	}

	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		pending, pendingErr := restarted.CompletionPending(started.ID)
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		if !pending {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("persisted assistant answer did not settle the completion obligation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mainClient.mu.Lock()
	requestCount := len(mainClient.requests)
	mainClient.mu.Unlock()
	if requestCount != 0 {
		t.Fatalf("persisted answer should prevent duplicate model work, got %d requests", requestCount)
	}
}
