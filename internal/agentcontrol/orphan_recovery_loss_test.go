package agentcontrol

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type lostOrphanTaskReply struct {
	persistence.Documents
	lost  atomic.Bool
	down  atomic.Bool
	count atomic.Int32
	stage int32
}

func (d *lostOrphanTaskReply) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if d.down.Load() {
		return persistence.View{}, errors.New("provider disconnected")
	}
	return d.Documents.Read(ctx, q)
}

func (d *lostOrphanTaskReply) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if d.down.Load() {
		return persistence.Receipt{}, errors.New("provider disconnected")
	}
	receipt, err := d.Documents.Commit(ctx, tx)
	match := err == nil && len(tx.Writes) > 0 && tx.Writes[0].Namespace == harness.DocumentNamespace && bytes.Contains(tx.Writes[0].Value, []byte(`"interrupted"`))
	if match && d.count.Add(1) == d.stage && d.lost.CompareAndSwap(false, true) {
		d.down.Store(true)
		return persistence.Receipt{}, errors.New("lost task status acknowledgment")
	}
	return receipt, err
}

func TestOrphanRecoveryRepairsPartialInterruptedProjectionAfterLostReply(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			for _, stage := range []int32{1, 2, 3} {
				t.Run(fmt.Sprintf("stage-%d", stage), func(t *testing.T) {
					dir := t.TempDir()
					raw, err := persistence.Open(context.Background(), filepath.Join(dir, "docs"), backend)
					if err != nil {
						t.Fatal(err)
					}
					defer raw.Close()
					hs := harness.NewDocumentStore(raw, "thread")
					task := harness.Task{ID: "worker", Status: harness.TaskStatusRunning, LastRunID: "run-worker", StartedAt: time.Now()}
					if err := hs.UpsertTask(task); err != nil {
						t.Fatal(err)
					}
					if err := hs.UpsertRun(harness.AgentRun{ID: "run-worker", TaskID: "worker", Status: harness.TaskStatusRunning}); err != nil {
						t.Fatal(err)
					}
					ts := agentthread.NewDocumentStore(raw, "thread")
					if err := ts.UpsertThread(agentthread.Metadata{ID: "worker", SessionID: "thread", Path: "/root/worker", Status: agentthread.StatusRunning}); err != nil {
						t.Fatal(err)
					}
					writeResumeSnapshot(t, filepath.Join(dir, "workers"), "worker", subagent.ResumeSnapshotVersion, dir, "running")
					docs := &lostOrphanTaskReply{Documents: raw, stage: stage}
					cfg := Config{Documents: docs, Client: &fakeClient{resp: providers.ChatResponse{Content: "done"}}, SessionID: "thread", ParentRepo: dir, HistoryDir: filepath.Join(dir, "workers"), ThreadDir: filepath.Join(dir, "threads"), HarnessDir: filepath.Join(dir, "harness"), WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }}
					first, firstErr := New(cfg)
					if first != nil {
						first.Close()
					}
					if firstErr == nil {
						t.Fatal("recovery must report the uncertain write and not publish a partial control")
					}
					if !docs.lost.Load() {
						t.Fatal("fixture did not lose the task write acknowledgment")
					}
					if err := raw.Close(); err != nil {
						t.Fatal(err)
					}
					raw, err = persistence.Open(context.Background(), filepath.Join(dir, "docs"), backend)
					if err != nil {
						t.Fatal(err)
					}
					defer raw.Close()
					docs.Documents = raw
					hs = harness.NewDocumentStore(raw, "thread")
					ts = agentthread.NewDocumentStore(raw, "thread")
					docs.down.Store(false)
					retry, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer retry.Close()
					runs, err := hs.ListRuns()
					if err != nil {
						t.Fatal(err)
					}
					if len(runs) != 1 || runs[0].Status != harness.TaskStatusInterrupted {
						t.Fatalf("recovery reported success but run remains %+v", runs)
					}
					rows, err := ts.ListThreads()
					if err != nil {
						t.Fatal(err)
					}
					childFound := false
					for _, row := range rows {
						if row.ID == "worker" && row.Status != agentthread.StatusFailed {
							t.Fatalf("child remains %s", row.Status)
						}
						childFound = childFound || row.ID == "worker"
					}
					if !childFound {
						t.Fatal("worker child disappeared")
					}
					worker, err := subagent.LoadPersistedRunFromDocuments(raw, "thread", "worker")
					if err != nil || worker.Status != subagent.StatusInterrupted {
						t.Fatalf("worker snapshot not recovered: %+v %v", worker, err)
					}
					before, err := hs.ReadEvents()
					if err != nil {
						t.Fatal(err)
					}
					var interruptedEvents int
					for _, event := range before {
						if event.Type == harness.EventTaskStatusChanged && event.TaskID == "worker" && event.Status == string(harness.TaskStatusInterrupted) {
							interruptedEvents++
						}
					}
					if interruptedEvents != 1 {
						t.Fatalf("want one interrupted event, got %d", interruptedEvents)
					}
					retry.Close()
					third, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer third.Close()
					after, err := hs.ReadEvents()
					if err != nil {
						t.Fatal(err)
					}
					if len(before) != len(after) {
						t.Fatalf("repeated recovery duplicated events: %d -> %d", len(before), len(after))
					}
				})
			}
		})
	}
}

