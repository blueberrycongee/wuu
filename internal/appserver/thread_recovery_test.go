package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

func TestServerThreadListReconcilesTerminalWorkerSnapshot(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "current-workspace"
		if foreign {
			name = "previous-workspace"
		}
		t.Run(name, func(t *testing.T) { testThreadListReconcilesTerminalWorkerSnapshot(t, foreign) })
	}
}

func testThreadListReconcilesTerminalWorkerSnapshot(t *testing.T, foreign bool) {
	t.Helper()
	rt := newTestRuntime(t, &fakeClient{})
	rt.StateDir = filepath.Join(rt.RootDir, ".wuu", "state")
	artifactStateDir := rt.StateDir
	if foreign {
		rt.WuuHome = t.TempDir()
		rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
		rt.StateDir = filepath.Join(rt.WuuHome, "workspaces", "current")
		artifactStateDir = filepath.Join(rt.WuuHome, "workspaces", "previous")
	}
	rootID := "root-with-interrupted-child"
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if err := rt.Sessions.UpdateIndex(rootID, 2, "root summary"); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Date(2026, 7, 29, 1, 53, 10, 0, time.UTC)
	completedAt := startedAt.Add(6 * time.Minute)
	artifactDir := statepath.SessionArtifactDir(artifactStateDir, rootID)
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
	// Reconciliation migrates legacy snapshots into the selected document store;
	// the retired file store is not the write authority.
	selected := agentthread.NewDocumentStore(testRuntimeState(t, rt).Documents, rootID, store.Dir())
	threads, err := selected.ListThreads()
	if err != nil {
		t.Fatalf("list reconciled thread store: %v", err)
	}
	if len(threads) != 1 || threads[0].Status != agentthread.StatusCancelled || !threads[0].UpdatedAt.Equal(completedAt) {
		t.Fatalf("persisted child status was not reconciled: %+v", threads)
	}
}

func TestThreadListsUseMigratedAgentDataWithoutInspectingLegacyRoots(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
	rt.StateDir = filepath.Join(rt.WuuHome, "workspaces", "current")
	if err := os.MkdirAll(rt.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binding := testRuntimeState(t, rt)
	for _, id := range []string{"migrated-empty", "migrated-child", "migrated-archived"} {
		if _, err := binding.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		store := agentthread.NewDocumentStore(binding.Documents, id)
		if id == "migrated-child" {
			if err := store.UpsertThread(agentthread.Metadata{
				ID: "completed-child", SessionID: id, ParentID: id,
				Path: "/root/child", Status: agentthread.StatusCompleted,
				Source: agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentPath: agentthread.RootPath},
			}); err != nil {
				t.Fatal(err)
			}
		} else if _, err := store.ListThreads(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := binding.Sessions.UpdateArchived("migrated-archived", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(rt.WuuHome, "workspaces", "unrelated-link")); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		method string
		want   int
	}{
		{method: "thread/list", want: 2},
		{method: "thread/listAll", want: 2},
		{method: "thread/listArchived", want: 1},
	} {
		dispatchPayload(t, srv, tc.method, tc.method, ThreadListParams{SummaryOnly: true})
		response := responseByID(t, parseOutput(t, out.String()), tc.method)
		if response["error"] != nil {
			t.Fatalf("%s failed for already migrated sessions: %v", tc.method, response["error"])
		}
		list := remarshal[ThreadListResult](t, response["result"])
		if len(list.Threads) != tc.want {
			t.Fatalf("%s returned %d threads, want %d", tc.method, len(list.Threads), tc.want)
		}
		for _, thread := range list.Threads {
			if thread.ID == "migrated-child" && (len(thread.ChildAgents) != 1 || thread.ChildAgents[0].Status != string(agentthread.StatusCompleted)) {
				t.Fatalf("%s lost the completed child: %+v", tc.method, thread.ChildAgents)
			}
		}
	}
}

func TestThreadListImportsAgentDataFromAnotherWorkspace(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
	rt.StateDir = filepath.Join(rt.WuuHome, "workspaces", "current")
	rootID := "legacy-root"
	if _, err := rt.Sessions.CreateWithMetadata(rootID, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(rt.WuuHome, "workspaces", "previous")
	legacy := agentthread.NewStore(filepath.Join(statepath.SessionArtifactDir(foreign, rootID), "threads"))
	if err := legacy.UpsertThread(agentthread.Metadata{
		ID: "legacy-child", SessionID: rootID, ParentID: rootID,
		Path: "/root/legacy", Status: agentthread.StatusCompleted,
		Source: agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentPath: agentthread.RootPath},
	}); err != nil {
		t.Fatal(err)
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	dispatchPayload(t, srv, "legacy", "thread/list", ThreadListParams{SummaryOnly: true})
	response := responseByID(t, parseOutput(t, out.String()), "legacy")
	if response["error"] != nil {
		t.Fatalf("legacy list failed: %v", response["error"])
	}
	list := remarshal[ThreadListResult](t, response["result"])
	if len(list.Threads) != 1 || len(list.Threads[0].ChildAgents) != 1 || list.Threads[0].ChildAgents[0].ID != "legacy-child" {
		t.Fatalf("legacy child was not imported: %+v", list.Threads)
	}
}

func TestUnmigratedAgentDataRejectsUnsafeLegacyRoots(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WuuHome = t.TempDir()
	rt.SessionDir = statepath.SessionsDir(rt.WuuHome)
	rt.StateDir = filepath.Join(rt.WuuHome, "workspaces", "current")
	if err := os.MkdirAll(rt.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(rt.WuuHome, "workspaces", "unsafe-link")); err != nil {
		t.Fatal(err)
	}
	srv := New(rt, &lockedBuffer{})
	t.Cleanup(srv.Close)
	store, err := srv.agentThreadStore(testRuntimeState(t, rt), "unmigrated")
	if err == nil {
		_, err = store.ListThreads()
	}
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("unsafe legacy discovery was not rejected: %v", err)
	}
}
