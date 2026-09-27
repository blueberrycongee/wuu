// Package worktree wraps git worktree operations for subagent isolation.
//
// Each subagent in coordinator mode runs inside its own git worktree,
// rooted under the workspace state directory at
// worktrees/{session-id}/{worker-id}/. The worktree is
// created in detached HEAD mode based on the parent repository's current
// HEAD, so workers see a snapshot of the project at spawn time without
// polluting the parent's branch state.
//
// Worktrees persist after the worker completes so the user can inspect
// the changes (cd into the path, git diff, cherry-pick, etc.). Cleanup
// is explicit via Cleanup() or CleanupSession().
package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Worktree represents one isolated git worktree for a subagent.
type Worktree struct {
	Path         string // absolute path to the worktree directory
	SessionID    string // owning session
	WorkerID     string // worker that owns it
	HEAD         string // the frozen commit it was created from (full sha)
	BaseRepo     string // canonical repository/worktree used to resolve HEAD
	ManifestPath string // durable prelaunch identity, when opened recoverably
}

// Lease records the durable identity and review state for a task worktree.
type Lease struct {
	Path         string    `json:"path"`
	SessionID    string    `json:"session_id"`
	TaskID       string    `json:"task_id"`
	AgentID      string    `json:"agent_id,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	BaseRepo     string    `json:"base_repo,omitempty"`
	BaseHEAD     string    `json:"base_head"`
	Dirty        bool      `json:"dirty,omitempty"`
	ChangedFiles []string  `json:"changed_files,omitempty"`
	ManifestPath string    `json:"manifest_path,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type LeaseOptions struct {
	SessionID   string
	TaskID      string
	AgentID     string
	Branch      string
	BaseRepo    string
	ManifestDir string
}

type Status struct {
	Dirty        bool     `json:"dirty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	Porcelain    []string `json:"porcelain,omitempty"` // XY status plus a literal destination path per entry
}

type MergePreview struct {
	CanApply      bool     `json:"can_apply"`
	ConflictFiles []string `json:"conflict_files,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type Review struct {
	Status       Status       `json:"status"`
	Diff         string       `json:"diff,omitempty"`
	MergePreview MergePreview `json:"merge_preview"`
}

type ApplyResult struct {
	Applied      bool     `json:"applied"`
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// Manager creates and tracks worktrees rooted at a parent repository.
type Manager struct {
	parentRepo string // absolute path to the source git repo (parent cwd)
	rootDir    string // workspace-state worktrees directory
}

// NewManager constructs a Manager. parentRepo must be inside a git
// repository (or be one). rootDir is where worktrees are stored,
// typically under the user-level workspace state directory.
func NewManager(parentRepo, rootDir string) (*Manager, error) {
	abs, err := filepath.Abs(parentRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve parent repo: %w", err)
	}
	if !isGitRepo(abs) {
		return nil, fmt.Errorf("not a git repository: %s (run 'git init' first)", abs)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root: %w", err)
	}
	return &Manager{
		parentRepo: abs,
		rootDir:    rootAbs,
	}, nil
}

// Create allocates a new worktree for the given session/worker.
// If baseRepo is empty, the parent repo's current HEAD is used as the
// source. If baseRepo points to another existing worktree, the new
// worktree is based on that worktree's HEAD instead - useful for
// chaining workers.
func (m *Manager) Create(sessionID, workerID, baseRepo string) (*Worktree, error) {
	if sessionID == "" || workerID == "" {
		return nil, errors.New("sessionID and workerID required")
	}

	source := m.parentRepo
	if strings.TrimSpace(baseRepo) != "" {
		abs, err := filepath.Abs(baseRepo)
		if err != nil {
			return nil, fmt.Errorf("resolve base repo: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("base repo does not exist: %s", abs)
		}
		source = abs
	}

	// Resolve the source's current HEAD so the worktree is reproducible.
	head, err := resolveHead(source)
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD of %s: %w", source, err)
	}

	target := filepath.Join(m.rootDir, sessionID, workerID)
	if _, err := os.Stat(target); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("create worktree parent dir: %w", err)
	}

	// `git worktree add --detach <path> <commit>` creates a worktree at
	// the given commit with no branch attached.
	cmd := exec.Command("git", "worktree", "add", "--detach", target, head)
	cmd.Dir = source
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git worktree add failed: %w\n%s", err, out)
	}

	return &Worktree{
		Path:      target,
		SessionID: sessionID,
		WorkerID:  workerID,
		HEAD:      head,
		BaseRepo:  source,
	}, nil
}

