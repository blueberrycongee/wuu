package agentcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type nestedReplayBlockingClient struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func newNestedReplayBlockingClient() *nestedReplayBlockingClient {
	return &nestedReplayBlockingClient{started: make(chan struct{}), release: make(chan struct{})}
}

func (c *nestedReplayBlockingClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming request")
}

func (c *nestedReplayBlockingClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 1 {
		close(c.started)
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "parent resumed with nested result"}
	events <- providers.StreamEvent{Type: providers.EventDone}
	close(events)
	return events, nil
}

func (c *nestedReplayBlockingClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestResumeProducesNewDeliveryID proves the delivery ledger treats a
// fail->resume->complete cycle as two distinct results: the failed
// snapshot and the later completed snapshot each mint their own delivery
// id, and each is claimable exactly once. The resume delivers a second
// time without special-casing because completedAt/result/status changed.
func TestResumeProducesNewDeliveryID(t *testing.T) {
	threadDir := t.TempDir()
	c := &AgentControl{
		rootThreadID: "root-thread",
		threadStore:  agentthread.NewStore(threadDir),
	}
	base := time.Now().UTC()

	failed := subagent.SubAgentSnapshot{
		ID:          "worker-resume",
		AgentPath:   "/root/resume",
		ParentID:    "root-thread",
		Status:      subagent.StatusFailed,
		Error:       errors.New("api terminal error"),
		CompletedAt: base,
	}
	failedID := mustEnsureAgentResultDelivery(t, c, failed).ResultID
	if failedID == "" {
		t.Fatal("expected a delivery id for the failed run")
	}
	if ok, _, err := c.ClaimAgentResultDeliveryID(failedID, agentResultConsumerNestedFollowup); err != nil || !ok {
		t.Fatal("failed delivery should be claimable exactly once")
	}
	if ok, _, err := c.ClaimAgentResultDeliveryID(failedID, agentResultConsumerNestedFollowup); err != nil || ok {
		t.Fatal("failed delivery must not be claimable twice")
	}

	// The resume run reaches a new terminal snapshot: new status, new
	// completion time, new result -> a distinct delivery id.
	completed := subagent.SubAgentSnapshot{
		ID:          "worker-resume",
		AgentPath:   "/root/resume",
		ParentID:    "root-thread",
		Status:      subagent.StatusCompleted,
		Result:      "resumed done",
		CompletedAt: base.Add(time.Minute),
	}
	completedID := mustEnsureAgentResultDelivery(t, c, completed).ResultID
	if completedID == "" || completedID == failedID {
		t.Fatalf("resume should mint a new delivery id, failed=%q completed=%q", failedID, completedID)
	}
	if ok, _, err := c.ClaimAgentResultDeliveryID(completedID, agentResultConsumerNestedFollowup); err != nil || !ok {
		t.Fatal("completed delivery should be claimable exactly once")
	}
	if ok, _, err := c.ClaimAgentResultDeliveryID(completedID, agentResultConsumerNestedFollowup); err != nil || ok {
		t.Fatal("completed delivery must not be claimable twice")
	}
}

func TestAgentResultDeliveryClaimRestoresFromThreadEvents(t *testing.T) {
	threadDir := t.TempDir()
	snap := subagent.SubAgentSnapshot{
		ID:          "worker-1",
		AgentPath:   "/root/review",
		ParentID:    "root-thread",
		Status:      subagent.StatusCompleted,
		Result:      "done",
		CompletedAt: time.Now().UTC(),
	}

	first := &AgentControl{
		rootThreadID: "root-thread",
		threadStore:  agentthread.NewStore(threadDir),
	}
	delivery := mustEnsureAgentResultDelivery(t, first, snap)
	if delivery.ResultID == "" {
		t.Fatal("expected delivery result id")
	}
	if ok, consumedBy, err := first.ClaimAgentResultDeliveryID(delivery.ResultID, agentResultConsumerAwaitAgents); err != nil || !ok || consumedBy != "" {
		t.Fatalf("first claim = %v consumedBy=%q, want claimed", ok, consumedBy)
	}

	restored := &AgentControl{
		rootThreadID: "root-thread",
		threadStore:  agentthread.NewStore(threadDir),
	}
	restored.restoreAgentResultDeliveries()
	if ok, consumedBy, err := restored.ClaimAgentResultDeliveryID(delivery.ResultID, agentResultConsumerAutoCompletion); err != nil || ok || consumedBy != agentResultConsumerAwaitAgents {
		t.Fatalf("restored claim = %v consumedBy=%q, want already consumed by await_agents", ok, consumedBy)
	}
}

func TestAgentResultDeliveryClaimIsAtomicAcrossControls(t *testing.T) {
	threadDir := t.TempDir()
	snap := subagent.SubAgentSnapshot{
		ID:          "worker-shared",
		AgentPath:   "/root/shared",
		ParentID:    "root-thread",
		Status:      subagent.StatusCompleted,
		Result:      "shared result",
		CompletedAt: time.Now().UTC(),
	}
	controls := []*AgentControl{
		{rootThreadID: "root-thread", threadStore: agentthread.NewStore(threadDir)},
		{rootThreadID: "root-thread", threadStore: agentthread.NewStore(threadDir)},
	}
	resultID := mustEnsureAgentResultDelivery(t, controls[0], snap).ResultID
	controls[1].restoreAgentResultDeliveries()

	type outcome struct {
		consumer   string
		claimed    bool
		consumedBy string
		err        error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(controls))
	var wg sync.WaitGroup
	for i, control := range controls {
		wg.Add(1)
		consumer := fmt.Sprintf("consumer-%d", i)
		go func(control *AgentControl, consumer string) {
			defer wg.Done()
			<-start
			claimed, consumedBy, err := control.ClaimAgentResultDeliveryID(resultID, consumer)
			outcomes <- outcome{consumer: consumer, claimed: claimed, consumedBy: consumedBy, err: err}
		}(control, consumer)
	}
	close(start)
	wg.Wait()
	close(outcomes)

	winner := ""
	claimedCount := 0
	results := make([]outcome, 0, len(controls))
	for result := range outcomes {
		results = append(results, result)
		if result.err != nil {
			t.Fatalf("ClaimAgentResultDeliveryID: %v", result.err)
		}
		if result.claimed {
			claimedCount++
			winner = result.consumer
		}
	}
	if claimedCount != 1 {
		t.Fatalf("claimed count = %d, want 1: %+v", claimedCount, results)
	}
	for _, result := range results {
		if !result.claimed && result.consumedBy != winner {
			t.Fatalf("loser observed consumer %q, want %q", result.consumedBy, winner)
		}
	}
	events, err := agentthread.NewStore(threadDir).ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	claimCount := 0
	for _, event := range events {
		if event.Type == agentthread.EventResultClaim && event.ResultID == resultID {
			claimCount++
		}
	}
	if claimCount != 1 {
		t.Fatalf("durable claim count = %d, want 1", claimCount)
	}
}

func TestPendingNestedCompletionRehydratesParentAfterRestart(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	const sessionID = "nested-completion-restart"
	artifactDir := filepath.Join(dir, ".wuu-state", "sessions", sessionID)
	newControl := func(client providers.StreamClient) *AgentControl {
		t.Helper()
		control, err := New(Config{
			Client:       client,
			DefaultModel: "nested-replay-test",
			ParentRepo:   dir,
			WorktreeRoot: filepath.Join(dir, ".wuu-state", "worktrees"),
			SessionID:    sessionID,
			HistoryDir:   filepath.Join(artifactDir, "workers"),
			ThreadDir:    filepath.Join(artifactDir, "threads"),
			HarnessDir:   filepath.Join(artifactDir, "harness"),
			WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
				return fakeToolkit{}, nil
			},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return control
	}

	first := newControl(&fakeClient{resp: providers.ChatResponse{Content: "parent initial result"}})
	parent, err := first.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "parent",
		Prompt:      "finish before the nested child",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("spawn parent: %v", err)
	}
	childClient := newBlockingClient()
	first.UpdateWorkerDefaults(childClient, "nested-replay-test", subagent.ManagerOptions{})
	child, err := first.Spawn(context.Background(), SpawnRequest{
		Type:       DefaultSubagentType,
		TaskName:   "child",
		Prompt:     "finish during shutdown",
		ParentID:   parent.AgentID,
		ParentPath: parent.AgentPath,
	})
	if err != nil {
		t.Fatalf("spawn child: %v", err)
	}
	childClient.waitStarted(t)

	// Closing admission before cancellation reproduces the lost-delivery
	// window: terminal consumption records result-ready, but must not start or
	// queue a parent turn while shutdown is in progress.
	first.BeginShutdown()
	first.StopAll()
	first.YieldWorkerTerminalFinalizations()
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	childSnapshot, err := first.Manager().Wait(waitCtx, child.AgentID)
	cancel()
	if err != nil {
		t.Fatalf("wait child shutdown: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for first.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if first.HasOwnedWorkerExecutions() {
		t.Fatal("first control retained worker execution ownership")
	}
	resultID := first.AgentResultDeliveryID(childSnapshot)
	if resultID == "" {
		t.Fatal("shutdown child produced no durable result id")
	}
	if consumer, err := first.AgentResultDeliveryConsumer(resultID); err != nil || consumer != "" {
		t.Fatalf("nested result during shutdown consumer = %q, %v; want unclaimed", consumer, err)
	}
	// Reproduce the deeper crash window: the child result is durably reserved
	// for its parent, then the process dies before the parent inbox write or
	// Manager.Followup. Pending is deliberately replayable, unlike the old
	// one-phase nested_followup claim that lost the result forever.
	if claimed, consumedBy, err := first.ClaimAgentResultDeliveryID(resultID, agentResultConsumerNestedPending); err != nil || !claimed || consumedBy != "" {
		t.Fatalf("reserve nested result before restart = %t, %q, %v", claimed, consumedBy, err)
	}
	if consumer, err := first.AgentResultDeliveryConsumer(resultID); err != nil || consumer != agentResultConsumerNestedPending {
		t.Fatalf("nested result crash-window consumer = %q, %v; want pending", consumer, err)
	}
	if worker := first.Manager().Get(parent.AgentID); worker == nil || !subagent.IsTerminal(worker.Snapshot().Status) {
		t.Fatalf("shutdown unexpectedly resumed parent: %+v", worker)
	}
	first.Close()

	secondClient := newNestedReplayBlockingClient()
	second := newControl(secondClient)
	t.Cleanup(func() {
		second.BeginShutdown()
		second.StopAll()
		second.YieldWorkerTerminalFinalizations()
		deadline := time.Now().Add(2 * time.Second)
		for second.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		second.Close()
	})
	// Terminal recovery takes ownership first, then fails after the durable
	// inbox write while startup replay is waiting on the same result gate. The
	// waiter must observe that failure and take over; treating "in flight" as
	// success would let recovery acknowledge the child with no parent wakeup.
	firstAttemptEntered := make(chan struct{})
	releaseFirstAttempt := make(chan struct{})
	waiterEntered := make(chan struct{})
	var waiterOnce sync.Once
	var attemptMu sync.Mutex
	attempts := 0
	second.workerReleaseHookMu.Lock()
	second.beforeNestedResultFollowupForTest = func(string) error {
		attemptMu.Lock()
		attempts++
		attempt := attempts
		attemptMu.Unlock()
		if attempt == 1 {
			close(firstAttemptEntered)
			<-releaseFirstAttempt
			return errors.New("injected first-owner failure")
		}
		return nil
	}
	second.nestedResultDeliveryWaitForTest = func(string) {
		waiterOnce.Do(func() { close(waiterEntered) })
	}
	second.workerReleaseHookMu.Unlock()
	second.StartWorkerTerminalRecovery()
	select {
	case <-firstAttemptEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal recovery did not acquire the nested result gate")
	}
	second.StartQueuedWork()
	select {
	case <-waiterEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup replay did not wait for the first nested result owner")
	}
	close(releaseFirstAttempt)
	select {
	case <-secondClient.started:
	case <-time.After(2 * time.Second):
		t.Fatal("restored parent followup did not start")
	}
	close(secondClient.release)
	deadline = time.Now().Add(5 * time.Second)
	for {
		consumer, consumerErr := second.AgentResultDeliveryConsumer(resultID)
		if consumerErr != nil {
			t.Fatalf("read restored nested result consumer: %v", consumerErr)
		}
		if consumer == agentResultConsumerNestedFollowup {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restored nested result consumer = %q, want %q", consumer, agentResultConsumerNestedFollowup)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		worker := second.Manager().Get(parent.AgentID)
		if worker != nil && subagent.IsTerminal(worker.Snapshot().Status) && !second.HasOwnedWorkerExecutions() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restored parent did not finish nested-result turn: %+v", worker)
		}
		time.Sleep(10 * time.Millisecond)
	}
	history, ok := second.Manager().History(parent.AgentID)
	if !ok {
		t.Fatal("restored parent history is unavailable")
	}
	found := false
	for _, message := range history {
		var communication agentthread.InterAgentCommunication
		if json.Unmarshal([]byte(message.Content), &communication) == nil &&
			communication.Author == agentthread.AgentPath(child.AgentPath) &&
			communication.Recipient == agentthread.AgentPath(parent.AgentPath) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("restored parent history missing nested completion: %+v", history)
	}
	if calls := secondClient.callCount(); calls != 1 {
		t.Fatalf("concurrent nested recovery started %d parent turns, want 1", calls)
	}
	attemptMu.Lock()
	attemptCount := attempts
	attemptMu.Unlock()
	if attemptCount < 2 {
		t.Fatalf("nested result followup attempts = %d, want waiter takeover after owner failure", attemptCount)
	}
}

func mustEnsureAgentResultDelivery(t *testing.T, c *AgentControl, snap subagent.SubAgentSnapshot) agentResultDelivery {
	t.Helper()
	delivery, err := c.ensureAgentResultDelivery(snap)
	if err != nil {
		t.Fatalf("ensure result delivery: %v", err)
	}
	return delivery
}
