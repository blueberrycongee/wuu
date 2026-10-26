package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func TestDocumentStoreMigratesAndIsolatesQueue(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	legacyDir := t.TempDir()
	legacy := NewStore(legacyDir)
	if err := legacy.UpsertTask(Task{ID: "task"}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.UpsertQueueItem(QueueItem{ID: "queued", Kind: "agent_spawn"}); err != nil {
		t.Fatal(err)
	}
	store := NewDocumentStore(docs, "session", legacyDir)
	tasks, err := store.ListTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("migration: %+v %v", tasks, err)
	}
	claimed, err := store.ClaimQueueItem("queued")
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	reopened := NewDocumentStore(docs, "session", legacyDir)
	claimed, err = reopened.ClaimQueueItem("queued")
	if err != nil || claimed {
		t.Fatalf("claim replay: %v %v", claimed, err)
	}
	if err := store.UpsertQueueItem(QueueItem{ID: "cancelling", Kind: "agent_spawn"}); err != nil {
		t.Fatal(err)
	}
	marked, err := reopened.MarkQueueItemCancelling("cancelling")
	if err != nil || !marked {
		t.Fatalf("mark cancellation: %v %v", marked, err)
	}
	item, found, err := store.GetQueueItem("cancelling")
	if err != nil || !found || item.State != QueueItemStateCancelling {
		t.Fatalf("selected queue cancellation not durable: %+v %v", item, err)
	}

	other := NewDocumentStore(docs, "other", t.TempDir())
	tasks, err = other.ListTasks()
	if err != nil || len(tasks) != 0 {
		t.Fatalf("scope leaked: %+v %v", tasks, err)
	}
}

type pausedReportCommit struct {
	persistence.Documents
	committed chan struct{}
	resume    chan struct{}
	once      sync.Once
}

func (d *pausedReportCommit) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil {
		d.once.Do(func() { close(d.committed); <-d.resume })
	}
	return receipt, err
}
func TestDocumentReportArtifactCannotOverwriteNewerCommit(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	dir := t.TempDir()
	current := NewDocumentStore(docs, "session", dir)
	if _, err := current.ListReports(); err != nil {
		t.Fatal(err)
	}
	paused := &pausedReportCommit{Documents: docs, committed: make(chan struct{}), resume: make(chan struct{})}
	old := NewDocumentStore(paused, "session", dir)
	done := make(chan error, 1)
	go func() {
		_, err := old.SubmitReport(Report{ID: "old", TaskID: "task", Kind: ReportKindFinalText, Summary: "obsolete"})
		done <- err
	}()
	<-paused.committed
	latest, err := current.SubmitReport(Report{ID: "new", TaskID: "task", Kind: ReportKindStructured, Summary: "authoritative"})
	if err != nil {
		close(paused.resume)
		t.Fatal(err)
	}
	close(paused.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(latest.ReportPath)
	if err != nil || !strings.Contains(string(raw), "authoritative") {
		t.Fatalf("stale post-commit artifact: %s %v", raw, err)
	}
}