// CreateLease creates a task-bound worktree and writes a manifest describing
// the session/task/agent binding. It keeps Create unchanged for legacy callers.
func (m *Manager) CreateLease(opts LeaseOptions) (*Lease, error) {
	sessionID := strings.TrimSpace(opts.SessionID)
	taskID := strings.TrimSpace(opts.TaskID)
	if sessionID == "" || taskID == "" {
		return nil, errors.New("sessionID and taskID required")
	}
	workerID := strings.TrimSpace(opts.AgentID)
	if workerID == "" {
		workerID = taskID
	}
	wt, err := m.Create(sessionID, workerID, opts.BaseRepo)
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch != "" {
		if err := checkoutBranch(wt.Path, branch); err != nil {
			_ = m.Cleanup(wt)
			return nil, err
		}
	}
	now := time.Now().UTC()
	lease := &Lease{
		Path:      wt.Path,
		SessionID: sessionID,
		TaskID:    taskID,
		AgentID:   strings.TrimSpace(opts.AgentID),
		Branch:    branch,
		BaseRepo:  firstNonEmpty(opts.BaseRepo, m.parentRepo),
		BaseHEAD:  wt.HEAD,
		CreatedAt: now,
		UpdatedAt: now,
	}
	status, err := m.Status(lease)
	if err != nil {
		_ = m.Cleanup(wt)
		return nil, err
	}
	lease.Dirty = status.Dirty
	lease.ChangedFiles = status.ChangedFiles
	manifestDir := strings.TrimSpace(opts.ManifestDir)
	if manifestDir == "" {
		manifestDir = filepath.Join(m.rootDir, "manifests", sessionID)
	}
	lease.ManifestPath = filepath.Join(manifestDir, taskID+".json")
	if err := m.WriteManifest(lease); err != nil {
		_ = m.Cleanup(wt)
		return nil, err
	}
	return lease, nil
}

// Cleanup removes a worktree from disk and unregisters it from git.
// Safe to call on a worktree that's already been removed.
func (m *Manager) Cleanup(wt *Worktree) error {
	if wt == nil || wt.Path == "" {
		return nil
	}
	return m.withPrelaunchLock(func() error {
		if err := m.cleanupWorktree(wt); err != nil {
			return err
		}
		return m.removePrelaunchManifest(wt)
	})
}

func (m *Manager) cleanupWorktree(wt *Worktree) error {
	if _, err := os.Lstat(wt.Path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect worktree before cleanup: %w", err)
		}
		// The directory is already gone, but cleanup is not complete until Git
		// has also forgotten the worktree.
		return errors.Join(m.pruneFromGit(), m.verifyCleanup(wt.Path))
	}
	cmd := exec.Command("git", "worktree", "remove", "--force", wt.Path)
	cmd.Dir = m.parentRepo
	out, removeErr := cmd.CombinedOutput()
	if removeErr == nil {
		return m.verifyCleanup(wt.Path)
	}

	// Try a manual removal as a fallback (e.g., the worktree was already
	// detached from Git's metadata). This does not make cleanup successful on
	// its own: prune and the final disk/registry consistency check must also
	// succeed.
	if rmErr := os.RemoveAll(wt.Path); rmErr != nil {
		return fmt.Errorf("git worktree remove: %w\n%s\n(rmAll: %v)", removeErr, out, rmErr)
	}
	fallbackErr := errors.Join(m.pruneFromGit(), m.verifyCleanup(wt.Path))
	if fallbackErr != nil {
		return errors.Join(
			fmt.Errorf("git worktree remove: %w\n%s", removeErr, out),
			fallbackErr,
		)
	}
	return nil
}

