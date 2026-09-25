package agentcontrol

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// TestAwaitAlwaysCarriesResultTextAfterConsumption verifies the delivery
// ledger dedupes injection only: once a worker's result has been claimed, an
// explicit re-await still returns the full result text so a parent that asks
// again is never handed an empty row.
func TestAwaitAlwaysCarriesResultTextAfterConsumption(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "durable worker answer"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-await-carry",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-await-carry", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-await-carry", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "carry_result",
		Prompt:      "finish the task",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	first, err := c.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
	if err != nil {
		t.Fatalf("first AwaitFrom: %v", err)
	}
	if len(first.Results) != 1 || strings.TrimSpace(first.Results[0].Result) == "" {
		t.Fatalf("first await should carry the result text, got %+v", first.Results)
	}

	second, err := c.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
	if err != nil {
		t.Fatalf("second AwaitFrom: %v", err)
	}
	if len(second.Results) != 1 {
		t.Fatalf("explicit re-await should still return the task, got %+v", second.Results)
	}
	if strings.TrimSpace(second.Results[0].Result) == "" {
		t.Fatalf("explicit re-await must still carry the result text after the delivery was consumed, got %+v", second.Results[0])
	}
}

// TestAwaitFromRehydratesAcrossRestart simulates a process restart: a first
// AgentControl runs a worker to completion (with a submitted report), and a
// fresh instance pointed at the same state dirs must let await_agents see
// that dormant run — the same lazy rehydration send_message/followup_task
// already get — instead of reporting not_found. Targetless awaits must not
// rehydrate anything.
func TestAwaitFromRehydratesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	historyDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-restart", "workers")
	threadDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-restart", "threads")
	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-restart", "harness")
	config := func(client providers.StreamClient) Config {
		return Config{
			Client:        client,
			DefaultModel:  "fake-model",
			ParentRepo:    dir,
			WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
			SessionID:     "sess-await-restart",
			HistoryDir:    historyDir,
			ThreadDir:     threadDir,
			HarnessDir:    harnessDir,
			WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		}
	}

	first, err := New(config(&fakeClient{resp: providers.ChatResponse{Content: "done before restart"}}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := first.Spawn(context.Background(), SpawnRequest{
		Type:        DefaultSubagentType,
		TaskName:    "await_restart",
		Prompt:      "finish before the restart",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("expected completed first run, got %s", res.Status)
	}
	report, err := first.RecordAgentReport(res.AgentID, res.AgentPath, AgentReportRequest{
		Outcome: "completed",
		Summary: "Finished before the restart.",
	})
	if err != nil {
		t.Fatalf("RecordAgentReport: %v", err)
	}
	waitForHarnessEvent(t, first.HarnessStore(), harness.EventRunCompleted, res.AgentID)
	first.Close()

	// Fresh AgentControl over the same state dirs stands in for a restart.
	second, err := New(config(&fakeClient{resp: providers.ChatResponse{Content: "unused after restart"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	if second.Manager().Get(res.AgentID) != nil {
		t.Fatal("restarted manager should not track the dead run before rehydration")
	}

	// A targetless await never scans history: the dormant run stays dormant.
	blank, err := second.AwaitFrom(agentthread.RootPath, context.Background(), nil)
	if err != nil {
		t.Fatalf("targetless AwaitFrom: %v", err)
	}
	if len(blank.Results) != 0 {
		t.Fatalf("targetless await after restart should see no active children, got %+v", blank.Results)
	}
	if second.Manager().Get(res.AgentID) != nil {
		t.Fatal("targetless await must not rehydrate dormant runs")
	}

	awaited, err := second.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom across restart: %v", err)
	}
	if len(awaited.Results) != 1 {
		t.Fatalf("expected one await result, got %+v", awaited.Results)
	}
	got := awaited.Results[0]
	if got.Status == "not_found" {
		t.Fatalf("explicit await should rehydrate the dormant run, got not_found: %+v", got)
	}
	if got.Status != string(harness.TaskStatusCompleted) {
		t.Fatalf("await status = %q, want %q (result %+v)", got.Status, harness.TaskStatusCompleted, got)
	}
	if got.AgentID != res.AgentID {
		t.Fatalf("await agent id = %q, want %q", got.AgentID, res.AgentID)
	}
	if got.ReportPath != report.ReportPath {
		t.Fatalf("await report path = %q, want %q", got.ReportPath, report.ReportPath)
	}
	if second.Manager().Get(res.AgentID) == nil {
		t.Fatal("explicit await should leave the rehydrated run addressable for follow-ups")
	}
}

// TestNoTargetAwaitDoesNotRejoinDeliveredCompletedWithoutReport reproduces the
// polling trap observed with live models: a worker that finishes without a
// structured report is completed with its report missing, and a no-target
// await used to re-join it on every call, returning an already-consumed empty
// row plus guidance the parent can never satisfy.
func TestNoTargetAwaitDoesNotRejoinDeliveredCompletedWithoutReport(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "raw worker answer"}},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-await-loop",
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-await-loop", "threads"),
		HarnessDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-await-loop", "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     DefaultSubagentType,
		TaskName: "await_report_loop",
		Prompt:   "finish without report",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	first, err := c.AwaitFrom(agentthread.RootPath, context.Background(), nil)
	if err != nil {
		t.Fatalf("first AwaitFrom: %v", err)
	}
	if len(first.Results) != 1 || first.Results[0].Status != string(harness.TaskStatusCompleted) || !first.Results[0].ReportMissing {
		t.Fatalf("first no-target await should deliver the completed (report-missing) result, got %+v", first)
	}
	if first.Results[0].Result == "" {
		t.Fatalf("first delivery should carry the raw result, got %+v", first.Results[0])
	}

	second, err := c.AwaitFrom(agentthread.RootPath, context.Background(), nil)
	if err != nil {
		t.Fatalf("second AwaitFrom: %v", err)
	}
	if len(second.Results) != 0 {
		t.Fatalf("no-target await must not re-join a terminal task whose result was delivered, got %+v", second.Results)
	}

	explicit, err := c.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
	if err != nil {
		t.Fatalf("explicit AwaitFrom: %v", err)
	}
	if len(explicit.Results) != 1 || explicit.Results[0].Status != string(harness.TaskStatusCompleted) || !explicit.Results[0].ReportMissing {
		t.Fatalf("explicit-target await should still report the completed task, got %+v", explicit)
	}
}

// closingTurnBlockingClient completes the worker's first model turn
// instantly with plain text (no agent_report), then blocks every later turn
// — in these tests, the requires_report closing nudge — until release is
// closed. started is closed when the second turn begins, giving tests a
// deterministic hook for "the closing turn is in flight".
type closingTurnBlockingClient struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func newClosingTurnBlockingClient() *closingTurnBlockingClient {
	return &closingTurnBlockingClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *closingTurnBlockingClient) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "finished without filing a report"}, nil
}

func (c *closingTurnBlockingClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	ch := make(chan providers.StreamEvent, 2)
	if call == 1 {
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "finished without filing a report"}
		ch <- providers.StreamEvent{Type: providers.EventDone}
		close(ch)
		return ch, nil
	}
	if call == 2 {
		close(c.started)
	}
	go func() {
		select {
		case <-c.release:
			ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "closing turn text"}
			ch <- providers.StreamEvent{Type: providers.EventDone}
		case <-ctx.Done():
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}
		close(ch)
	}()
	return ch, nil
}

