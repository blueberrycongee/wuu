package agentcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestUnconfirmedTerminalAfterSnapshotFailure(t *testing.T) {
	const id = "audit_worker"
	root := os.Getenv("WUU_AUDIT_TERMINAL_ROOT")
	if root != "" {
		harnessDir := filepath.Join(root, "harness")
		c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), harnessDir, 1, &fakeClient{resp: providers.ChatResponse{Content: "done"}}, nil)
		var failedAttempts atomic.Int32
		c.Manager().SetTerminalPrepareObserver(func(n subagent.Notification) error {
			if n.Status == subagent.StatusCompleted {
				return c.prepareWorkerTerminal(n)
			}
			failedAttempts.Add(1)
			return errors.New("injected terminal intent storage outage")
		})
		c.Manager().SetTerminalObserver(func(n subagent.Notification) error {
			if n.Status != subagent.StatusFailed || failedAttempts.Load() < 500 {
				os.Exit(18)
			}
			// Model process loss after prepare retries have exhausted, before finalization.
			os.Exit(0)
			return nil
		})
		_, err := c.Manager().Spawn(context.Background(), subagent.SpawnOptions{ID: id, Type: "explorer", ParentID: "queued-spawn-concurrency", AgentPath: "/root/audit_worker", Prompt: "finish", Toolkit: fakeToolkit{}, HistoryPath: root})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Second)
		t.Fatal("child did not reach failed terminal callback")
	}
	root = t.TempDir()
	persistQueuedSpawnForConcurrencyTest(t, filepath.Join(root, "harness"), id, time.Now().UTC())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestUnconfirmedTerminalAfterSnapshotFailure$", "-test.timeout=25s")
	cmd.Env = append(os.Environ(), "WUU_AUDIT_TERMINAL_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(workerTerminalFinalizationPath(filepath.Join(root, "harness"), id))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["phase"] != "draft" {
		t.Errorf("unconfirmed intent phase = %v, want draft", record["phase"])
	}
	client := &queuedSpawnCountingClient{}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
	got := make(chan subagent.Notification, 1)
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error { got <- n; return nil })
	defer un()
	c.StartWorkerTerminalRecovery()
	c.StartQueuedWork()
	select {
	case n := <-got:
		if n.Status != subagent.StatusInterrupted || n.Snapshot.Result != "done" || n.Snapshot.Error == nil || !strings.Contains(n.Snapshot.Error.Error(), "not confirmed") {
			t.Errorf("recovered unknown outcome = %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unknown outcome did not settle as interrupted")
	}
	if calls := client.calls.Load(); calls != 0 {
		t.Errorf("unconfirmed terminal relaunched worker %d times", calls)
	}
	waitForQueuedSpawnConcurrencyTest(t, func() bool { pending, err := c.workerTerminalFinalizationPending(id); return err == nil && !pending }, "unknown terminal to settle")
}

func TestTerminalRecoveryRechecksDraftUnderWorkerLease(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	const id = "stale_scan"
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted, ParentID: "queued-spawn-concurrency", AgentPath: "/root/stale_scan", Result: "old"}}
	stale := newWorkerTerminalFinalizationRecord(n)
	n.Snapshot.Result = "new unconfirmed"
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	got := make(chan subagent.Notification, 1)
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error { got <- n; return nil })
	defer un()
	c.startPendingWorkerTerminalRecovery(stale)
	c.workerTerminalRecoveryWG.Wait()
	select {
	case n := <-got:
		if n.Status != subagent.StatusInterrupted || n.Snapshot.Result != "new unconfirmed" {
			t.Fatalf("stale scan recovered %+v", n)
		}
	default:
		t.Fatal("current unconfirmed draft was not recovered")
	}
}

func TestFrozenTreeDoesNotConsumeTerminalDraft(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	const id = "frozen_draft"
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted, ParentID: "queued-spawn-concurrency", AgentPath: "/root/frozen_draft"}}
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	c.treeFrozen.Store(true)
	if got := c.ResolveFrozenWorkerTree(); len(got) != 0 {
		t.Fatalf("consumed unconfirmed results: %+v", got)
	}
	if pending, err := c.workerTerminalFinalizationPending(id); err != nil || !pending {
		t.Fatalf("draft pending=%v, %v", pending, err)
	}
}

