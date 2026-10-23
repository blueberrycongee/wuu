package agentcontrol

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type terminalFollowupTransitionClient struct {
	mu            sync.Mutex
	calls         int
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

func (c *terminalFollowupTransitionClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "done"}, nil
}

func (c *terminalFollowupTransitionClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	ch := make(chan providers.StreamEvent, 2)
	if call == 1 {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "first done"}
		ch <- providers.StreamEvent{Type: providers.EventDone}
		close(ch)
		return ch, nil
	}
	go func() {
		if call == 2 {
			close(c.secondStarted)
		}
		select {
		case <-c.releaseSecond:
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "followup done"}
			ch <- providers.StreamEvent{Type: providers.EventDone}
		case <-ctx.Done():
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}
		close(ch)
	}()
	return ch, nil
}

func TestConcurrentFollowupWaitsForTerminalTransition(t *testing.T) {
	dir := t.TempDir()
	client := &terminalFollowupTransitionClient{
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
	control, err := New(Config{
		AcquireLoopDriver: loopdriver.AcquireLocal(loopdriver.DefaultDriver{}),

		Client:       client,
		DefaultModel: "fake-model",
		ParentRepo:   dir,
		WorktreeRoot: filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:    "terminal-followup-transition",
		HistoryDir:   filepath.Join(dir, "workers"),
		ThreadDir:    filepath.Join(dir, "threads"),
		HarnessDir:   filepath.Join(dir, "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var releaseSecondOnce sync.Once
	releaseSecond := func() { releaseSecondOnce.Do(func() { close(client.releaseSecond) }) }
	observerEntered := make(chan struct{})
	releaseObserverCh := make(chan struct{})
	var releaseObserverOnce sync.Once
	releaseObserver := func() { releaseObserverOnce.Do(func() { close(releaseObserverCh) }) }
	var observerOnce sync.Once
	control.beforeWorkerTerminalTransitionForTest = func(string) {
		observerOnce.Do(func() {
			close(observerEntered)
			<-releaseObserverCh
		})
	}
	terminalEntered := make(chan struct{})
	releaseTerminalCh := make(chan struct{})
	var releaseTerminalOnce sync.Once
	releaseTerminal := func() { releaseTerminalOnce.Do(func() { close(releaseTerminalCh) }) }
	t.Cleanup(func() {
		releaseObserver()
		releaseTerminal()
		releaseSecond()
		control.StopAll()
		deadline := time.Now().Add(2 * time.Second)
		for control.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		control.Close()
	})

	var terminalOnce sync.Once
	unsubscribe := control.SubscribeWorkerTerminalFinalizer(func(subagent.Notification) error {
		terminalOnce.Do(func() {
			close(terminalEntered)
			<-releaseTerminalCh
		})
		return nil
	})
	defer unsubscribe()

	spawned, err := control.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "terminal_followup_transition", Prompt: "finish",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-observerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not publish its terminal transition")
	}

	followupDone := make(chan error, 1)
	go func() {
		_, followupErr := control.FollowupTask(context.Background(), spawned.AgentID, "continue")
		followupDone <- followupErr
	}()
	select {
	case err := <-followupDone:
		t.Fatalf("follow-up crossed an unfinished terminal transition: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-client.secondStarted:
		t.Fatal("follow-up provider turn started before terminal finalization finished")
	default:
	}

	// Let the reliable observer acquire the transition lock. The follow-up
	// must remain parked until both durable bookkeeping and server finalizers
	// have completed and the worker lease is released.
	releaseObserver()
	select {
	case <-terminalEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not enter terminal finalization")
	}
	select {
	case err := <-followupDone:
		t.Fatalf("follow-up crossed a blocked terminal finalizer: %v", err)
	default:
	}
	releaseTerminal()
	select {
	case err := <-followupDone:
		if err != nil {
			t.Fatalf("FollowupTask: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up did not resume after terminal finalization")
	}
	select {
	case <-client.secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up provider turn did not start")
	}

	waitForWorkerLifecycleState(t, control, spawned.AgentID, agentthread.StatusRunning, harness.TaskStatusRunning)
	releaseSecond()
	waitForSubAgentStatus(t, control.Manager(), spawned.AgentID, subagent.StatusCompleted, 2*time.Second)
	waitForWorkerLifecycleState(t, control, spawned.AgentID, agentthread.StatusCompleted, harness.TaskStatusCompleted)
}

func waitForWorkerLifecycleState(t *testing.T, control *AgentControl, workerID string, threadStatus agentthread.Status, taskStatus harness.TaskStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		meta, found := control.Threads().Resolve(workerID)
		tasks, err := control.HarnessStore().ListTasks()
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		for _, task := range tasks {
			if found && meta.Status == threadStatus && task.ID == workerID && task.Status == taskStatus {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	meta, _ := control.Threads().Resolve(workerID)
	tasks, _ := control.HarnessStore().ListTasks()
	t.Fatalf("worker lifecycle did not reach thread=%s task=%s: thread=%+v tasks=%+v", threadStatus, taskStatus, meta, tasks)
}
