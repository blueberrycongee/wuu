package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Exercise real persistence and file tools across a cold project relocation.
func TestRelocatedProjectUsesNewDirectory(t *testing.T) {
	for _, name := range []string{"old_directory_missing", "old_directory_reused_by_other_project", "custom_directory", "linked_worktree"} {
		reuseOldPath := name == "old_directory_reused_by_other_project"
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			oldRoot, newRoot := filepath.Join(base, "original"), filepath.Join(base, "relocated")
			wuuHome := filepath.Join(base, "wuu-home")
			for _, directory := range []string{oldRoot, wuuHome} {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("WUU_HOME", wuuHome)
			t.Setenv("CODEX_HOME", filepath.Join(base, "codex-home"))
			writeRegistry := func(root string) {
				data, err := json.Marshal(map[string]any{
					"projects":       []map[string]any{{"id": "audit-project", "name": "Audit project", "path": root, "previous_paths": []string{oldRoot}}},
					"active_context": map[string]string{"kind": "project", "project_id": "audit-project", "cwd": root},
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(wuuHome, "projects.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			makeRuntime := func(root string, client *fakeClient) *runtime.Session {
				rt := newTestRuntime(t, client)
				rt.RootDir, rt.WorkspaceID, rt.WuuHome = root, "audit-project", wuuHome
				rt.SessionDir = filepath.Join(wuuHome, "sessions")
				rt.StateDir = filepath.Join(wuuHome, "state")
				kit, err := tools.New(root)
				if err != nil {
					t.Fatal(err)
				}
				kit.SetStateDir(rt.StateDir)
				rt.Toolkit, rt.StreamRunner.Tools = kit, kit
				return rt
			}
			writeRegistry(oldRoot)
			outBefore := &lockedBuffer{}
			before := New(makeRuntime(oldRoot, &fakeClient{response: providersResponse("Initial conversation before move")}), outBefore)
			if err := before.handleLine(context.Background(), []byte(`{"id":"start","method":"thread/start"}`)); err != nil {
				t.Fatal(err)
			}
			created := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, outBefore.String()), "start")["result"]).Thread
			if created.ID == "" {
				t.Fatalf("creation failed: %s", outBefore.String())
			}
			initialTurn, _ := json.Marshal(map[string]any{"id": "initial-turn", "method": "turn/start", "params": TurnStartParams{ThreadID: created.ID, Prompt: "Start a conversation before moving this project"}})
			if err := before.handleLine(context.Background(), initialTurn); err != nil {
				t.Fatal(err)
			}
			waitForMethod(t, outBefore, NotificationTurnCompleted)
			before.Close()
			expectedRoot := newRoot
			if name == "custom_directory" || name == "linked_worktree" {
				expectedRoot = t.TempDir()
				worktreePath, baseRepo := "", ""
				if name == "linked_worktree" {
					worktreePath, baseRepo = expectedRoot, oldRoot
				}
				if _, err := session.UpdateWorkspaceBinding(filepath.Join(wuuHome, "sessions"), created.ID, expectedRoot, worktreePath, "", baseRepo); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Rename(oldRoot, newRoot); err != nil {
				t.Fatal(err)
			}
			if reuseOldPath {
				if err := os.Mkdir(oldRoot, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(oldRoot, "unrelated-project.txt"), []byte("other project"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// This is the projects.json mutation performed by ProjectManager.relocate.
			// The independent TypeScript probe invokes that method directly.
			writeRegistry(newRoot)
			client := &fakeClient{responses: []providers.ChatResponse{
				{ToolCalls: []providers.ToolCall{{ID: "audit-write", Name: "write_file", Arguments: `{"path":"audit-written.txt","content":"written by resumed thread"}`}}},
				{Content: "done"},
			}}
			outAfter := &lockedBuffer{}
			after := New(makeRuntime(newRoot, client), outAfter)
			t.Cleanup(after.Close)
			call := func(id, method string, params any) {
				raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				if err := after.handleLine(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
			}
			call("list", "thread/list", map[string]any{})
			listed := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, outAfter.String()), "list")["result"])
			if len(listed.Threads) != 1 || listed.Threads[0].ID != created.ID {
				t.Fatalf("relocated project did not list old session: %+v", listed)
			}
			call("resume", "thread/resume", map[string]string{"session_id": created.ID})
			resumed := remarshal[ThreadResumeResult](t, responseByID(t, parseOutput(t, outAfter.String()), "resume")["result"])
			if resumed.Thread.CWD != expectedRoot || listed.Threads[0].CWD != expectedRoot {
				t.Fatalf("wrong relocated read binding: resume=%q list=%q want=%q", resumed.Thread.CWD, listed.Threads[0].CWD, expectedRoot)
			}
			t.Logf("registered_root=%q session_id=%q resumed_cwd=%q workspace_id=%q", newRoot, created.ID, resumed.Thread.CWD, resumed.Thread.WorkspaceID)
			call("turn", "turn/start", TurnStartParams{ThreadID: created.ID, Prompt: "Write audit-written.txt in this relocated project"})
			response := responseByID(t, parseOutput(t, outAfter.String()), "turn")
			if response["error"] != nil {
				t.Fatalf("turn rejected: %v", response)
			}
			waitForMethod(t, outAfter, NotificationTurnCompleted)
			t.Logf("rpc_evidence=%s", outAfter.String())
			oldData, oldErr := os.ReadFile(filepath.Join(oldRoot, "audit-written.txt"))
			newData, newErr := os.ReadFile(filepath.Join(expectedRoot, "audit-written.txt"))
			t.Logf("old_path_written=%v old_content=%q new_path_written=%v new_content=%q", oldErr == nil, oldData, newErr == nil, newData)
			persisted, ok, err := session.Find(filepath.Join(wuuHome, "sessions"), created.ID)
			if err != nil || !ok || persisted.CWD != expectedRoot {
				t.Fatalf("binding not persisted: %+v err=%v", persisted, err)
			}
			if name == "linked_worktree" && (persisted.WorktreePath != expectedRoot || persisted.WorktreeBaseRepo != newRoot) {
				t.Fatalf("worktree binding lost: %+v", persisted)
			}
			if newErr != nil || oldErr == nil {
				t.Fatalf("relocation contract violated: expected write only under %q; old_read_error=%v new_read_error=%v", newRoot, oldErr, newErr)
			}
		})
	}
}