func TestInterruptedRepairRespectsOtherTerminalTasksAndLiveWorkers(t *testing.T) {
	dir := t.TempDir()
	docs, err := persistence.Open(context.Background(), filepath.Join(dir, "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	hs := harness.NewDocumentStore(docs, "thread")
	for _, task := range []harness.Task{{ID: "busy", Status: harness.TaskStatusInterrupted, LastRunID: "busy-run"}, {ID: "complete", Status: harness.TaskStatusCompleted, LastRunID: "complete-run"}} {
		if err := hs.UpsertTask(task); err != nil {
			t.Fatal(err)
		}
		if err := hs.UpsertRun(harness.AgentRun{ID: task.LastRunID, TaskID: task.ID, Status: harness.TaskStatusRunning}); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, "harness")
	lease, acquired, err := tryAcquireWorkerExecutionLease(root, "busy")
	if err != nil || !acquired {
		t.Fatalf("worker lease: %v %v", acquired, err)
	}
	defer lease.release()
	c, err := New(Config{Documents: docs, Client: &fakeClient{}, SessionID: "thread", ParentRepo: dir, HistoryDir: filepath.Join(dir, "workers"), ThreadDir: filepath.Join(dir, "threads"), HarnessDir: root, WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	runs, err := hs.ListRuns()
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if run.Status != harness.TaskStatusRunning {
			t.Fatalf("ineligible run was modified: %+v", run)
		}
	}
}

func TestInterruptedRepairDoesNotUseWorkerLeaseAcquiredBySameControl(t *testing.T) {
	dir := t.TempDir()
	docs, err := persistence.Open(context.Background(), filepath.Join(dir, "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	hs := harness.NewDocumentStore(docs, "thread")
	task := harness.Task{ID: "worker", Status: harness.TaskStatusInterrupted, LastRunID: "worker-run"}
	if err := hs.UpsertTask(task); err != nil {
		t.Fatal(err)
	}
	if err := hs.UpsertRun(harness.AgentRun{ID: "worker-run", TaskID: "worker", Status: harness.TaskStatusRunning}); err != nil {
		t.Fatal(err)
	}
	c := &AgentControl{documents: docs, sessionID: "thread", harnessDir: filepath.Join(dir, "harness"), harnessStore: hs, workerLeases: make(map[string]*workerExecutionLease)}
	defer c.releaseWorkerExecution("worker")
	err = c.repairInterruptedHarnessTaskWithAcquire(task, time.Now(), func(id string) (bool, error) {
		acquired, err := c.acquireWorkerExecution(id)
		if err != nil || !acquired {
			t.Fatalf("concurrent worker admission: %v %v", acquired, err)
		}
		return c.acquireWorkerExecution(id)
	})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := hs.ListRuns()
	if err != nil || len(runs) != 1 || runs[0].Status != harness.TaskStatusRunning {
		t.Fatalf("active worker projection changed: %+v %v", runs, err)
	}
}

func TestInterruptedRepairKeepsTerminalChildRegistryState(t *testing.T) {
	dir := t.TempDir()
	docs, err := persistence.Open(context.Background(), filepath.Join(dir, "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store := agentthread.NewDocumentStore(docs, "thread")
	meta := agentthread.Metadata{ID: "worker", SessionID: "thread", Path: "/root/worker", Status: agentthread.StatusCompleted}
	if err := store.UpsertThread(meta); err != nil {
		t.Fatal(err)
	}
	registry := agentthread.NewRegistry()
	if err := registry.Restore(meta); err != nil {
		t.Fatal(err)
	}
	c := &AgentControl{threads: registry, threadStore: store}
	if err := c.markAgentThreadInterrupted("worker", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, ok := registry.Resolve("worker")
	if !ok || got.Status != agentthread.StatusCompleted {
		t.Fatalf("completed child was changed: %+v", got)
	}
}

type failFirstChildStatus struct {
	persistence.Documents
	fail        atomic.Bool
	afterCommit bool
}

func (d *failFirstChildStatus) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if len(tx.Writes) > 0 && tx.Writes[0].Namespace == agentthread.DocumentNamespace && bytes.Contains(tx.Writes[0].Value, []byte(`"failed"`)) && d.fail.CompareAndSwap(true, false) {
		if d.afterCommit {
			if _, err := d.Documents.Commit(ctx, tx); err != nil {
				return persistence.Receipt{}, err
			}
		}
		return persistence.Receipt{}, errors.New("child storage unavailable")
	}
	return d.Documents.Commit(ctx, tx)
}

func TestInterruptedChildWriteCanRetryOnSameControl(t *testing.T) {
	for _, mode := range []string{"before-write", "lost-reply", "terminal-wins"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			raw, err := persistence.Open(context.Background(), filepath.Join(dir, "docs"), "sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			store := agentthread.NewDocumentStore(raw, "thread")
			meta := agentthread.Metadata{ID: "worker", SessionID: "thread", Path: "/root/worker", Status: agentthread.StatusRunning}
			if err := store.UpsertThread(meta); err != nil {
				t.Fatal(err)
			}
			registry := agentthread.NewRegistry()
			if err := registry.Restore(meta); err != nil {
				t.Fatal(err)
			}
			docs := &failFirstChildStatus{Documents: raw, afterCommit: mode == "lost-reply"}
			docs.fail.Store(true)
			c := &AgentControl{threads: registry, threadStore: agentthread.NewDocumentStore(docs, "thread")}
			if err := c.markAgentThreadInterrupted("worker", time.Now()); err == nil {
				t.Fatal("first write must fail")
			}
			if mode == "terminal-wins" {
				meta.Status = agentthread.StatusCompleted
				if err := store.UpsertThread(meta); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.markAgentThreadInterrupted("worker", time.Now()); err != nil {
				t.Fatal(err)
			}
			rows, err := store.ListThreads()
			want := agentthread.StatusFailed
			if mode == "terminal-wins" {
				want = agentthread.StatusCompleted
			}
			if err != nil || len(rows) != 1 || rows[0].Status != want {
				t.Fatalf("retry left stale child: %+v %v", rows, err)
			}
			if mode != "terminal-wins" {
				events, err := store.ReadEvents()
				var changes int
				for _, event := range events {
					if event.Type == agentthread.EventStatusChange && event.Status == agentthread.StatusFailed {
						changes++
					}
				}
				if err != nil || changes != 1 {
					t.Fatalf("child status events: %+v %v", events, err)
				}
			}
		})
	}
}