// HasChanges reports pending changes or commits since the frozen base. An
// unknown base cannot prove that automatic cleanup is safe.
func (m *Manager) HasChanges(wt *Worktree) (bool, error) {
	status, err := m.Status(wt)
	if err != nil {
		return false, err
	}
	if status.Dirty {
		return true, nil
	}
	head, err := resolveHead(wt.Path)
	if err != nil {
		return false, err
	}
	return head != wt.HEAD, nil
}

// Status snapshots dirty state and changed files for a lease or worktree.
func (m *Manager) Status(target any) (Status, error) {
	path := worktreePath(target)
	if path == "" {
		return Status{}, errors.New("worktree path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return Status{}, fmt.Errorf("stat worktree: %w", err)
	}
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return Status{}, fmt.Errorf("git status: %w", err)
	}
	records := strings.Split(string(out), "\x00")
	lines := make([]string, 0, len(records))
	changed := make([]string, 0, len(records))
	for i := 0; i < len(records); i++ {
		line := records[i]
		if file := porcelainFile(line); file != "" {
			lines = append(lines, line)
			changed = append(changed, file)
			// With -z, rename/copy records list the literal destination first,
			// followed by a separate NUL-delimited source path.
			if strings.ContainsAny(line[:2], "RC") {
				i++
			}
		}
	}
	base, err := worktreeBase(target)
	if err != nil {
		return Status{}, err
	}
	cmd = exec.Command("git", "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", base, "--")
	cmd.Dir = path
	out, err = cmd.CombinedOutput()
	if err != nil {
		return Status{}, fmt.Errorf("git diff status: %w\n%s", err, out)
	}
	seen := make(map[string]bool, len(changed))
	for _, name := range changed {
		seen[name] = true
	}
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" && !seen[name] {
			changed = append(changed, name)
			seen[name] = true
		}
	}
	sort.Strings(changed)
	return Status{
		Dirty:        len(changed) > 0,
		ChangedFiles: changed,
		Porcelain:    lines,
	}, nil
}

func (m *Manager) Diff(target any) (string, error) {
	path := worktreePath(target)
	if path == "" {
		return "", errors.New("worktree path is required")
	}
	base, err := worktreeBase(target)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "diff", "--no-ext-diff", "--no-textconv", "--binary", base, "--")
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff: %w\n%s", err, out)
	}
	return string(out), nil
}

func (m *Manager) Review(target any, targetRepo string) (Review, error) {
	status, err := m.Status(target)
	if err != nil {
		return Review{}, err
	}
	diff, err := m.Diff(target)
	if err != nil {
		return Review{}, err
	}
	preview := MergePreview{CanApply: true}
	if strings.TrimSpace(targetRepo) != "" {
		preview = m.MergePreview(target, targetRepo)
	}
	return Review{Status: status, Diff: diff, MergePreview: preview}, nil
}

// MergePreview checks whether the worktree's tracked diff can be applied to
// targetRepo without mutating targetRepo.
func (m *Manager) MergePreview(target any, targetRepo string) MergePreview {
	status, err := m.Status(target)
	if err != nil {
		return MergePreview{Error: err.Error()}
	}
	if untracked := untrackedFiles(status.Porcelain); len(untracked) > 0 {
		return MergePreview{Error: fmt.Sprintf("worktree has untracked files that are not represented in the merge diff: %s", strings.Join(untracked, ", "))}
	}

	diff, err := m.Diff(target)
	if err != nil {
		return MergePreview{CanApply: false, Error: err.Error()}
	}
	if strings.TrimSpace(diff) == "" {
		return MergePreview{CanApply: true}
	}
	cmd := exec.Command("git", "apply", "--check", "--whitespace=nowarn", "-")
	cmd.Dir = targetRepo
	cmd.Stdin = strings.NewReader(diff)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return MergePreview{
			CanApply:      false,
			ConflictFiles: conflictFilesFromGitApply(out),
			Error:         strings.TrimSpace(string(out)),
		}
	}
	return MergePreview{CanApply: true}
}

