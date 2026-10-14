package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

type sessionWorkspaceErrorWriter struct{}

func (sessionWorkspaceErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("notification unavailable")
}

func TestRebindThreadWorkspacePersistsLinkedWorktreeAndNotifies(t *testing.T) {
	repo := initSessionWorkspaceRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runSessionWorkspaceGit(t, repo, "worktree", "add", "-b", "session-workspace-test", linked)

	rt := newTestRuntime(t, &fakeClient{})
	rt.RootDir = repo
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])

	if err := srv.rebindThreadWorkspace(start.Thread.ID, linked); err != nil {
		t.Fatalf("rebindThreadWorkspace: %v", err)
	}
	want, err := canonicalWorkspaceDirectory(linked)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := session.Find(rt.SessionDir, start.Thread.ID)
	if err != nil || !found {
		t.Fatalf("Find: found=%v err=%v", found, err)
	}
	if stored.CWD != want || stored.WorktreePath != want {
		t.Fatalf("stored workspace = cwd %q, path %q; want %q", stored.CWD, stored.WorktreePath, want)
	}
	wantBaseRepo, err := canonicalWorkspaceDirectory(repo)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorktreeBaseRepo != wantBaseRepo {
		t.Fatalf("stored base repo = %q, want %q", stored.WorktreeBaseRepo, wantBaseRepo)
	}
	if stored.WorktreeBaseHEAD == "" {
		t.Fatal("stored worktree base HEAD is empty")
	}
	thread := srv.thread(start.Thread.ID)
	if thread == nil || thread.CWD != want || thread.WorktreePath != want {
		t.Fatalf("in-memory thread was not rebound: %+v", thread)
	}
	updated := remarshal[ThreadUpdatedNotification](t,
		notificationByMethod(t, parseOutput(t, out.String()), "thread/updated")["params"])
	if updated.Thread.ID != start.Thread.ID || updated.Thread.CWD != want {
		t.Fatalf("thread/updated notification did not include rebound cwd: %+v", updated)
	}

	reloadOut := &lockedBuffer{}
	restarted := New(rt, reloadOut)
	if err := restarted.handleLine(context.Background(), []byte(`{"id":"2","method":"thread/list"}`)); err != nil {
		t.Fatalf("thread/list after restart: %v", err)
	}
	reloaded := remarshal[ThreadListResult](t,
		responseByID(t, parseOutput(t, reloadOut.String()), "2")["result"]).Threads
	var restored *Thread
	for index := range reloaded {
		candidate := &reloaded[index]
		if candidate.ID == start.Thread.ID {
			restored = candidate
			break
		}
	}
	if restored == nil || restored.CWD != want || restored.Worktree == nil || restored.Worktree.Path != want {
		t.Fatalf("reloaded thread did not restore workspace: %+v", restored)
	}
}

// The model sees symlink-resolved paths, while the project may be registered
// through a symlink. Moving back to the project by its resolved path must
// leave the worktree, not bind the project as a linked worktree.
func TestRebindThreadWorkspaceBackToProjectThroughSymlink(t *testing.T) {
	repo := initSessionWorkspaceRepo(t)
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	projectLink := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(resolvedRepo, projectLink); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	runSessionWorkspaceGit(t, repo, "worktree", "add", "-b", "session-workspace-back", linked)

	rt := newTestRuntime(t, &fakeClient{})
	rt.RootDir = projectLink
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threadID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	if err := srv.rebindThreadWorkspace(threadID, linked); err != nil {
		t.Fatalf("rebind to linked worktree: %v", err)
	}
	if err := srv.rebindThreadWorkspace(threadID, resolvedRepo); err != nil {
		t.Fatalf("rebind back to project: %v", err)
	}

	stored, found, err := session.Find(rt.SessionDir, threadID)
	if err != nil || !found {
		t.Fatalf("Find: found=%v err=%v", found, err)
	}
	if stored.WorktreePath != "" || stored.WorktreeBaseRepo != "" || stored.WorktreeBaseHEAD != "" {
		t.Fatalf("moving back to the project kept a worktree binding: path %q, base repo %q, base HEAD %q",
			stored.WorktreePath, stored.WorktreeBaseRepo, stored.WorktreeBaseHEAD)
	}
	if stored.CWD != projectLink {
		t.Fatalf("stored cwd = %q, want the project path %q", stored.CWD, projectLink)
	}
}