func TestLegacyTerminalRecordStillRecovers(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	const id = "legacy_completed"
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted, ParentID: "queued-spawn-concurrency", AgentPath: "/root/legacy_completed", Result: "legacy result"}}
	if err := c.persistWorkerTerminalFinalization(n); err != nil {
		t.Fatal(err)
	}
	got := make(chan subagent.Notification, 1)
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error { got <- n; return nil })
	defer un()
	c.StartWorkerTerminalRecovery()
	select {
	case n := <-got:
		if n.Status != subagent.StatusCompleted || n.Snapshot.Result != "legacy result" {
			t.Fatalf("recovered %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy final record not recovered")
	}
}

func TestTerminalClosingCrashDoesNotReplayCompletedTurn(t *testing.T) {
	const id = "closing_crash"
	root := os.Getenv("WUU_AUDIT_CLOSING_ROOT")
	if root != "" {
		client := newClosingTurnBlockingClient()
		c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
		c.SetReportClosingFollowupHookForTest(func(workerID string) {
			if workerID == id {
				os.Exit(0)
			}
		})
		c.StartWorkerTerminalRecovery()
		c.StartQueuedWork()
		time.Sleep(20 * time.Second)
		t.Fatal("closing turn did not reach crash barrier")
	}
	root = t.TempDir()
	persistQueuedSpawnOfTypeForConcurrencyTest(t, filepath.Join(root, "harness"), id, requiresReportWorkerType, time.Now().UTC())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestTerminalClosingCrashDoesNotReplayCompletedTurn$", "-test.timeout=25s")
	cmd.Env = append(os.Environ(), "WUU_AUDIT_CLOSING_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(workerTerminalFinalizationPath(filepath.Join(root, "harness"), id))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record["phase"] != "draft" {
		t.Errorf("closing in flight left replayable phase %v", record["phase"])
	}
	client := &queuedSpawnCountingClient{}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
	got := make(chan subagent.Notification, 4)
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error { got <- n; return nil })
	defer un()
	c.StartWorkerTerminalRecovery()
	c.StartQueuedWork()
	select {
	case n := <-got:
		if n.Status != subagent.StatusInterrupted || n.Snapshot.Error == nil || !strings.Contains(n.Snapshot.Error.Error(), "not confirmed") {
			t.Errorf("closing outcome incorrectly recovered %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing outcome did not settle as interrupted")
	}
	if calls := client.calls.Load(); calls != 0 {
		t.Errorf("restarted closing or queued worker %d times", calls)
	}
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		pending, err := c.workerTerminalFinalizationPending(id)
		return err == nil && !pending && !c.workerExecutionOwned(id)
	}, "interrupted recovery to release worker")
	resumeCtx, cancelResume := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelResume()
	if _, err := c.FollowupTask(resumeCtx, id, "explicitly resume"); err != nil {
		t.Fatalf("explicit followup after unknown outcome: %v", err)
	}
	waitForQueuedSpawnConcurrencyTest(t, func() bool { return client.calls.Load() > 0 }, "explicit followup model request")
}

func TestConfirmTerminalRejectsStaleGenerationAndSnapshot(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	n := subagent.Notification{AgentID: "draft_id", Status: subagent.StatusCompleted, TerminalGeneration: "new", TerminalPrepared: true, Snapshot: subagent.SubAgentSnapshot{ID: "draft_id", Status: subagent.StatusCompleted, Result: "current"}}
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	stale := n
	stale.TerminalGeneration = "old"
	if err := c.confirmWorkerTerminal(stale); !errors.Is(err, errTerminalDraftMismatch) {
		t.Fatalf("stale generation err=%v", err)
	}
	altered := n
	altered.Snapshot.Result = "stale"
	if err := c.confirmWorkerTerminal(altered); !errors.Is(err, errTerminalDraftMismatch) {
		t.Fatalf("stale result err=%v", err)
	}
	notPrepared := n
	notPrepared.TerminalPrepared = false
	if err := c.confirmWorkerTerminal(notPrepared); !errors.Is(err, errTerminalDraftMismatch) {
		t.Fatalf("unprepared err=%v", err)
	}
	if err := c.confirmWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
}

func TestFailedClosingAdmissionRestoresFinal(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	const id = "failed_closing"
	meta := agentthread.Metadata{ID: id, SessionID: "queued-spawn-concurrency", ParentID: "queued-spawn-concurrency", Path: "/root/failed_closing", TaskName: id, Role: requiresReportWorkerType, Status: agentthread.StatusRunning, Source: agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentThreadID: "queued-spawn-concurrency", ParentPath: agentthread.RootPath, EdgeStatus: agentthread.EdgeOpen}}
	if err := c.threads.Restore(meta); err != nil {
		t.Fatal(err)
	}
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted, ParentID: "queued-spawn-concurrency", AgentPath: "/root/failed_closing"}}
	if err := c.persistWorkerTerminalFinalization(n); err != nil {
		t.Fatal(err)
	}
	// Manager has no worker for this ID, so FollowupForcingTool fails after the
	// draft is persisted. The prior completed result must remain replayable.
	continued, err := c.maybeNudgeReportClosing(n)
	if err != nil || continued {
		t.Fatalf("closing outcome = %v, %v", continued, err)
	}
	raw, err := os.ReadFile(workerTerminalFinalizationPath(c.harnessDir, id))
	if err != nil {
		t.Fatal(err)
	}
	var rec workerTerminalFinalizationRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Phase != "" || rec.Status != subagent.StatusCompleted {
		t.Fatalf("restored record = %+v", rec)
	}
}