// ApplyToTarget applies the worktree's tracked diff to targetRepo. It does not
// commit. Untracked worktree files are rejected because the tracked diff does not
// represent them.
func (m *Manager) ApplyToTarget(target any, targetRepo string) (ApplyResult, error) {
	status, err := m.Status(target)
	if err != nil {
		return ApplyResult{}, err
	}
	if untracked := untrackedFiles(status.Porcelain); len(untracked) > 0 {
		return ApplyResult{}, fmt.Errorf("worktree has untracked files that are not represented in the merge diff: %s", strings.Join(untracked, ", "))
	}
	diff, err := m.Diff(target)
	if err != nil {
		return ApplyResult{}, err
	}
	if strings.TrimSpace(diff) == "" {
		return ApplyResult{Applied: false}, nil
	}
	preview := m.MergePreview(target, targetRepo)
	if !preview.CanApply {
		if strings.TrimSpace(preview.Error) != "" {
			return ApplyResult{}, fmt.Errorf("worktree diff cannot apply cleanly: %s", preview.Error)
		}
		return ApplyResult{}, errors.New("worktree diff cannot apply cleanly")
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = targetRepo
	cmd.Stdin = strings.NewReader(diff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return ApplyResult{}, fmt.Errorf("git apply: %w\n%s", err, out)
	}
	return ApplyResult{Applied: true, ChangedFiles: status.ChangedFiles}, nil
}

// RollbackLease resets tracked and untracked changes inside the isolated
// worktree. It does not touch the parent repository.
func (m *Manager) RollbackLease(target any) error {
	path := worktreePath(target)
	if path == "" {
		return errors.New("worktree path is required")
	}
	for _, args := range [][]string{
		{"reset", "--hard", "HEAD"},
		{"clean", "-fd"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	if lease, ok := target.(*Lease); ok && lease != nil {
		status, err := m.Status(lease)
		if err == nil {
			lease.Dirty = status.Dirty
			lease.ChangedFiles = status.ChangedFiles
			lease.UpdatedAt = time.Now().UTC()
			_ = m.WriteManifest(lease)
		}
	}
	return nil
}

func (m *Manager) WriteManifest(lease *Lease) error {
	if lease == nil {
		return nil
	}
	if strings.TrimSpace(lease.ManifestPath) == "" {
		return errors.New("manifest path is required")
	}
	status, err := m.Status(lease)
	if err == nil {
		lease.Dirty = status.Dirty
		lease.ChangedFiles = status.ChangedFiles
	}
	lease.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(lease.ManifestPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lease.ManifestPath, append(data, '\n'), 0o644)
}

// CleanupIfClean removes the worktree only when it has no changes or commits
// since its frozen base. Returns kept=true (with no error) if the worktree was dirty
// and was therefore preserved for the user to inspect.
//
// Ephemeral read-only sub-agents should not leave detritus on disk, but
// anything the worker actually modified must survive so the orchestrator
// or user can review and merge it.
func (m *Manager) CleanupIfClean(wt *Worktree) (kept bool, err error) {
	if wt == nil || wt.Path == "" {
		return false, nil
	}
	dirty, err := m.HasChanges(wt)
	if err != nil {
		return false, err
	}
	if dirty {
		return true, nil
	}
	if cleanupErr := m.Cleanup(wt); cleanupErr != nil {
		return false, cleanupErr
	}
	return false, nil
}

// CleanupSessionIfClean removes each clean worktree in a session group while
// preserving any worktree with user changes or an unreadable Git status.
func (m *Manager) CleanupSessionIfClean(sessionID string) (kept bool, err error) {
	sessionID, err = validIdentityPart("sessionID", sessionID)
	if err != nil {
		return false, err
	}
	worktrees, err := m.List(sessionID)
	if err != nil {
		return false, err
	}
	for _, wt := range worktrees {
		preserved, cleanupErr := m.CleanupIfClean(wt)
		if cleanupErr != nil {
			// A failed status or cleanup check is a reason to keep data, not to
			// fall through to the force-removal path.
			return true, cleanupErr
		}
		kept = kept || preserved
	}
	if kept {
		return true, nil
	}
	dir := filepath.Join(m.rootDir, sessionID)
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		// A concurrently created or otherwise unlisted worktree keeps the group.
		return true, nil
	}
	return false, m.withPrelaunchLock(func() error {
		manifestDir := filepath.Join(m.rootDir, prelaunchManifestDir, sessionID)
		return os.RemoveAll(manifestDir)
	})
}

// CleanupSession removes all worktrees belonging to a session.
func (m *Manager) CleanupSession(sessionID string) error {
	var err error
	sessionID, err = validIdentityPart("sessionID", sessionID)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.rootDir, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entries = nil
	}
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wt := &Worktree{
			Path:      filepath.Join(dir, e.Name()),
			SessionID: sessionID,
			WorkerID:  e.Name(),
		}
		if err := m.Cleanup(wt); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Try to remove the now-empty session directory.
	_ = os.Remove(dir)
	if firstErr != nil {
		return firstErr
	}
	return m.withPrelaunchLock(func() error {
		manifestDir := filepath.Join(m.rootDir, prelaunchManifestDir, sessionID)
		if err := os.RemoveAll(manifestDir); err != nil {
			return fmt.Errorf("remove session worktree manifests: %w", err)
		}
		return nil
	})
}

// List returns all worktrees currently on disk for the given session.
// Note: this scans the filesystem, not git's worktree registry.
func (m *Manager) List(sessionID string) ([]*Worktree, error) {
	dir := filepath.Join(m.rootDir, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Worktree
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wt := &Worktree{
			Path:      filepath.Join(dir, e.Name()),
			SessionID: sessionID,
			WorkerID:  e.Name(),
		}
		manifest, exists, err := readPrelaunchManifest(m.prelaunchManifestPath(sessionID, e.Name()))
		if err != nil {
			return nil, err
		}
		if exists {
			if err := m.validatePrelaunchManifest(manifest, sessionID, e.Name(), wt.Path, OpenOrCreateOptions{BaseRepo: manifest.BaseRepo}); err != nil {
				return nil, err
			}
			wt.HEAD = manifest.BaseRevision
			wt.BaseRepo = manifest.BaseRepo
		}
		out = append(out, wt)
	}
	return out, nil
}

// pruneFromGit asks Git to forget worktrees that no longer exist on disk.
func (m *Manager) pruneFromGit() error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = m.parentRepo
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := strings.TrimSpace(string(out)); detail != "" {
		return fmt.Errorf("git worktree prune: %w: %s", err, detail)
	}
	return fmt.Errorf("git worktree prune: %w", err)
}

