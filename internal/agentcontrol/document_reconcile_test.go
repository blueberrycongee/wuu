package agentcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type failedWorkerSnapshotDocuments struct {
	persistence.Documents
	failure error
}

func (d failedWorkerSnapshotDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if q.Namespace == subagent.HistoryDocumentNamespace {
		return persistence.View{}, d.failure
	}
	return d.Documents.Read(ctx, q)
}

func TestReconcileSelectedWorkerReadFailurePreservesTerminalState(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"id":"worker","status":"completed","result":"keep me"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := subagent.LoadPersistedRunFromDocuments(docs, "root", "worker", path); err != nil {
		t.Fatal(err)
	}
	tasks := harness.NewDocumentStore(docs, "root")
	task := harness.Task{ID: "worker", Status: harness.TaskStatusRunning}
	if err := tasks.UpsertTask(task); err != nil {
		t.Fatal(err)
	}
	threads := agentthread.NewDocumentStore(docs, "root")
	if err := threads.UpsertThread(agentthread.Metadata{ID: "worker", Status: agentthread.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("selected worker snapshot read unavailable")
	c := &AgentControl{documents: failedWorkerSnapshotDocuments{Documents: docs, failure: failure}, sessionID: "root", historyDir: dir, harnessDir: filepath.Join(dir, "harness"), harnessStore: tasks, threadStore: threads}
	if err := c.reconcileUnownedHarnessTask(task, time.Now()); !errors.Is(err, failure) {
		t.Fatalf("read failure was treated as missing snapshot: %v", err)
	}
	stored, err := tasks.ListTasks()
	if err != nil || len(stored) != 1 || stored[0].Status != harness.TaskStatusRunning {
		t.Fatalf("rewrote task after failed read: %+v %v", stored, err)
	}
	childRows, err := threads.ListThreads()
	if err != nil || len(childRows) != 1 || childRows[0].Status != agentthread.StatusCompleted {
		t.Fatalf("rewrote child after failed read: %+v %v", childRows, err)
	}
	run, err := subagent.LoadPersistedRunFromDocuments(docs, "root", "worker")
	if err != nil || run.Status != subagent.StatusCompleted || run.Result != "keep me" {
		t.Fatalf("lost terminal worker: %+v %v", run, err)
	}
}