func TestShutdownDoesNotCancelQueueWithUnconfirmedTerminal(t *testing.T) {
	root := t.TempDir()
	harnessDir := filepath.Join(root, "harness")
	const id = "draft_shutdown"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, id, time.Now().UTC())
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), harnessDir, 1, &fakeClient{}, nil)
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted}}
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	c.BeginShutdown()
	c.StopAll()
	exists, err := c.HarnessStore().QueueItemExists(id)
	if err != nil || !exists {
		t.Fatalf("unknown outcome lost queue intent: %v, %v", exists, err)
	}
	if pending, err := c.workerTerminalFinalizationPending(id); err != nil || !pending {
		t.Fatalf("draft pending=%v, %v", pending, err)
	}
}

type freezeBarrierDocuments struct {
	persistence.Documents
	entered, release chan struct{}
	once             atomic.Bool
}

func (d *freezeBarrierDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if d.once.CompareAndSwap(false, true) {
		close(d.entered)
		select {
		case <-d.release:
		case <-ctx.Done():
			return persistence.Receipt{}, ctx.Err()
		}
	}
	return d.Documents.Commit(ctx, tx)
}

func TestFrozenTreeCannotDeleteSuccessorDraft(t *testing.T) {
	root := t.TempDir()
	harnessDir := filepath.Join(root, "harness")
	const id = "frozen_successor"
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), harnessDir, 1, &fakeClient{}, nil)
	docs, err := persistence.Open(context.Background(), filepath.Join(root, "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	barrier := &freezeBarrierDocuments{Documents: docs, entered: make(chan struct{}), release: make(chan struct{})}
	c.threadStore = agentthread.NewDocumentStore(barrier, "queued-spawn-concurrency", filepath.Join(root, "control", "threads"))
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted, ParentID: "queued-spawn-concurrency", AgentPath: "/root/frozen_successor", Result: "old"}}
	if err := c.persistWorkerTerminalFinalization(n); err != nil {
		t.Fatal(err)
	}
	done := make(chan []FrozenWorkerResult, 1)
	c.treeFrozen.Store(true)
	go func() { done <- c.ResolveFrozenWorkerTree() }()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("freeze did not reach result claim")
	}
	lease, acquired, err := tryAcquireWorkerExecutionLease(harnessDir, id)
	if err != nil || acquired {
		if lease != nil {
			_ = lease.release()
		}
		t.Fatalf("successor lease during frozen claim = %v,%v, want busy", acquired, err)
	}
	close(barrier.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("freeze did not finish")
	}
	lease, acquired, err = tryAcquireWorkerExecutionLease(harnessDir, id)
	if err != nil || !acquired {
		t.Fatalf("successor lease after frozen claim = %v,%v", acquired, err)
	}
	defer lease.release()
	n.Snapshot.Result = "new candidate"
	n.TerminalGeneration = "successor"
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(workerTerminalFinalizationPath(harnessDir, id))
	if err != nil {
		t.Fatalf("freeze deleted successor draft: %v", err)
	}
	var rec workerTerminalFinalizationRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Phase != "draft" || rec.Generation != "successor" {
		t.Fatalf("successor record changed: %+v", rec)
	}
}

