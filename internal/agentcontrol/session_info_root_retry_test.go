package agentcontrol

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"path/filepath"
	"testing"
	"time"
)

func TestSetSessionInfoDoesNotDuplicateQueueAfterRootFailure(t *testing.T) {
	dir := t.TempDir()
	raw, err := persistence.Open(context.Background(), filepath.Join(dir, "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	docs := &lostRootReply{Documents: raw}
	c, err := New(Config{Documents: docs, Client: &fakeClient{resp: providers.ChatResponse{Content: "done"}}, SessionID: "session-pending", ParentRepo: dir, WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hdir := filepath.Join(dir, "session", "harness")
	now := time.Now().UTC()
	id := "worker_a"
	meta := agentthread.Metadata{ID: id, SessionID: "thread", TaskName: id, Role: DefaultSubagentType, ParentID: "root", Path: "/root/" + id, Status: agentthread.StatusPending, CreatedAt: now, UpdatedAt: now, Source: agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentPath: agentthread.RootPath}}
	payload, err := json.Marshal(queuedSpawnPayload{WorkerID: id, WorkerType: DefaultSubagentType, ThreadMeta: meta, Prompt: "do", Isolation: string(IsolationInplace)})
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.NewDocumentStore(raw, "thread", hdir).UpsertQueueItem(harness.QueueItem{ID: id, TaskID: id, Kind: "agent_spawn", Payload: payload, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	docs.fail.Store(true)
	if err := c.SetSessionInfo("thread", filepath.Join(dir, "session", "workers"), filepath.Join(dir, "session", "threads")); err == nil {
		t.Fatal("expected failure")
	}
	if err := c.SetSessionInfo("thread", filepath.Join(dir, "session", "workers"), filepath.Join(dir, "session", "threads")); err != nil {
		t.Fatal(err)
	}
	c.queueMu.Lock()
	n := len(c.queued)
	c.queueMu.Unlock()
	if n != 1 {
		t.Fatalf("retry loaded %d copies of one durable queue item", n)
	}
}