func TestRebindThreadWorkspaceRejectsUnrelatedRepositoryAndSubdirectory(t *testing.T) {
	repo := initSessionWorkspaceRepo(t)
	rt := newTestRuntime(t, &fakeClient{})
	rt.RootDir = repo
	srv := New(rt, &lockedBuffer{})
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threads, err := session.List(rt.SessionDir, 10)
	if err != nil || len(threads) != 1 {
		t.Fatalf("List: len=%d err=%v", len(threads), err)
	}

	unrelated := initSessionWorkspaceRepo(t)
	if err := srv.rebindThreadWorkspace(threads[0].ID, unrelated); err == nil ||
		!strings.Contains(err.Error(), "linked worktree") {
		t.Fatalf("unrelated repository error = %v", err)
	}
	subdir := filepath.Join(repo, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.rebindThreadWorkspace(threads[0].ID, subdir); err == nil ||
		!strings.Contains(err.Error(), "worktree root") {
		t.Fatalf("subdirectory error = %v", err)
	}
	stored, found, err := session.Find(rt.SessionDir, threads[0].ID)
	if err != nil || !found {
		t.Fatalf("Find: found=%v err=%v", found, err)
	}
	wantRepo, err := canonicalWorkspaceDirectory(repo)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CWD != wantRepo || stored.WorktreePath != "" {
		t.Fatalf("rejected workspace mutated session: %+v", stored)
	}
}

func TestRebindThreadWorkspaceCompletesWhenNotificationFails(t *testing.T) {
	repo := initSessionWorkspaceRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runSessionWorkspaceGit(t, repo, "worktree", "add", "-b", "notification-failure", linked)

	rt := newTestRuntime(t, &fakeClient{})
	rt.RootDir = repo
	out := &lockedBuffer{}
	srv := New(rt, out)
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	start := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	srv.out = sessionWorkspaceErrorWriter{}

	if err := srv.rebindThreadWorkspace(start.Thread.ID, linked); err != nil {
		t.Fatalf("rebindThreadWorkspace: %v", err)
	}
	want, err := canonicalWorkspaceDirectory(linked)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := session.Find(rt.SessionDir, start.Thread.ID)
	if err != nil || !found || stored.CWD != want {
		t.Fatalf("stored workspace after notification failure: found=%v err=%v session=%+v", found, err, stored)
	}
	thread := srv.thread(start.Thread.ID)
	if thread == nil || thread.CWD != want {
		t.Fatalf("in-memory workspace after notification failure: %+v", thread)
	}
}

func TestRebindThreadWorkspaceCompletesWhenChildAgentLookupFails(t *testing.T) {
	repo := initSessionWorkspaceRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runSessionWorkspaceGit(t, repo, "worktree", "add", "-b", "child-agent-failure", linked)

	rt := newTestRuntime(t, &fakeClient{})
	rt.RootDir = repo
	rt.StateDir = t.TempDir()
	srv := New(rt, &lockedBuffer{})
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threads, err := session.List(rt.SessionDir, 10)
	if err != nil || len(threads) != 1 {
		t.Fatalf("List: len=%d err=%v", len(threads), err)
	}
	threadID := threads[0].ID
	threadsPath := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, threadID), "threads")
	if err := os.MkdirAll(filepath.Dir(threadsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(threadsPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := srv.rebindThreadWorkspace(threadID, linked); err != nil {
		t.Fatalf("rebindThreadWorkspace: %v", err)
	}
	want, err := canonicalWorkspaceDirectory(linked)
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := session.Find(rt.SessionDir, threadID)
	if err != nil || !found || stored.CWD != want {
		t.Fatalf("stored workspace after child-agent failure: found=%v err=%v session=%+v", found, err, stored)
	}
	thread := srv.thread(threadID)
	if thread == nil || thread.CWD != want {
		t.Fatalf("in-memory workspace after child-agent failure: %+v", thread)
	}
}

func TestRebindThreadWorkspaceRejectsNonGitProject(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	srv := New(rt, &lockedBuffer{})
	if err := srv.handleLine(context.Background(), []byte(`{"id":"1","method":"thread/start"}`)); err != nil {
		t.Fatalf("thread/start: %v", err)
	}
	threads, err := session.List(rt.SessionDir, 10)
	if err != nil || len(threads) != 1 {
		t.Fatalf("List: len=%d err=%v", len(threads), err)
	}
	if err := srv.rebindThreadWorkspace(threads[0].ID, rt.RootDir); err == nil ||
		!strings.Contains(err.Error(), "project Git repository") {
		t.Fatalf("non-Git project error = %v", err)
	}
}

func initSessionWorkspaceRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runSessionWorkspaceGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSessionWorkspaceGit(t, repo, "add", "README.md")
	runSessionWorkspaceGit(t, repo,
		"-c", "user.name=Wuu Test",
		"-c", "user.email=wuu-test@example.invalid",
		"commit", "-m", "initial")
	return repo
}

func runSessionWorkspaceGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// Exercise the read-only RPC before and after a pin, including workspace
// ownership and an external engine which does not use Wuu's transport.
func TestThreadExecutionEnvironmentReadOnly(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	defer srv.Close()
	th := newThreadState("old", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now().UTC())
	srv.threads[th.ID] = th
	read := func(id, thread string) map[string]any {
		t.Helper()
		raw := fmt.Sprintf(`{"id":%q,"method":"thread/execution-environment","params":{"thread_id":%q}}`, id, thread)
		if err := srv.handleLine(context.Background(), []byte(raw)); err != nil {
			t.Fatal(err)
		}
		return responseByID(t, parseOutput(t, out.String()), id)
	}
	for _, missing := range []string{"directory", "database"} {
		t.Run("missing_"+missing, func(t *testing.T) {
			original := rt.SessionDir
			rt.SessionDir = filepath.Join(t.TempDir(), "sessions")
			t.Cleanup(func() { rt.SessionDir = original })
			probe := rt.SessionDir
			if missing == "database" {
				if err := os.Mkdir(rt.SessionDir, 0700); err != nil {
					t.Fatal(err)
				}
				probe = session.DBPath(rt.SessionDir)
			}
			if response := read("missing-"+missing, "not-loaded"); response["error"] == nil {
				t.Fatalf("missing session accepted: %+v", response)
			}
			if _, err := os.Stat(probe); !os.IsNotExist(err) {
				t.Fatalf("inspection created missing %s: %v", missing, err)
			}
		})
	}
	response := read("unselected", th.ID)
	if response["error"] != nil {
		t.Fatal(response)
	}
	result := remarshal[ThreadExecutionEnvironmentResult](t, response["result"])
	if result.Environment.State != "not_initialized" {
		t.Fatalf("unselected: %+v", result)
	}
	stateDir, err := srv.workspaceStateDir()
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := statepath.SessionArtifactDir(stateDir, th.ID)
	pin := filepath.Join(artifactDir, "execution-environment.json")
	if _, err := os.Stat(pin); !os.IsNotExist(err) {
		t.Fatalf("read selected environment: %v", err)
	}
	if err := os.MkdirAll(artifactDir, 0700); err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(executionenv.Selection{Name: "my-worker", Profile: &executionenv.Profile{Backend: "command", Command: []string{"not-installed", "secret-argument"}, Workspace: "/worker"}})
	if err := os.WriteFile(pin, saved, 0600); err != nil {
		t.Fatal(err)
	}
	response = read("selected", th.ID)
	result = remarshal[ThreadExecutionEnvironmentResult](t, response["result"])
	if result.Environment.Name != "my-worker" || result.Environment.Root != "/worker" || result.Environment.State != "not_initialized" {
		t.Fatalf("pin: %+v", response)
	}
	if strings.Contains(fmt.Sprint(response), "secret-argument") {
		t.Fatal("profile arguments leaked")
	}
	th.EngineID = "codex"
	response = read("external", th.ID)
	result = remarshal[ThreadExecutionEnvironmentResult](t, response["result"])
	if result.Environment.State != "unsupported" || result.Environment.Name != "my-worker" {
		t.Fatalf("external: %+v", response)
	}
	if err := os.Remove(pin); err != nil {
		t.Fatal(err)
	}
	response = read("external-local", th.ID)
	result = remarshal[ThreadExecutionEnvironmentResult](t, response["result"])
	if result.Environment.State != "external" || result.Environment.Root != th.CWD || result.Environment.Name != "codex" {
		t.Fatalf("external local: %+v", response)
	}
	th.WorkspaceID = "another-project"
	if response := read("foreign-project", th.ID); response["error"] == nil {
		t.Fatalf("read environment from the wrong project store: %+v", response)
	}
	for _, id := range []string{"missing", "../old", ""} {
		if response := read("invalid-"+id, id); response["error"] == nil {
			t.Fatalf("invalid session accepted: %+v", response)
		}
	}
}
