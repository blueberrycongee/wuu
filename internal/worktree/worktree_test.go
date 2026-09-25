package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// initRepo creates a minimal git repo at dir with one commit.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "--", name)
	runGit(t, dir, "commit", "-q", "-m", "test commit")
	return runGit(t, dir, "rev-parse", "HEAD")
}

func mutatePrelaunchManifest(t *testing.T, path string, mutate func(*prelaunchManifest)) {
	t.Helper()
	manifest, exists, err := readPrelaunchManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("prelaunch manifest does not exist: %s", path)
	}
	mutate(&manifest)
	if err := writePrelaunchManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func TestNewManager_NotGitRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	_, err := NewManager(dir, filepath.Join(dir, "wt"))
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}

func TestNewManager_GitRepo(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m.parentRepo == "" {
		t.Fatal("parentRepo not set")
	}
}

func TestCreateAndCleanup(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}

	wt, err := m.Create("sess-1", "worker-A", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wt.Path == "" {
		t.Fatal("worktree path empty")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree not on disk: %v", err)
	}
	// README.md from initial commit should exist in the worktree.
	if _, err := os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
		t.Fatalf("expected README.md in worktree, got: %v", err)
	}
	if wt.HEAD == "" {
		t.Fatal("HEAD not recorded")
	}

	if err := m.Cleanup(wt); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, got: %v", err)
	}
}

func TestCleanupReportsLockedRegistryEntryAfterFallback(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wt, err := m.Create("sess-locked", "worker-locked", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		cmd := exec.Command("git", "worktree", "unlock", wt.Path)
		cmd.Dir = dir
		_ = cmd.Run()
		_ = m.Cleanup(wt)
	})

	// A locked worktree makes `remove --force` fail. The existing fallback
	// removes the directory manually, while `git worktree prune` deliberately
	// keeps the locked registry entry and still exits successfully.
	cmd := exec.Command("git", "worktree", "lock", "--reason", "cleanup-test", wt.Path)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lock worktree: %v\n%s", err, out)
	}

	cleanupErr := m.Cleanup(wt)
	if cleanupErr == nil {
		t.Fatal("Cleanup succeeded while Git still retained the locked worktree")
	}
	if !strings.Contains(cleanupErr.Error(), "Git registry still contains") {
		t.Fatalf("Cleanup error = %v, want registry inconsistency", cleanupErr)
	}
	if _, err := os.Lstat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manual fallback should have removed the worktree path, got %v", err)
	}
	registered, err := m.worktreeRegistered(wt.Path)
	if err != nil {
		t.Fatalf("check worktree registry: %v", err)
	}
	if !registered {
		t.Fatal("locked worktree registry entry unexpectedly disappeared")
	}

	cmd = exec.Command("git", "worktree", "unlock", wt.Path)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unlock missing worktree entry: %v\n%s", err, out)
	}
	if err := m.Cleanup(wt); err != nil {
		t.Fatalf("Cleanup after unlock: %v", err)
	}
	registered, err = m.worktreeRegistered(wt.Path)
	if err != nil {
		t.Fatalf("check final worktree registry: %v", err)
	}
	if registered {
		t.Fatal("worktree registry entry remains after successful cleanup")
	}
}

func TestCleanupPropagatesPruneFailure(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wt, err := m.Create("sess-prune", "worker-prune", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	originalParent := m.parentRepo
	t.Cleanup(func() {
		m.parentRepo = originalParent
		_ = m.Cleanup(wt)
	})

	// Make every Git cleanup command fail deterministically without relying on
	// filesystem permissions. The manual disk fallback still succeeds, leaving
	// the original repository's registry entry behind.
	m.parentRepo = filepath.Join(dir, "missing-parent-repo")
	cleanupErr := m.Cleanup(wt)
	m.parentRepo = originalParent
	if cleanupErr == nil {
		t.Fatal("Cleanup swallowed git worktree prune failure")
	}
	if !strings.Contains(cleanupErr.Error(), "git worktree prune") {
		t.Fatalf("Cleanup error = %v, want prune failure", cleanupErr)
	}
	if _, err := os.Lstat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manual fallback should have removed the worktree path, got %v", err)
	}
	registered, err := m.worktreeRegistered(wt.Path)
	if err != nil {
		t.Fatalf("check worktree registry: %v", err)
	}
	if !registered {
		t.Fatal("worktree registry entry unexpectedly disappeared after prune failure")
	}

	if err := m.Cleanup(wt); err != nil {
		t.Fatalf("Cleanup after restoring parent repo: %v", err)
	}
}