// TestAwaitTreatsUnsettledRequiresReportCompletionAsActive is the
// deterministic reproduction of the settlement window itself: the manager
// has flipped a requires_report run to completed (the row below) but the
// notification consumer has not adjudicated it yet (the run is still marked
// pending, exactly as Spawn leaves it). await must keep waiting, and must
// release the moment the consumer records the terminal state. Rows for runs
// this process never started, and non-completed rows, are untouched.
func TestAwaitTreatsUnsettledRequiresReportCompletionAsActive(t *testing.T) {
	dir := t.TempDir()
	c, err := New(Config{
		Client:        &fakeClient{},
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	completed := []AwaitAgentResult{{AgentID: "w1", Status: string(harness.TaskStatusCompleted)}}

	// The window: spawn marked the run, the consumer has not settled it.
	c.markReportSettlementPending("w1")
	if c.awaitComplete(completed) {
		t.Fatal("await must keep waiting while a requires_report completion is unsettled")
	}
	// Only completed rows wait on settlement; a failed run of the same id
	// takes the normal path.
	failed := []AwaitAgentResult{{AgentID: "w1", Status: string(harness.TaskStatusFailed)}}
	if !c.awaitComplete(failed) {
		t.Fatal("non-completed terminal rows must not wait on report settlement")
	}
	// The consumer recorded the terminal notification: settled.
	c.clearReportSettlement("w1")
	if !c.awaitComplete(completed) {
		t.Fatal("await must release once the consumer settled the run")
	}
	// Runs this process never started (dormant/rehydrated) are never pending.
	other := []AwaitAgentResult{{AgentID: "w-dormant", Status: string(harness.TaskStatusCompleted)}}
	if !c.awaitComplete(other) {
		t.Fatal("runs from previous processes must settle immediately")
	}
}

// TestAwaitWaitsOutRequiresReportClosingTurn drives the full race end to
// end: a requires_report worker completes its first turn without a report,
// the runtime launches the mechanical closing turn (held open by the
// blocking client), and an await running through all of it must not return
// until the run's report is durably adjudicated — here the synthesized
// final_text report after the closing turn also files nothing.
func TestAwaitWaitsOutRequiresReportClosingTurn(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	client := newClosingTurnBlockingClient()
	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-settle", "harness")
	c, err := New(Config{
		Client:        client,
		DefaultModel:  "fake-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:     "sess-await-settle",
		HistoryDir:    filepath.Join(dir, ".wuu-state", "sessions", "sess-await-settle", "workers"),
		ThreadDir:     filepath.Join(dir, ".wuu-state", "sessions", "sess-await-settle", "threads"),
		HarnessDir:    harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	t.Cleanup(func() {
		select {
		case <-client.release:
		default:
			close(client.release)
		}
	})

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:     requiresReportWorkerType,
		TaskName: "review_settle",
		Prompt:   "review this diff",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("closing turn never started")
	}

	done := make(chan AwaitAgentsResult, 1)
	go func() {
		awaited, aerr := c.AwaitFrom(agentthread.RootPath, context.Background(), []string{res.AgentID})
		if aerr != nil {
			t.Errorf("AwaitFrom: %v", aerr)
		}
		done <- awaited
	}()

	// While the closing turn is in flight the run is not consumable: the
	// first completion window and the running closing turn must both hold
	// the await open.
	select {
	case awaited := <-done:
		t.Fatalf("await returned while the closing turn was still in flight: %+v", awaited.Results)
	case <-time.After(300 * time.Millisecond):
	}

	close(client.release)

	var awaited AwaitAgentsResult
	select {
	case awaited = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("await did not return after the closing turn settled")
	}
	if len(awaited.Results) != 1 {
		t.Fatalf("expected one await result, got %+v", awaited.Results)
	}
	got := awaited.Results[0]
	if got.Status != string(harness.TaskStatusCompleted) {
		t.Fatalf("await status = %q, want completed (%+v)", got.Status, got)
	}
	// The settlement invariant: by the time await hands the row to the
	// parent, the run's report adjudication is durably recorded.
	report, ok, err := c.HarnessStore().ReportForTask(res.AgentID)
	if err != nil || !ok {
		t.Fatalf("await returned before the report was adjudicated (ok=%v err=%v)", ok, err)
	}
	if harness.NormalizeReportKind(report.Kind) != harness.ReportKindFinalText {
		t.Fatalf("expected synthesized final_text report, got %+v", report)
	}
	if got.ReportKind != string(harness.ReportKindFinalText) || got.ReportPath == "" {
		t.Fatalf("await row must carry the adjudicated report facts, got %+v", got)
	}
	task, ok := c.harnessTask(res.AgentID)
	if !ok || task.Status != harness.TaskStatusCompleted {
		t.Fatalf("harness task must be terminal when await returns, got %+v ok=%v", task, ok)
	}
}

// TestAwaitSettlesRehydratedRunKilledMidClosingTurn covers the crash
// tombstone: a process dies while a requires_report closing turn is in
// flight, leaving a terminal persisted snapshot but a harness task stuck
// "running" with no report. A fresh process must not hang awaiting a
// consumer decision that is never coming: the explicit await rehydrates the
// run, reconciles the harness record to the snapshot's terminal truth,
// synthesizes the swallowed final_text report, and returns promptly.
func TestAwaitSettlesRehydratedRunKilledMidClosingTurn(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	historyDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-crash", "workers")
	threadDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-crash", "threads")
	harnessDir := filepath.Join(dir, ".wuu-state", "sessions", "sess-await-crash", "harness")
	config := func(client providers.StreamClient) Config {
		return Config{
			Client:        client,
			DefaultModel:  "fake-model",
			ParentRepo:    dir,
			WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
			SessionID:     "sess-await-crash",
			HistoryDir:    historyDir,
			ThreadDir:     threadDir,
			HarnessDir:    harnessDir,
			WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		}
	}

	client := newClosingTurnBlockingClient()
	first, err := New(config(client))
	if err != nil {
		t.Fatal(err)
	}
	res, err := first.Spawn(context.Background(), SpawnRequest{
		Type:     requiresReportWorkerType,
		TaskName: "review_crash",
		Prompt:   "review this diff",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Let the abandoned closing turn finish and persist BEFORE TempDir
	// cleanup: the terminal notification is emitted strictly after the run's
	// history write, so draining it removes the write/removal race.
	t.Cleanup(func() {
		ch := make(chan subagent.Notification, 16)
		first.Manager().Subscribe(ch)
		defer first.Manager().Unsubscribe(ch)
		select {
		case <-client.release:
		default:
			close(client.release)
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case n := <-ch:
				if n.AgentID == res.AgentID && isFinalSubAgentStatus(n.Status) {
					return
				}
			case <-deadline:
				return
			}
		}
	})
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("closing turn never started")
	}
	// Precondition for the tombstone: the consumer skipped recording (it
	// started the nudge instead), so the harness task is still running and
	// no report exists, while the persisted snapshot already says completed.
	if task, ok := first.harnessTask(res.AgentID); !ok || task.Status != harness.TaskStatusRunning {
		t.Fatalf("expected harness task stuck running mid closing turn, got %+v ok=%v", task, ok)
	}
	if _, ok, _ := first.HarnessStore().ReportForTask(res.AgentID); ok {
		t.Fatal("no report should exist mid closing turn")
	}
	// "Kill" the process mid closing turn: stop the consumer and abandon
	// the in-flight turn without letting it finish.
	first.Close()

	second, err := New(config(&fakeClient{resp: providers.ChatResponse{Content: "unused after crash"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	awaited, err := second.AwaitFrom(agentthread.RootPath, ctx, []string{res.AgentID})
	if err != nil {
		t.Fatalf("AwaitFrom after crash: %v", err)
	}
	if awaited.TimedOut {
		t.Fatalf("await must settle a rehydrated dormant run immediately, got timeout: %+v", awaited.Results)
	}
	if len(awaited.Results) != 1 || awaited.Results[0].Status != string(harness.TaskStatusCompleted) {
		t.Fatalf("expected completed rehydrated result, got %+v", awaited.Results)
	}
	// The tombstone is repaired: task terminal, swallowed report synthesized.
	task, ok := second.harnessTask(res.AgentID)
	if !ok || task.Status != harness.TaskStatusCompleted {
		t.Fatalf("rehydration should reconcile the stuck harness task, got %+v ok=%v", task, ok)
	}
	report, ok, err := second.HarnessStore().ReportForTask(res.AgentID)
	if err != nil || !ok || harness.NormalizeReportKind(report.Kind) != harness.ReportKindFinalText {
		t.Fatalf("rehydration should synthesize the swallowed final_text report, got %+v ok=%v err=%v", report, ok, err)
	}
	if awaited.Results[0].ReportPath == "" {
		t.Fatalf("await row should carry the reconciled report path, got %+v", awaited.Results[0])
	}
}
