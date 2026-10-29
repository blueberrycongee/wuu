package agentcontrol

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type lostRootReply struct {
	persistence.Documents
	fail atomic.Bool
}

func (d *lostRootReply) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil && len(tx.Writes) > 0 && tx.Writes[0].Namespace == agentthread.DocumentNamespace && bytes.Contains(tx.Writes[0].Value, []byte(`"root"`)) && d.fail.CompareAndSwap(true, false) {
		return persistence.Receipt{}, errors.New("lost root write acknowledgement")
	}
	return receipt, err
}

func TestRootRecoveryPreservesDurableRegistrationAfterLostReply(t *testing.T) {
	dir := t.TempDir()
	raw, err := persistence.Open(context.Background(), filepath.Join(dir, "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	docs := &lostRootReply{Documents: raw}
	cfg := Config{Documents: docs, Client: &fakeClient{resp: providers.ChatResponse{Content: "done"}}, SessionID: "thread", ParentRepo: dir, ThreadDir: filepath.Join(dir, "threads"), HarnessDir: filepath.Join(dir, "harness"), WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }}
	docs.fail.Store(true)
	c, err := New(cfg)
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("root registration with unknown outcome must fail")
	}
	var uncertain *persistence.CommitError
	if !errors.As(err, &uncertain) {
		t.Fatalf("lost acknowledgement did not preserve its transaction: %v", err)
	}
	before, err := agentthread.NewDocumentStore(raw, "thread").ListThreads()
	if err != nil || len(before) != 1 {
		t.Fatalf("durable root after lost reply: %+v %v", before, err)
	}
	eventsBefore, err := agentthread.NewDocumentStore(raw, "thread").ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	retry, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	after, err := agentthread.NewDocumentStore(raw, "thread").ListThreads()
	if err != nil || len(after) != 1 {
		t.Fatalf("root after retry: %+v %v", after, err)
	}
	if !before[0].CreatedAt.Equal(after[0].CreatedAt) || !before[0].UpdatedAt.Equal(after[0].UpdatedAt) {
		t.Fatalf("root registration rewritten: before=%+v after=%+v", before[0], after[0])
	}
	eventsAfter, err := agentthread.NewDocumentStore(raw, "thread").ReadEvents()
	if err != nil || len(eventsAfter) != len(eventsBefore) {
		t.Fatalf("recovery duplicated root event: before=%d after=%d err=%v", len(eventsBefore), len(eventsAfter), err)
	}
}

func TestRootRecoveryAfterLostReplyAndProcessExit(t *testing.T) {
	if dir := os.Getenv("WUU_ROOT_RECOVERY_CHILD"); dir != "" {
		raw, err := persistence.Open(context.Background(), filepath.Join(dir, "documents"), "sqlite")
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		docs := &lostRootReply{Documents: raw}
		docs.fail.Store(true)
		cfg := Config{Documents: docs, Client: &fakeClient{resp: providers.ChatResponse{Content: "done"}}, SessionID: "thread", ParentRepo: dir, ThreadDir: filepath.Join(dir, "threads"), HarnessDir: filepath.Join(dir, "harness"), WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }}
		control, err := New(cfg)
		if control != nil {
			control.Close()
		}
		if err == nil {
			t.Fatal("missing uncertain outcome")
		}
		return
	}
	dir := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestRootRecoveryAfterLostReplyAndProcessExit$")
	child.Env = append(os.Environ(), "WUU_ROOT_RECOVERY_CHILD="+dir)
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("first owner: %v\n%s", err, out)
	}
	raw, err := persistence.Open(context.Background(), filepath.Join(dir, "documents"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	store := agentthread.NewDocumentStore(raw, "thread")
	before, err := store.ListThreads()
	if err != nil || len(before) != 1 {
		t.Fatalf("persisted root: %+v %v", before, err)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Documents: raw, Client: &fakeClient{resp: providers.ChatResponse{Content: "done"}}, SessionID: "thread", ParentRepo: dir, ThreadDir: filepath.Join(dir, "threads"), HarnessDir: filepath.Join(dir, "harness"), WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }}
	control, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	after, err := store.ListThreads()
	if err != nil || len(after) != 1 || !before[0].CreatedAt.Equal(after[0].CreatedAt) || !before[0].UpdatedAt.Equal(after[0].UpdatedAt) {
		t.Fatalf("reopened root before=%+v after=%+v error=%v", before, after, err)
	}
	updated, err := store.ReadEvents()
	if err != nil || len(updated) != len(events) {
		t.Fatalf("reopened root events before=%d after=%d error=%v", len(events), len(updated), err)
	}
}

func TestRootRecoveryPreservesCommittedWorkspaceAfterLostReply(t *testing.T) {
	dir := t.TempDir()
	raw, err := persistence.Open(context.Background(), filepath.Join(dir, "documents"), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	docs := &lostRootReply{Documents: raw}
	cfg := Config{Documents: docs, Client: &fakeClient{resp: providers.ChatResponse{Content: "done"}}, SessionID: "thread", ParentRepo: dir, ThreadDir: filepath.Join(dir, "threads"), HarnessDir: filepath.Join(dir, "harness"), WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }}
	first, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	store := agentthread.NewDocumentStore(raw, "thread")
	before, err := store.ListThreads()
	if err != nil || len(before) != 1 {
		t.Fatalf("initial root: %+v %v", before, err)
	}
	cfg.ParentRepo = filepath.Join(dir, "new-workspace")
	docs.fail.Store(true)
	failed, err := New(cfg)
	if failed != nil {
		failed.Close()
	}
	if err == nil {
		t.Fatal("workspace update hid lost acknowledgement")
	}
	changed, err := store.ListThreads()
	if err != nil || len(changed) != 1 {
		t.Fatalf("committed root: %+v %v", changed, err)
	}
	if changed[0].CWD != cfg.ParentRepo || !changed[0].CreatedAt.Equal(before[0].CreatedAt) {
		t.Fatalf("workspace update replaced root: %+v", changed[0])
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatal(err)
	}
	retry, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer retry.Close()
	after, err := store.ListThreads()
	if err != nil || len(after) != 1 || !changed[0].UpdatedAt.Equal(after[0].UpdatedAt) {
		t.Fatalf("workspace retry rewrote root: %+v %v", after, err)
	}
	repeated, err := store.ReadEvents()
	if err != nil || len(events) != len(repeated) {
		t.Fatalf("workspace retry duplicated event: %d -> %d (%v)", len(events), len(repeated), err)
	}
}
