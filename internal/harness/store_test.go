package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStorePersistsTaskRunReportAndEvents(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	now := time.Date(2026, 5, 26, 1, 2, 3, 0, time.UTC)

	task := Task{
		ID:        "worker-1",
		SessionID: "sess-1",
		Path:      "/root/research",
		Name:      "research",
		Role:      "worker",
		Intent:    "inspect harness",
		Workspace: WorkspaceLease{
			Mode:      WorkspaceWorktree,
			Root:      "/tmp/wt",
			CreatedAt: now,
		},
		Status:    TaskStatusRunning,
		LastRunID: "worker-1-run-1",
		CreatedAt: now,
		UpdatedAt: now,
		StartedAt: now,
	}
	if err := store.UpsertTask(task); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}
	if err := store.UpsertRun(AgentRun{
		ID:        "worker-1-run-1",
		TaskID:    task.ID,
		AgentID:   task.ID,
		Role:      "worker",
		Status:    TaskStatusRunning,
		StartedAt: now,
	}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}
	if err := store.AppendEvent(Event{Type: EventTaskCreated, TaskID: task.ID, CreatedAt: now}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	report, err := store.SubmitReport(Report{
		TaskID:    task.ID,
		RunID:     task.LastRunID,
		AgentID:   task.ID,
		AgentPath: task.Path,
		Outcome:   "completed",
		Summary:   "Found the key files.",
		WorkDone:  []string{"Read agentcontrol."},
		Evidence: []EvidenceRef{{
			Type: "file",
			Path: "internal/agentcontrol/agent_control.go",
			Line: 180,
			Note: "spawn lifecycle",
		}},
		SubmittedAt: now,
	})
	if err != nil {
		t.Fatalf("SubmitReport: %v", err)
	}
	if report.ReportPath == "" {
		t.Fatal("expected report path")
	}
	if _, err := os.Stat(report.ReportPath); err != nil {
		t.Fatalf("report file missing: %v", err)
	}

	if _, err := store.UpdateRunStatus(task.LastRunID, TaskStatusCompleted, now.Add(time.Minute), 10, 20, ""); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	if _, err := store.UpdateTaskStatus(task.ID, TaskStatusCompleted, now.Add(time.Minute), 10, 20, ""); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != TaskStatusCompleted || tasks[0].ReportPath != report.ReportPath {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
	runs, err := store.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != TaskStatusCompleted || runs[0].InputTokens != 10 {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	artifacts, err := store.ListArtifacts()
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != ArtifactReport || artifacts[0].Path != report.ReportPath {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2 || events[0].Type != EventTaskCreated || events[1].Type != EventReportSubmitted {
		t.Fatalf("unexpected events: %+v", events)
	}
	if _, err := os.Stat(filepath.Join(dir, "reports", task.ID+".md")); err != nil {
		t.Fatalf("stable report path missing: %v", err)
	}
}

func TestStoreReportForTaskReturnsLatest(t *testing.T) {
	store := NewStore(t.TempDir())
	old := time.Date(2026, 5, 26, 1, 0, 0, 0, time.UTC)
	if _, err := store.SubmitReport(Report{
		ID:          "first",
		TaskID:      "task-1",
		Outcome:     "stuck",
		Summary:     "first",
		SubmittedAt: old,
	}); err != nil {
		t.Fatalf("SubmitReport first: %v", err)
	}
	if _, err := store.SubmitReport(Report{
		ID:          "second",
		TaskID:      "task-1",
		Outcome:     "completed",
		Summary:     "second",
		SubmittedAt: old.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SubmitReport second: %v", err)
	}
	report, ok, err := store.ReportForTask("task-1")
	if err != nil {
		t.Fatalf("ReportForTask: %v", err)
	}
	if !ok || report.ID != "second" || report.Outcome != "completed" {
		t.Fatalf("expected latest report, got %+v ok=%v", report, ok)
	}
}

func TestStoreMapsLegacyAwaitingReportToCompleted(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Simulate a record persisted by an older build that still used the
	// removed "awaiting_report" status. Reads must normalize it to
	// completed; the loop ended without error, so the run is completed.
	if err := os.WriteFile(filepath.Join(dir, "tasks.json"),
		[]byte(`[{"id":"legacy-1","status":"awaiting_report"}]`), 0o644); err != nil {
		t.Fatalf("write tasks.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runs.json"),
		[]byte(`[{"id":"legacy-1-run-1","task_id":"legacy-1","status":"awaiting_report"}]`), 0o644); err != nil {
		t.Fatalf("write runs.json: %v", err)
	}
	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != TaskStatusCompleted {
		t.Fatalf("legacy awaiting_report task must read as completed, got %+v", tasks)
	}
	runs, err := store.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != TaskStatusCompleted {
		t.Fatalf("legacy awaiting_report run must read as completed, got %+v", runs)
	}
}

func TestStorePersistsQueueItems(t *testing.T) {
	store := NewStore(t.TempDir())
	payload := json.RawMessage(`{"task":"queued"}`)
	if err := store.UpsertQueueItem(QueueItem{
		ID:      "queue-1",
		TaskID:  "worker-1",
		Kind:    "agent_spawn",
		Payload: payload,
	}); err != nil {
		t.Fatalf("UpsertQueueItem: %v", err)
	}
	items, err := store.ListQueueItems()
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	var decoded map[string]string
	if len(items) == 1 {
		if err := json.Unmarshal(items[0].Payload, &decoded); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
	}
	if len(items) != 1 || items[0].ID != "queue-1" || decoded["task"] != "queued" {
		t.Fatalf("unexpected queue items: %+v", items)
	}
	exists, err := store.QueueItemExists("queue-1")
	if err != nil || !exists {
		t.Fatalf("QueueItemExists(queue-1) = %v, %v; want true, nil", exists, err)
	}
	marked, err := store.MarkQueueItemCancelling("queue-1")
	if err != nil || !marked {
		t.Fatalf("MarkQueueItemCancelling(queue-1) = %v, %v; want true, nil", marked, err)
	}
	item, found, err := store.GetQueueItem("queue-1")
	if err != nil || !found || item.State != QueueItemStateCancelling {
		t.Fatalf("GetQueueItem(queue-1) = %+v, %v, %v; want cancelling, true, nil", item, found, err)
	}
	if err := store.DeleteQueueItem("queue-1"); err != nil {
		t.Fatalf("DeleteQueueItem: %v", err)
	}
	items, err = store.ListQueueItems()
	if err != nil {
		t.Fatalf("ListQueueItems after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected queue empty, got %+v", items)
	}
	exists, err = store.QueueItemExists("queue-1")
	if err != nil || exists {
		t.Fatalf("QueueItemExists(queue-1) after delete = %v, %v; want false, nil", exists, err)
	}
}

func TestStoreClaimQueueItemAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	storeA := NewStore(dir)
	storeB := NewStore(dir)
	if err := storeA.UpsertQueueItem(QueueItem{
		ID:     "queue-1",
		TaskID: "worker-1",
		Kind:   "agent_spawn",
	}); err != nil {
		t.Fatalf("UpsertQueueItem: %v", err)
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Store{storeA, storeB} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			<-start
			claimed, err := store.ClaimQueueItem("queue-1")
			if err != nil {
				errs <- err
				return
			}
			results <- claimed
		}(store)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("ClaimQueueItem: %v", err)
	}

	claimed := 0
	for result := range results {
		if result {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("successful claims = %d, want 1", claimed)
	}
	items, err := storeA.ListQueueItems()
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected queue empty, got %+v", items)
	}
}

// TestSubmitReportKindPrecedence locks the stand-in rules: a synthesized
// final_text report never lands when the task already has any report, and a
// structured submission supersedes a previously recorded stand-in — so
// concurrent completion-time synthesis and tool-time structured submissions
// stay deterministic regardless of arrival order.
func TestSubmitReportKindPrecedence(t *testing.T) {
	store := NewStore(t.TempDir())

	synth := Report{TaskID: "task-1", Kind: ReportKindFinalText, Outcome: "completed", Summary: "stand-in"}
	if _, err := store.SubmitReport(synth); err != nil {
		t.Fatalf("submit final_text: %v", err)
	}

	structured := Report{TaskID: "task-1", ID: "task-1-structured", Kind: ReportKindStructured, Outcome: "completed", Summary: "real handoff"}
	if _, err := store.SubmitReport(structured); err != nil {
		t.Fatalf("submit structured: %v", err)
	}

	reports, err := store.ListReports()
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != 1 || reports[0].Kind != ReportKindStructured || reports[0].Summary != "real handoff" {
		t.Fatalf("structured report must supersede the stand-in, got %+v", reports)
	}

	// A late stand-in is a no-op against an existing report.
	if _, err := store.SubmitReport(Report{TaskID: "task-1", Kind: ReportKindFinalText, Summary: "late stand-in"}); err != nil {
		t.Fatalf("late final_text: %v", err)
	}
	reports, _ = store.ListReports()
	if len(reports) != 1 || reports[0].Summary != "real handoff" {
		t.Fatalf("late stand-in must not land or clobber, got %+v", reports)
	}
}

func TestStoreConcurrentInstancesPreserveAllIndexes(t *testing.T) {
	dir := t.TempDir()
	stores := []*Store{NewStore(dir), NewStore(dir)}
	const perStore = 12
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	var wg sync.WaitGroup
	for storeIndex, store := range stores {
		wg.Add(1)
		go func(storeIndex int, store *Store) {
			defer wg.Done()
			<-start
			for i := 0; i < perStore; i++ {
				id := fmt.Sprintf("task-%d-%02d", storeIndex, i)
				runID := id + "-run"
				now := time.Date(2026, 7, 14, storeIndex, i, 0, 0, time.UTC)
				if err := store.UpsertTask(Task{
					ID:        id,
					Path:      "/root/" + id,
					Status:    TaskStatusRunning,
					LastRunID: runID,
					CreatedAt: now,
					UpdatedAt: now,
				}); err != nil {
					errs <- fmt.Errorf("upsert task %s: %w", id, err)
					return
				}
				if err := store.UpsertRun(AgentRun{
					ID:        runID,
					TaskID:    id,
					Status:    TaskStatusRunning,
					StartedAt: now,
				}); err != nil {
					errs <- fmt.Errorf("upsert run %s: %w", runID, err)
					return
				}
				if err := store.AddArtifact(Artifact{
					ID:        id + "-log",
					TaskID:    id,
					RunID:     runID,
					Kind:      ArtifactLog,
					Path:      filepath.Join("logs", id+".txt"),
					CreatedAt: now,
				}); err != nil {
					errs <- fmt.Errorf("add artifact %s: %w", id, err)
					return
				}
				if _, err := store.SubmitReport(Report{
					ID:          id + "-report",
					TaskID:      id,
					RunID:       runID,
					Kind:        ReportKindStructured,
					Outcome:     "completed",
					Summary:     id,
					SubmittedAt: now,
				}); err != nil {
					errs <- fmt.Errorf("submit report %s: %w", id, err)
					return
				}
				if err := store.UpsertQueueItem(QueueItem{
					ID:      id + "-queue",
					TaskID:  id,
					Kind:    "agent_spawn",
					Payload: json.RawMessage(`{"task":"queued"}`),
				}); err != nil {
					errs <- fmt.Errorf("upsert queue item %s: %w", id, err)
					return
				}
			}
		}(storeIndex, store)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	want := len(stores) * perStore
	store := NewStore(dir)
	tasks, err := store.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != want {
		t.Fatalf("task count = %d, want %d", len(tasks), want)
	}
	for _, task := range tasks {
		if task.ReportPath == "" || len(task.ArtifactPaths) != 2 {
			t.Fatalf("task %s has incomplete report/artifact links: %+v", task.ID, task)
		}
	}
	runs, err := store.ListRuns()
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != want {
		t.Fatalf("run count = %d, want %d", len(runs), want)
	}
	artifacts, err := store.ListArtifacts()
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(artifacts) != 2*want {
		t.Fatalf("artifact count = %d, want %d", len(artifacts), 2*want)
	}
	reports, err := store.ListReports()
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != want {
		t.Fatalf("report count = %d, want %d", len(reports), want)
	}
	queue, err := store.ListQueueItems()
	if err != nil {
		t.Fatalf("ListQueueItems: %v", err)
	}
	if len(queue) != want {
		t.Fatalf("queue count = %d, want %d", len(queue), want)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != 2*want {
		t.Fatalf("event count = %d, want %d", len(events), 2*want)
	}
}