func TestCreate_DuplicateFails(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, _ := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))

	wt, err := m.Create("sess", "dup", "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup(wt)

	if _, err := m.Create("sess", "dup", ""); err == nil {
		t.Fatal("expected duplicate Create to fail")
	}
}

func TestOpenOrCreateReusesCrashedPrelaunchAndCleanupRemovesManifest(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	root := filepath.Join(t.TempDir(), "worktrees")
	m, err := NewManager(dir, root)
	if err != nil {
		t.Fatal(err)
	}

	first, err := m.OpenOrCreate(OpenOrCreateOptions{SessionID: "sess", WorkerID: "worker", BaseRepo: dir})
	if err != nil {
		t.Fatalf("OpenOrCreate first launch: %v", err)
	}
	t.Cleanup(func() { _ = m.Cleanup(first) })
	if _, err := os.Stat(first.ManifestPath); err != nil {
		t.Fatalf("prelaunch manifest missing: %v", err)
	}
	originalHead := first.HEAD
	newHead := commitFile(t, dir, "after-crash.txt", "new parent head\n")
	if newHead == originalHead {
		t.Fatal("parent HEAD did not advance")
	}

	restarted, err := NewManager(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := restarted.OpenOrCreate(OpenOrCreateOptions{SessionID: "sess", WorkerID: "worker", BaseRepo: dir})
	if err != nil {
		t.Fatalf("OpenOrCreate after simulated crash: %v", err)
	}
	if reopened.Path != first.Path || reopened.HEAD != originalHead {
		t.Fatalf("reopened worktree = %+v, want path %q at frozen HEAD %q", reopened, first.Path, originalHead)
	}
	if err := restarted.Cleanup(reopened); err != nil {
		t.Fatalf("Cleanup reopened worktree: %v", err)
	}
	if _, err := os.Stat(reopened.ManifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Cleanup left prelaunch manifest: %v", err)
	}
}

func TestOpenOrCreateCreatesMissingTargetFromManifestFrozenRevision(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	root := filepath.Join(t.TempDir(), "worktrees")
	m, err := NewManager(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	base, err := m.ResolveBase(dir, "")
	if err != nil {
		t.Fatalf("ResolveBase: %v", err)
	}
	target := filepath.Join(root, "sess", "worker")
	manifestPath := m.prelaunchManifestPath("sess", "worker")
	manifest := prelaunchManifest{
		SchemaVersion: prelaunchManifestSchema,
		SessionID:     "sess",
		WorkerID:      "worker",
		ParentRepo:    m.parentRepo,
		Repository:    base.Repository,
		BaseRepo:      base.Repo,
		BaseRevision:  base.Revision,
		TargetPath:    target,
		CreatedAt:     time.Now().UTC(),
	}
	if err := writePrelaunchManifest(manifestPath, manifest); err != nil {
		t.Fatalf("write simulated pre-create manifest: %v", err)
	}
	newHead := commitFile(t, dir, "advanced.txt", "advanced\n")
	if newHead == base.Revision {
		t.Fatal("parent HEAD did not advance")
	}

	wt, err := m.OpenOrCreate(OpenOrCreateOptions{SessionID: "sess", WorkerID: "worker", BaseRepo: dir})
	if err != nil {
		t.Fatalf("resume manifest-only prelaunch: %v", err)
	}
	t.Cleanup(func() { _ = m.Cleanup(wt) })
	if wt.HEAD != base.Revision {
		t.Fatalf("created HEAD = %q, want frozen %q", wt.HEAD, base.Revision)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "advanced.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree unexpectedly used advanced parent HEAD: %v", err)
	}
}

func TestOpenOrCreateRejectsIdentityAndGitMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Manager, *Worktree, *OpenOrCreateOptions)
	}{
		{
			name: "session",
			mutate: func(t *testing.T, _ *Manager, wt *Worktree, _ *OpenOrCreateOptions) {
				mutatePrelaunchManifest(t, wt.ManifestPath, func(manifest *prelaunchManifest) { manifest.SessionID = "other-session" })
			},
		},
		{
			name: "worker",
			mutate: func(t *testing.T, _ *Manager, wt *Worktree, _ *OpenOrCreateOptions) {
				mutatePrelaunchManifest(t, wt.ManifestPath, func(manifest *prelaunchManifest) { manifest.WorkerID = "other-worker" })
			},
		},
		{
			name: "base repo",
			mutate: func(t *testing.T, _ *Manager, _ *Worktree, opts *OpenOrCreateOptions) {
				other := t.TempDir()
				initRepo(t, other)
				opts.BaseRepo = other
			},
		},
		{
			name: "base revision",
			mutate: func(t *testing.T, m *Manager, _ *Worktree, opts *OpenOrCreateOptions) {
				opts.BaseRevision = commitFile(t, m.parentRepo, "new-head.txt", "new head\n")
			},
		},
		{
			name: "target",
			mutate: func(t *testing.T, _ *Manager, wt *Worktree, _ *OpenOrCreateOptions) {
				mutatePrelaunchManifest(t, wt.ManifestPath, func(manifest *prelaunchManifest) { manifest.TargetPath += "-other" })
			},
		},
		{
			name: "HEAD",
			mutate: func(t *testing.T, m *Manager, wt *Worktree, _ *OpenOrCreateOptions) {
				newHead := commitFile(t, m.parentRepo, "head-mismatch.txt", "mismatch\n")
				runGit(t, wt.Path, "checkout", "--detach", newHead)
			},
		},
		{
			name: "dirty target",
			mutate: func(t *testing.T, _ *Manager, wt *Worktree, _ *OpenOrCreateOptions) {
				if err := os.WriteFile(filepath.Join(wt.Path, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			initRepo(t, dir)
			m, err := NewManager(dir, filepath.Join(t.TempDir(), "worktrees"))
			if err != nil {
				t.Fatal(err)
			}
			opts := OpenOrCreateOptions{SessionID: "sess", WorkerID: "worker", BaseRepo: dir}
			wt, err := m.OpenOrCreate(opts)
			if err != nil {
				t.Fatalf("OpenOrCreate fixture: %v", err)
			}
			t.Cleanup(func() { _ = m.Cleanup(wt) })
			test.mutate(t, m, wt, &opts)

			if _, err := m.OpenOrCreate(opts); !errors.Is(err, ErrPrelaunchIdentityMismatch) {
				t.Fatalf("OpenOrCreate mismatch error = %v, want ErrPrelaunchIdentityMismatch", err)
			}
		})
	}
}

func TestOpenOrCreateRejectsMissingAndCorruptManifest(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt",
			mutate: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			initRepo(t, dir)
			m, err := NewManager(dir, filepath.Join(t.TempDir(), "worktrees"))
			if err != nil {
				t.Fatal(err)
			}
			opts := OpenOrCreateOptions{SessionID: "sess", WorkerID: "worker", BaseRepo: dir}
			wt, err := m.OpenOrCreate(opts)
			if err != nil {
				t.Fatalf("OpenOrCreate fixture: %v", err)
			}
			t.Cleanup(func() { _ = m.Cleanup(wt) })
			test.mutate(t, wt.ManifestPath)

			if _, err := m.OpenOrCreate(opts); !errors.Is(err, ErrPrelaunchIdentityMismatch) {
				t.Fatalf("OpenOrCreate manifest error = %v, want ErrPrelaunchIdentityMismatch", err)
			}
		})
	}
}