func (m *Manager) verifyCleanup(path string) error {
	var consistencyErr error
	if _, err := os.Lstat(path); err == nil {
		consistencyErr = errors.Join(consistencyErr, fmt.Errorf("worktree cleanup incomplete: path still exists on disk: %s", path))
	} else if !errors.Is(err, os.ErrNotExist) {
		consistencyErr = errors.Join(consistencyErr, fmt.Errorf("verify worktree path removal: %w", err))
	}
	registered, err := m.worktreeRegistered(path)
	if err != nil {
		consistencyErr = errors.Join(consistencyErr, err)
	} else if registered {
		consistencyErr = errors.Join(consistencyErr, fmt.Errorf("worktree cleanup incomplete: Git registry still contains %s", path))
	}
	return consistencyErr
}

func (m *Manager) worktreeRegistered(path string) (bool, error) {
	want, err := canonicalWorktreePath(path)
	if err != nil {
		return false, fmt.Errorf("resolve worktree path for registry check: %w", err)
	}
	cmd := exec.Command("git", "worktree", "list", "--porcelain", "-z")
	cmd.Dir = m.parentRepo
	out, err := cmd.CombinedOutput()
	if err != nil {
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return false, fmt.Errorf("list Git worktrees: %w: %s", err, detail)
		}
		return false, fmt.Errorf("list Git worktrees: %w", err)
	}
	for _, field := range bytes.Split(out, []byte{0}) {
		const prefix = "worktree "
		if !bytes.HasPrefix(field, []byte(prefix)) {
			continue
		}
		candidate, resolveErr := canonicalWorktreePath(string(field[len(prefix):]))
		if resolveErr != nil {
			return false, fmt.Errorf("resolve registered worktree path: %w", resolveErr)
		}
		if candidate == want || (runtime.GOOS == "windows" && strings.EqualFold(candidate, want)) {
			return true, nil
		}
	}
	return false, nil
}