func TestUnconfirmedNestedOutcomePreservesEvidenceWithoutAutoWake(t *testing.T) {
	root := t.TempDir()
	client := &queuedSpawnCountingClient{}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, client, nil)
	parent := agentthread.Metadata{ID: "parent", SessionID: "queued-spawn-concurrency", ParentID: "queued-spawn-concurrency", Path: "/root/parent", TaskName: "parent", Status: agentthread.StatusWaitingChildren, Source: agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentThreadID: "queued-spawn-concurrency", ParentPath: agentthread.RootPath, EdgeStatus: agentthread.EdgeOpen}}
	if err := c.threads.Restore(parent); err != nil {
		t.Fatal(err)
	}
	const id = "nested_unknown"
	candidate := strings.Repeat("candidate text ", 1000)
	n := subagent.Notification{AgentID: id, Status: subagent.StatusCompleted, TerminalGeneration: "nested-generation", Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusCompleted, ParentID: "parent", AgentPath: "/root/parent/nested_unknown", Result: candidate}}
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	got := make(chan subagent.Notification, 1)
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error { got <- n; return nil })
	defer un()
	c.StartWorkerTerminalRecovery()
	c.StartQueuedWork()
	select {
	case delivered := <-got:
		if delivered.Status != subagent.StatusInterrupted || !delivered.TerminalUnconfirmed || delivered.Snapshot.Result != candidate || delivered.Snapshot.Error == nil || !strings.Contains(delivered.Snapshot.Error.Error(), "tool effects may already have occurred") {
			t.Fatalf("notification=%+v", delivered)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unknown finalizer not called")
	}
	waitForQueuedSpawnConcurrencyTest(t, func() bool { pending, err := c.workerTerminalFinalizationPending(id); return err == nil && !pending }, "unknown acknowledgement")
	entries, err := os.ReadDir(filepath.Join(c.harnessDir, "terminal-unknown"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("candidate archive: %v %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(c.harnessDir, "terminal-unknown", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var archived workerTerminalFinalizationRecord
	if err := json.Unmarshal(raw, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.Phase != "draft" || archived.Snapshot.Result != candidate {
		t.Fatalf("candidate evidence lost: %+v", archived)
	}
	events, err := c.threadStore.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == agentthread.EventResultReady {
			t.Fatalf("unconfirmed candidate made auto-deliverable: %+v", event)
		}
		if event.Type == agentthread.EventMessage && event.ThreadID == c.rootThreadID && !event.TriggerTurn && strings.Contains(event.Message, "not confirmed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no root notification carries uncertainty: %+v", events)
	}
	if calls := client.calls.Load(); calls != 0 {
		t.Fatalf("uncertain result triggered %d automatic model calls", calls)
	}
}

func TestUnconfirmedEvidenceIsIdempotentAndKeepsDistinctGenerations(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	n := subagent.Notification{AgentID: "evidence", Status: subagent.StatusFailed, TerminalGeneration: "g1", Snapshot: subagent.SubAgentSnapshot{ID: "evidence", Status: subagent.StatusFailed, Result: "candidate", Error: errors.New("original failure")}}
	first := newWorkerTerminalFinalizationRecord(n)
	first.Phase = "draft"
	first.Generation = n.TerminalGeneration
	firstData, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.persistUnconfirmedTerminalEvidence(firstData); err != nil {
		t.Fatal(err)
	}
	if err := c.persistUnconfirmedTerminalEvidence(firstData); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Generation = "g2"
	second.Snapshot.Result = "other candidate"
	secondData, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.persistUnconfirmedTerminalEvidence(secondData); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(c.harnessDir, "terminal-unknown"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("evidence generations = %v, %v", entries, err)
	}
	seen := map[string]string{}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(c.harnessDir, "terminal-unknown", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var rec workerTerminalFinalizationRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		seen[rec.Generation] = rec.Snapshot.Result
	}
	if seen["g1"] != "candidate" || seen["g2"] != "other candidate" {
		t.Fatalf("evidence=%v", seen)
	}
}

func TestUnconfirmedFinalizerFailureRetainsIntentAndEvidence(t *testing.T) {
	root := t.TempDir()
	harnessDir := filepath.Join(root, "harness")
	const id = "retry_unknown"
	persistQueuedSpawnForConcurrencyTest(t, harnessDir, id, time.Now().UTC())
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), harnessDir, 1, &fakeClient{}, nil)
	n := subagent.Notification{AgentID: id, Status: subagent.StatusFailed, TerminalGeneration: "failed-gen", Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusFailed, ParentID: "queued-spawn-concurrency", AgentPath: "/root/retry_unknown", Result: "candidate", Error: errors.New("original error")}}
	if err := c.prepareWorkerTerminal(n); err != nil {
		t.Fatal(err)
	}
	first := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	un := c.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error {
		if n.Status != subagent.StatusInterrupted || n.Snapshot.Error == nil || !strings.Contains(n.Snapshot.Error.Error(), "original error") || !strings.Contains(n.Snapshot.Error.Error(), "tool effects may already have occurred") {
			return errors.New("incorrect uncertainty")
		}
		if calls.Add(1) == 1 {
			close(first)
			<-release
			return errors.New("external store unavailable")
		}
		return nil
	})
	defer un()
	c.StartWorkerTerminalRecovery()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("finalizer not entered")
	}
	exists, err := c.HarnessStore().QueueItemExists(id)
	if err != nil || !exists {
		t.Fatalf("queue before finalizer success=%v,%v", exists, err)
	}
	if pending, err := c.workerTerminalFinalizationPending(id); err != nil || !pending {
		t.Fatalf("terminal before success=%v,%v", pending, err)
	}
	entries, err := os.ReadDir(filepath.Join(harnessDir, "terminal-unknown"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("evidence before success=%v,%v", entries, err)
	}
	close(release)
	waitForQueuedSpawnConcurrencyTest(t, func() bool {
		pending, err := c.workerTerminalFinalizationPending(id)
		return err == nil && !pending && calls.Load() >= 2
	}, "unknown finalizer retry")
	entries, err = os.ReadDir(filepath.Join(harnessDir, "terminal-unknown"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("evidence after retry=%v,%v", entries, err)
	}
}

func TestUnconfirmedRootCommunicationDeduplicatesWithinGeneration(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	const id = "unknown_same_snapshot"
	n := subagent.Notification{AgentID: id, Status: subagent.StatusInterrupted, TerminalUnconfirmed: true, TerminalGeneration: "g1", Snapshot: subagent.SubAgentSnapshot{ID: id, Status: subagent.StatusInterrupted, ParentID: "queued-spawn-concurrency", AgentPath: "/root/unknown_same_snapshot", Result: "same", Error: errors.New("not confirmed")}}
	for _, gen := range []string{"g1", "g1", "g2", "g2"} {
		n.TerminalGeneration = gen
		if _, err := c.consumeWorkerNotification(n); err != nil {
			t.Fatal(err)
		}
	}
	events, err := c.threadStore.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == agentthread.EventMessage && event.ThreadID == c.rootThreadID && strings.Contains(event.Message, "not confirmed") {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("root notifications=%d, want one for each generation", count)
	}
}

func TestLateTerminalSnapshotIsNotAgentCompletionSignal(t *testing.T) {
	root := t.TempDir()
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 1, &fakeClient{}, nil)
	n := subagent.Notification{AgentID: "worker", Status: subagent.StatusCompleted, SnapshotOnly: true, Snapshot: subagent.SubAgentSnapshot{ID: "worker", Status: subagent.StatusCompleted, ParentID: "queued-spawn-concurrency"}}
	if signal, ok := c.agentNotificationSignal("", n); ok {
		t.Fatalf("late usage became completion signal: %+v", signal)
	}
}