func TestOpenOrCreateConcurrentSameIdentity(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	opts := OpenOrCreateOptions{SessionID: "sess", WorkerID: "worker", BaseRepo: dir}
	const callers = 6
	results := make(chan *Worktree, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wt, err := m.OpenOrCreate(opts)
			results <- wt
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent OpenOrCreate: %v", err)
		}
	}
	var first *Worktree
	for wt := range results {
		if first == nil {
			first = wt
			continue
		}
		if wt == nil || wt.Path != first.Path || wt.HEAD != first.HEAD {
			t.Fatalf("concurrent worktrees differ: first=%+v current=%+v", first, wt)
		}
	}
	if first == nil {
		t.Fatal("no concurrent OpenOrCreate result")
	}
	if err := m.Cleanup(first); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
}

func TestCleanupSession(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, _ := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))

	for _, wid := range []string{"a", "b", "c"} {
		if _, err := m.Create("sess-X", wid, ""); err != nil {
			t.Fatalf("Create %s: %v", wid, err)
		}
	}

	list, _ := m.List("sess-X")
	if len(list) != 3 {
		t.Fatalf("expected 3 worktrees, got %d", len(list))
	}

	if err := m.CleanupSession("sess-X"); err != nil {
		t.Fatalf("CleanupSession: %v", err)
	}

	list, _ = m.List("sess-X")
	if len(list) != 0 {
		t.Fatalf("expected 0 worktrees after cleanup, got %d", len(list))
	}
}