// canonicalWorktreePath resolves symlinks through the nearest existing
// ancestor. The worktree itself is often already gone when this runs, while
// parent aliases such as macOS /var -> /private/var still need normalization
// to match Git's registry path.
func canonicalWorktreePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cursor := filepath.Clean(abs)
	suffix := make([]string, 0, 2)
	for {
		if resolved, resolveErr := filepath.EvalSymlinks(cursor); resolveErr == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return filepath.Clean(abs), nil
		}
		suffix = append([]string{filepath.Base(cursor)}, suffix...)
		cursor = parent
	}
}

// ParentRepo returns the absolute path of the source git repository
// this manager was created against.
func (m *Manager) ParentRepo() string {
	return m.parentRepo
}

// IsGitRepo reports whether the given directory is inside a git repo.
func IsGitRepo(dir string) bool {
	return isGitRepo(dir)
}

func isGitRepo(dir string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// resolveHead returns the full sha of the given repo's current HEAD.
func resolveHead(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func checkoutBranch(dir, branch string) error {
	cmd := exec.Command("git", "switch", "-c", branch)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git switch -c %s: %w\n%s", branch, err, out)
	}
	return nil
}

// Path-only callers request ordinary working-tree status. Structured targets
// must retain their creation baseline rather than silently adopting current HEAD.
func worktreeBase(target any) (string, error) {
	var base string
	switch v := target.(type) {
	case *Lease:
		if v != nil {
			base = v.BaseHEAD
		}
	case Lease:
		base = v.BaseHEAD
	case *Worktree:
		if v != nil {
			base = v.HEAD
		}
	case Worktree:
		base = v.HEAD
	case string:
		return "HEAD", nil
	}
	if strings.TrimSpace(base) == "" {
		return "", errors.New("worktree base revision is required")
	}
	return base, nil
}

func worktreePath(target any) string {
	switch v := target.(type) {
	case *Lease:
		if v == nil {
			return ""
		}
		return v.Path
	case Lease:
		return v.Path
	case *Worktree:
		if v == nil {
			return ""
		}
		return v.Path
	case Worktree:
		return v.Path
	case string:
		return v
	default:
		return ""
	}
}

func porcelainFile(line string) string {
	if len(line) < 4 {
		return ""
	}
	return line[3:]
}

func untrackedFiles(lines []string) []string {
	var out []string
	for _, line := range lines {
		if strings.HasPrefix(line, "?? ") {
			if file := porcelainFile(line); file != "" {
				out = append(out, file)
			}
		}
	}
	sort.Strings(out)
	return out
}

func conflictFilesFromGitApply(out []byte) []string {
	scanner := bytes.Split(out, []byte{'\n'})
	seen := map[string]bool{}
	var files []string
	for _, lineBytes := range scanner {
		line := strings.TrimSpace(string(lineBytes))
		if !strings.HasPrefix(line, "error:") {
			continue
		}
		if idx := strings.Index(line, ":"); idx >= 0 {
			rest := strings.TrimSpace(line[idx+1:])
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				file := strings.Trim(fields[0], ":")
				if file != "" && !seen[file] {
					seen[file] = true
					files = append(files, file)
				}
			}
		}
	}
	sort.Strings(files)
	return files
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
