package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestServerThreadListReconcilesTerminalWorkerSnapshot(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu", "state")
	rootID := "root-with-interrupted-child"
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.UpdateIndex(rootID, 2, "root summary"); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Date(2026, 7, 29, 1, 53, 10, 0, time.UTC)
	completedAt := startedAt.Add(6 * time.Minute)
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, rootID)
	store := agentthread.NewStore(filepath.Join(artifactDir, "threads"))
	meta := agentthread.Metadata{
		ID:        "worker-interrupted",
		SessionID: rootID,
		ParentID:  rootID,
		Path:      "/root/inspect",
		TaskName:  "inspect",
		Role:      agentcontrol.DefaultSubagentType,
		Status:    agentthread.StatusRunning,
		CreatedAt: startedAt,
		UpdatedAt: startedAt,
		Source: agentthread.Source{
			Kind:           agentthread.SourceThreadSpawn,
			ParentThreadID: rootID,
			ParentPath:     agentthread.RootPath,
			Depth:          2,
		},
	}
	if err := store.UpsertThread(meta); err != nil {
		t.Fatalf("upsert worker thread: %v", err)
	}
	workerDir := filepath.Join(artifactDir, "workers")
	if err := os.MkdirAll(workerDir, 0o755); err != nil {
		t.Fatalf("mkdir worker history: %v", err)
	}
	data, err := json.MarshalIndent(persistedAgentHistory{
		ID:          meta.ID,
		Type:        meta.Role,
		TaskName:    meta.TaskName,
		AgentPath:   meta.Path,
		ParentID:    rootID,
		Description: meta.TaskName,
		Status:      string(subagent.StatusCancelled),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal worker history: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerDir, meta.ID+".json"), data, 0o644); err != nil {
		t.Fatalf("write worker history: %v", err)
	}

	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list: %v", err)
	}

	result := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if len(result.Threads) != 1 || len(result.Threads[0].ChildAgents) != 1 {
		t.Fatalf("unexpected thread list: %+v", result.Threads)
	}
	if status := result.Threads[0].ChildAgents[0].Status; status != string(agentthread.StatusCancelled) {
		t.Fatalf("child status = %q, want cancelled", status)
	}
	threads, err := store.ListThreads()
	if err != nil {
		t.Fatalf("list reconciled thread store: %v", err)
	}
	if len(threads) != 1 || threads[0].Status != agentthread.StatusCancelled || !threads[0].UpdatedAt.Equal(completedAt) {
		t.Fatalf("persisted child status was not reconciled: %+v", threads)
	}
}