func TestCleanupSessionRemovesManifestWithoutTarget(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	root := filepath.Join(t.TempDir(), "worktrees")
	m, err := NewManager(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	base, err := m.ResolveBase("", "")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := m.prelaunchManifestPath("sess", "worker")
	if err := writePrelaunchManifest(manifestPath, prelaunchManifest{
		SchemaVersion: prelaunchManifestSchema,
		SessionID:     "sess",
		WorkerID:      "worker",
		ParentRepo:    m.parentRepo,
		Repository:    base.Repository,
		BaseRepo:      base.Repo,
		BaseRevision:  base.Revision,
		TargetPath:    filepath.Join(root, "sess", "worker"),
		CreatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.CleanupSession("sess"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest-only launch was not cleaned: %v", err)
	}
	if err := m.CleanupSession("../outside"); err == nil {
		t.Fatal("CleanupSession accepted a path-traversing session ID")
	}
}

func TestCreate_FromBaseRepo(t *testing.T) {
	// Make a base repo, commit to it, then create a chained worktree
	// from a worktree of it.
	dir := t.TempDir()
	initRepo(t, dir)
	m, _ := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))

	wtA, err := m.Create("sess", "A", "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup(wtA)

	// Make a change in worktree A and commit it.
	if err := os.WriteFile(filepath.Join(wtA.Path, "new.txt"), []byte("from A"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "new.txt"},
		{"commit", "-q", "-m", "from A"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = wtA.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in worktree A: %v\n%s", args, err, out)
		}
	}

	// Now spawn worker B based on worktree A.
	wtB, err := m.Create("sess", "B", wtA.Path)
	if err != nil {
		t.Fatalf("Create chained: %v", err)
	}
	defer m.Cleanup(wtB)

	// Worker B should see the file A added.
	if _, err := os.Stat(filepath.Join(wtB.Path, "new.txt")); err != nil {
		t.Fatalf("chained worktree should contain A's commit: %v", err)
	}
}

func TestList_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, _ := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))

	list, err := m.List("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestHasChanges(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, _ := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))

	wt, err := m.Create("sess", "wA", "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup(wt)

	// Pristine worktree should be clean.
	dirty, err := m.HasChanges(wt)
	if err != nil {
		t.Fatalf("HasChanges (clean): %v", err)
	}
	if dirty {
		t.Fatal("expected clean, got dirty")
	}

	// Dropping an untracked file should flip it to dirty.
	if err := os.WriteFile(filepath.Join(wt.Path, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = m.HasChanges(wt)
	if err != nil {
		t.Fatalf("HasChanges (untracked): %v", err)
	}
	if !dirty {
		t.Fatal("expected dirty after untracked add")
	}
}

func TestCleanupIfClean(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, _ := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))

	// Clean worktree: removed.
	wt1, err := m.Create("sess", "clean", "")
	if err != nil {
		t.Fatal(err)
	}
	kept, err := m.CleanupIfClean(wt1)
	if err != nil {
		t.Fatalf("CleanupIfClean clean: %v", err)
	}
	if kept {
		t.Fatal("clean worktree should not be kept")
	}
	if _, err := os.Stat(wt1.Path); !os.IsNotExist(err) {
		t.Fatalf("clean worktree should be gone, got: %v", err)
	}

	// Dirty worktree: kept.
	wt2, err := m.Create("sess", "dirty", "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup(wt2)
	if err := os.WriteFile(filepath.Join(wt2.Path, "edit.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, err = m.CleanupIfClean(wt2)
	if err != nil {
		t.Fatalf("CleanupIfClean dirty: %v", err)
	}
	if !kept {
		t.Fatal("dirty worktree should be kept")
	}
	if _, err := os.Stat(wt2.Path); err != nil {
		t.Fatalf("dirty worktree should still exist: %v", err)
	}
}

func TestCreateLeaseWritesManifestAndReview(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}

	lease, err := m.CreateLease(LeaseOptions{
		SessionID:   "sess",
		TaskID:      "task-1",
		AgentID:     "agent-1",
		Branch:      "wuu/task-1",
		ManifestDir: filepath.Join(dir, ".wuu", "leases"),
	})
	if err != nil {
		t.Fatalf("CreateLease: %v", err)
	}
	defer m.Cleanup(&Worktree{Path: lease.Path})

	if lease.TaskID != "task-1" || lease.AgentID != "agent-1" || lease.BaseHEAD == "" || lease.Branch != "wuu/task-1" {
		t.Fatalf("lease identity not recorded: %+v", lease)
	}
	if _, err := os.Stat(lease.ManifestPath); err != nil {
		t.Fatalf("manifest should exist: %v", err)
	}

	if err := os.WriteFile(filepath.Join(lease.Path, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Path, "new.txt"), []byte("untracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err := m.Review(lease, dir)
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if !review.Status.Dirty || len(review.Status.ChangedFiles) != 2 {
		t.Fatalf("review should see changed tracked and untracked files: %+v", review.Status)
	}
	if review.Diff == "" || !review.MergePreview.CanApply {
		t.Fatalf("expected tracked diff with clean merge preview: %+v", review)
	}

	if err := m.WriteManifest(lease); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	data, err := os.ReadFile(lease.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if string(data) == "" || !containsBytes(data, []byte(`"dirty": true`)) {
		t.Fatalf("manifest should record dirty state:\n%s", string(data))
	}
}

func TestApplyToTargetAppliesTrackedDiff(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.CreateLease(LeaseOptions{SessionID: "sess", TaskID: "apply"})
	if err != nil {
		t.Fatalf("CreateLease: %v", err)
	}
	defer m.Cleanup(&Worktree{Path: lease.Path})
	if err := os.WriteFile(filepath.Join(lease.Path, "README.md"), []byte("applied\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := m.ApplyToTarget(lease, dir)
	if err != nil {
		t.Fatalf("ApplyToTarget: %v", err)
	}
	if !result.Applied || len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "README.md" {
		t.Fatalf("unexpected apply result: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if string(data) != "applied\n" {
		t.Fatalf("target file not updated: %q", string(data))
	}
}

func TestApplyToTargetRejectsUntrackedWorktreeFiles(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.CreateLease(LeaseOptions{SessionID: "sess", TaskID: "apply-untracked"})
	if err != nil {
		t.Fatalf("CreateLease: %v", err)
	}
	defer m.Cleanup(&Worktree{Path: lease.Path})
	if err := os.WriteFile(filepath.Join(lease.Path, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = m.ApplyToTarget(lease, dir)
	if err == nil || !strings.Contains(err.Error(), "untracked files") {
		t.Fatalf("expected untracked rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("untracked file should not be applied, stat err=%v", statErr)
	}
}

func TestRollbackLeaseResetsIsolatedWorktree(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	m, err := NewManager(dir, filepath.Join(dir, ".wuu", "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.CreateLease(LeaseOptions{SessionID: "sess", TaskID: "rollback"})
	if err != nil {
		t.Fatalf("CreateLease: %v", err)
	}
	defer m.Cleanup(&Worktree{Path: lease.Path})

	if err := os.WriteFile(filepath.Join(lease.Path, "README.md"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Path, "scratch.txt"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(lease)
	if err != nil {
		t.Fatalf("Status dirty: %v", err)
	}
	if !status.Dirty {
		t.Fatal("expected dirty lease")
	}

	if err := m.RollbackLease(lease); err != nil {
		t.Fatalf("RollbackLease: %v", err)
	}
	status, err = m.Status(lease)
	if err != nil {
		t.Fatalf("Status clean: %v", err)
	}
	if status.Dirty {
		t.Fatalf("expected clean lease after rollback: %+v", status)
	}
	if _, err := os.Stat(filepath.Join(lease.Path, "scratch.txt")); !os.IsNotExist(err) {
		t.Fatalf("scratch should be removed, got %v", err)
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestIsGitRepo(t *testing.T) {
	notGit := t.TempDir()
	if IsGitRepo(notGit) {
		t.Error("temp dir should not be a git repo")
	}

	gitDir := t.TempDir()
	initRepo(t, gitDir)
	if !IsGitRepo(gitDir) {
		t.Error("initialized dir should be a git repo")
	}
}

func TestSnapshotPreservesWorkingTreeAndFreezesCandidate(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	base := runGit(t, root, "rev-parse", "HEAD")
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "staged\n")
	runGit(t, root, "add", "README.md")
	write("README.md", "unstaged\n")
	write("new.txt", "untracked\n")
	before := runGit(t, root, "status", "--porcelain=v1")
	index := runGit(t, root, "write-tree")
	revision, diff, err := Snapshot(context.Background(), root, base, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+unstaged") || !strings.Contains(diff, "+untracked") {
		t.Fatalf("incomplete candidate: %s", diff)
	}
	if before != runGit(t, root, "status", "--porcelain=v1") || index != runGit(t, root, "write-tree") || base != runGit(t, root, "rev-parse", "HEAD") {
		t.Fatal("snapshot changed user's git state")
	}
	write("README.md", "later edit\n")
	replay, replayDiff, err := Snapshot(context.Background(), root, base, "run-1")
	if err != nil || replay != revision || replayDiff != diff {
		t.Fatalf("candidate changed on replay: %s %v", replay, err)
	}
}

func TestApplySnapshotPreservesUnrelatedChangesAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	initRepo(t, root)
	base := runGit(t, root, "rev-parse", "HEAD")
	candidate := filepath.Join(t.TempDir(), "candidate")
	runGit(t, root, "worktree", "add", "--detach", candidate, base)
	if err := os.WriteFile(filepath.Join(candidate, "README.md"), []byte("candidate\n"), 0600); err != nil {
		t.Fatal(err)
	}
	revision, _, err := Snapshot(ctx, candidate, base, "apply-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "user.txt"), []byte("keep me\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "user.txt")
	index := runGit(t, root, "write-tree")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("conflicting user edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ApplySnapshot(ctx, root, base, revision); err == nil {
		t.Fatal("conflicting patch applied")
	}
	content, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil || string(content) != "conflicting user edit\n" {
		t.Fatal("conflict damaged user edits")
	}
	runGit(t, root, "restore", "--worktree", "README.md")
	for range 2 {
		if err := ApplySnapshot(ctx, root, base, revision); err != nil {
			t.Fatal(err)
		}
	}
	if index != runGit(t, root, "write-tree") {
		t.Fatal("apply changed user's index")
	}
	content, err = os.ReadFile(filepath.Join(root, "user.txt"))
	if err != nil || string(content) != "keep me\n" {
		t.Fatal("apply lost unrelated work")
	}
}