func TestFreezeSkipsOwnedDraftWithoutLateParentWake(t *testing.T) {
	root := t.TempDir()
	parentTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "parent"}
	childTurn := scriptedStreamTurn{started: make(chan struct{}), gate: make(chan struct{}), content: "child"}
	client := &scriptedStreamClient{steps: []scriptedStreamTurn{parentTurn, childTurn}}
	c := newDormantQueuedSpawnConcurrencyControl(t, root, filepath.Join(root, "control"), filepath.Join(root, "harness"), 2, client, nil)
	parent, err := c.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "freeze_parent", Prompt: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-parentTurn.started:
	case <-time.After(5 * time.Second):
		t.Fatal("parent not started")
	}
	child, err := c.Spawn(context.Background(), SpawnRequest{Type: DefaultSubagentType, TaskName: "freeze_child", Prompt: "child", ParentID: parent.AgentID, ParentPath: parent.AgentPath})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-childTurn.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child not started")
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	c.SetWorkerTerminalTransitionHookForTest(func(id string) {
		if id == child.AgentID {
			once.Do(func() { close(entered); <-release })
		}
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	freezeDone := make(chan struct{})
	go func() { c.FreezeWorkerTree(); close(freezeDone) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("child terminal did not reach barrier")
	}
	select {
	case <-freezeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("freeze did not settle")
	}
	if !c.workerExecutionOwned(child.AgentID) {
		t.Fatal("child must retain lease while observer is blocked")
	}
	c.ResolveFrozenWorkerTree()
	if c.WorkerTreeFrozen() {
		t.Fatal("freeze did not lift")
	}
	close(release)
	waitForQueuedSpawnConcurrencyTest(t, func() bool { return !c.workerExecutionOwned(child.AgentID) }, "late child terminal to release lease")
	time.Sleep(150 * time.Millisecond)
	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 2 {
		t.Fatalf("late child triggered automatic model turn; calls=%d", calls)
	}
	if got := c.Manager().Get(parent.AgentID).Snapshot().Status; got != subagent.StatusCancelled {
		t.Fatalf("parent status after late child=%s", got)
	}
}
