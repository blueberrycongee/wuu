package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestGeneratedReportsPreserveGenerationsInDocumentStore(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	dir := t.TempDir()
	store := NewDocumentStore(docs, "session", dir)
	first := Report{ID: "gen-first", TaskID: "task", Kind: ReportKindFinalText, Summary: "first outcome", ReportPath: filepath.Join(dir, "reports", "task", "first", "report.md"), SubmittedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	next := Report{ID: "gen-next", TaskID: "task", Kind: ReportKindFinalText, Summary: "followup outcome", ReportPath: filepath.Join(dir, "reports", "task", "next", "report.md"), SubmittedAt: first.SubmittedAt.Add(time.Second)}
	if _, err := store.SubmitGeneratedReport(first); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitGeneratedReport(next); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitGeneratedReport(first); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.ReportForTask("task")
	if err != nil || !ok || latest.ID != next.ID {
		t.Fatalf("latest=%+v ok=%v err=%v", latest, ok, err)
	}
	a, err := os.ReadFile(first.ReportPath)
	if err != nil || string(a) != string(before) {
		t.Fatalf("first report overwritten: %q %v", a, err)
	}
	b, err := os.ReadFile(next.ReportPath)
	if err != nil || !strings.Contains(string(b), "followup outcome") {
		t.Fatalf("next report=%q %v", b, err)
	}
	explicit, err := store.SubmitReport(Report{ID: "explicit", TaskID: "task", Kind: ReportKindStructured, Summary: "authored", ReportPath: filepath.Join(dir, "reports", "authored.md")})
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.SubmitGeneratedReport(Report{ID: "gen-late", TaskID: "task", Kind: ReportKindFinalText, Summary: "late", ReportPath: filepath.Join(dir, "reports", "task", "late", "report.md")})
	if err != nil || after.ID != explicit.ID {
		t.Fatalf("generated displaced structured: %+v %v", after, err)
	}
}

func TestGeneratedReportDoesNotOverwriteExistingPath(t *testing.T) {
	for _, document := range []bool{false, true} {
		t.Run(fmt.Sprintf("document_%t", document), func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(dir)
			if document {
				docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
				if err != nil {
					t.Fatal(err)
				}
				defer docs.Close()
				store = NewDocumentStore(docs, "session", dir)
			}
			path := filepath.Join(dir, "reports", "existing.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("keep existing"), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := store.SubmitGeneratedReport(Report{ID: "gen", TaskID: "task", Kind: ReportKindFinalText, Summary: "other", ReportPath: path})
			if err == nil {
				t.Error("generated report accepted conflicting existing path")
			}
			if _, found, queryErr := store.ReportForTask("task"); queryErr != nil || found {
				t.Errorf("conflicting report became authoritative: found=%v err=%v", found, queryErr)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != "keep existing" {
				t.Fatalf("existing report changed: %q %v", data, readErr)
			}
		})
	}
}

func TestGeneratedReportCannotDisplaceConcurrentStructuredReport(t *testing.T) {
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
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
	oldPath := filepath.Join(dir, "reports", "task", "gen", "report.md")
	done := make(chan error, 1)
	go func() {
		_, err := old.SubmitGeneratedReport(Report{ID: "gen", TaskID: "task", Kind: ReportKindFinalText, Summary: "obsolete", ReportPath: oldPath})
		done <- err
	}()
	<-paused.committed
	authoritative, err := current.SubmitReport(Report{ID: "explicit", TaskID: "task", Kind: ReportKindStructured, Summary: "authoritative"})
	if err != nil {
		close(paused.resume)
		t.Fatal(err)
	}
	close(paused.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	latest, ok, err := current.ReportForTask("task")
	if err != nil || !ok || latest.ID != authoritative.ID {
		t.Fatalf("latest=%+v ok=%v err=%v", latest, ok, err)
	}
	data, err := os.ReadFile(authoritative.ReportPath)
	if err != nil || !strings.Contains(string(data), "authoritative") {
		t.Fatalf("structured report=%q err=%v", data, err)
	}
}

func TestGeneratedReportOutOfOrderReplayKeepsLatestTaskPath(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.UpsertTask(Task{ID: "task"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := Report{ID: "old", TaskID: "task", Kind: ReportKindFinalText, Summary: "old", ReportPath: filepath.Join(dir, "old.md"), SubmittedAt: now}
	next := Report{ID: "next", TaskID: "task", Kind: ReportKindFinalText, Summary: "next", ReportPath: filepath.Join(dir, "next.md"), SubmittedAt: now.Add(time.Second)}
	if _, err := store.SubmitGeneratedReport(next); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SubmitGeneratedReport(old); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.ReportForTask("task")
	if err != nil || !ok || latest.ID != next.ID {
		t.Fatalf("latest=%+v ok=%v err=%v", latest, ok, err)
	}
	tasks, err := store.ListTasks()
	if err != nil || len(tasks) != 1 || tasks[0].ReportPath != next.ReportPath {
		t.Fatalf("task report path=%+v err=%v", tasks, err)
	}
}

func TestStructuredReportPreservesLegacyGeneratedReference(t *testing.T) {
	for _, document := range []bool{false, true} {
		t.Run(fmt.Sprintf("document_%t", document), func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(dir)
			if document {
				docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
				if err != nil {
					t.Fatal(err)
				}
				defer docs.Close()
				store = NewDocumentStore(docs, "session", dir)
			}
			legacy, err := store.SubmitReport(Report{ID: "legacy", TaskID: "task", Kind: ReportKindFinalText, Summary: "old result"})
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(legacy.ReportPath)
			if err != nil {
				t.Fatal(err)
			}
			explicit, err := store.SubmitReport(Report{ID: "authored", TaskID: "task", Kind: ReportKindStructured, Summary: "authored result"})
			if err != nil {
				t.Fatal(err)
			}
			if explicit.ReportPath == legacy.ReportPath {
				t.Fatalf("structured overwrote legacy reference: %q", explicit.ReportPath)
			}
			after, err := os.ReadFile(legacy.ReportPath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("legacy report changed: %q %v", after, err)
			}
			latest, ok, err := store.ReportForTask("task")
			if err != nil || !ok || latest.ID != explicit.ID {
				t.Fatalf("latest=%+v ok=%v err=%v", latest, ok, err)
			}
		})
	}
}

func TestLegacyGeneratedReportMigrationKeepsOldAndNewReferences(t *testing.T) {
	dir := t.TempDir()
	legacy := NewStore(dir)
	original, err := legacy.SubmitReport(Report{ID: "legacy", TaskID: "task", Kind: ReportKindFinalText, Summary: "legacy outcome", SubmittedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(original.ReportPath)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	store := NewDocumentStore(docs, "session", dir)
	followup := Report{ID: "new-gen", TaskID: "task", Kind: ReportKindFinalText, Summary: "new outcome", ReportPath: filepath.Join(dir, "reports", "task", "next", "report.md"), SubmittedAt: original.SubmittedAt.Add(time.Second)}
	if _, err := store.SubmitGeneratedReport(followup); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.ReportForTask("task")
	if err != nil || !ok || latest.ID != followup.ID {
		t.Fatalf("migrated latest=%+v ok=%v err=%v", latest, ok, err)
	}
	explicit, err := store.SubmitReport(Report{ID: "explicit", TaskID: "task", Kind: ReportKindStructured, Summary: "authored"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(original.ReportPath)
	if err != nil || string(after) != string(oldBytes) {
		t.Fatalf("legacy reference changed: %q %v", after, err)
	}
	latest, ok, err = store.ReportForTask("task")
	if err != nil || !ok || latest.ID != explicit.ID {
		t.Fatalf("migrated structured latest=%+v ok=%v err=%v", latest, ok, err)
	}
	nextBytes, err := os.ReadFile(followup.ReportPath)
	if err != nil || !strings.Contains(string(nextBytes), "new outcome") {
		t.Fatalf("followup reference lost: %q %v", nextBytes, err)
	}
}

func TestGeneratedReportRetryRebuildsMissingProjection(t *testing.T) {
	for _, document := range []bool{false, true} {
		t.Run(fmt.Sprintf("document_%t", document), func(t *testing.T) {
			dir := t.TempDir()
			store := NewStore(dir)
			if document {
				docs, err := persistence.Open(context.Background(), filepath.Join(t.TempDir(), "docs"), "sqlite")
				if err != nil {
					t.Fatal(err)
				}
				defer docs.Close()
				store = NewDocumentStore(docs, "session", dir)
			}
			report := Report{ID: "generated", TaskID: "task", Kind: ReportKindFinalText, Summary: "recoverable", ReportPath: filepath.Join(dir, "reports", "generation", "report.md"), SubmittedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			if _, err := store.SubmitGeneratedReport(report); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(report.ReportPath); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SubmitGeneratedReport(report); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(report.ReportPath)
			if err != nil || !strings.Contains(string(data), "recoverable") {
				t.Fatalf("missing projection not restored: %q %v", data, err)
			}
		})
	}
}
