package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const (
	prelaunchManifestDir    = ".prelaunch"
	prelaunchManifestSchema = "wuu/worktree-prelaunch/v1"
)

// ErrPrelaunchIdentityMismatch means an existing durable manifest or Git
// worktree does not belong to the exact launch being recovered. Callers must
// not delete, repair, or take over the path implicitly.
var ErrPrelaunchIdentityMismatch = errors.New("worktree prelaunch identity mismatch")

// ResolvedBase is an immutable source identity for a worktree launch.
type ResolvedBase struct {
	Repo       string
	Revision   string
	Repository string
}

// OpenOrCreateOptions identifies one recoverable prelaunch worktree. An empty
// BaseRevision freezes the current HEAD for a new manifest; when a manifest
// already exists, its frozen revision remains authoritative. Supplying a
// revision requires an exact match on recovery.
type OpenOrCreateOptions struct {
	SessionID    string
	WorkerID     string
	BaseRepo     string
	BaseRevision string
}

type prelaunchManifest struct {
	SchemaVersion string    `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	WorkerID      string    `json:"worker_id"`
	ParentRepo    string    `json:"parent_repo"`
	Repository    string    `json:"repository"`
	BaseRepo      string    `json:"base_repo"`
	BaseRevision  string    `json:"base_revision"`
	TargetPath    string    `json:"target_path"`
	CreatedAt     time.Time `json:"created_at"`
}

// ResolveBase canonicalizes a base repository and resolves revision to a full
// commit ID. An empty base repo uses the Manager parent; an empty revision
// resolves HEAD. The source must belong to the same Git common repository as
// the Manager so Cleanup and registry verification remain authoritative.
func (m *Manager) ResolveBase(baseRepo, revision string) (ResolvedBase, error) {
	if m == nil {
		return ResolvedBase{}, errors.New("worktree manager is required")
	}
	basePath, repository, err := m.resolveBaseRepository(baseRepo)
	if err != nil {
		return ResolvedBase{}, err
	}
	ref := strings.TrimSpace(revision)
	if ref == "" {
		ref = "HEAD"
	}
	commit, err := resolveCommit(basePath, ref)
	if err != nil {
		return ResolvedBase{}, fmt.Errorf("resolve base revision %q in %s: %w", ref, basePath, err)
	}
	return ResolvedBase{Repo: basePath, Revision: commit, Repository: repository}, nil
}

// OpenOrCreate creates a detached worktree from a frozen commit or reopens the
// exact clean prelaunch worktree left by a crashed launcher. It never adopts an
// unmanifested path and never rewrites a mismatching/corrupt manifest.
//
// Create remains the strict fresh-only API for legacy callers. Durable queued
// launchers should migrate to OpenOrCreate, optionally persisting the result of
// ResolveBase and passing its Revision back as BaseRevision.
func (m *Manager) OpenOrCreate(opts OpenOrCreateOptions) (*Worktree, error) {
	if m == nil {
		return nil, errors.New("worktree manager is required")
	}
	sessionID, err := validIdentityPart("sessionID", opts.SessionID)
	if err != nil {
		return nil, err
	}
	workerID, err := validIdentityPart("workerID", opts.WorkerID)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(m.rootDir, sessionID, workerID)
	manifestPath := m.prelaunchManifestPath(sessionID, workerID)

	lock, err := storelock.Acquire(filepath.Join(m.rootDir, prelaunchManifestDir))
	if err != nil {
		return nil, fmt.Errorf("lock worktree prelaunch store: %w", err)
	}
	defer lock.Release()

	manifest, manifestExists, err := readPrelaunchManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if !manifestExists {
		if _, statErr := os.Lstat(target); statErr == nil {
			return nil, identityMismatch("target exists without a durable manifest: %s", target)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect worktree target: %w", statErr)
		}
		base, err := m.ResolveBase(opts.BaseRepo, opts.BaseRevision)
		if err != nil {
			return nil, err
		}
		manifest = prelaunchManifest{
			SchemaVersion: prelaunchManifestSchema,
			SessionID:     sessionID,
			WorkerID:      workerID,
			ParentRepo:    m.parentRepo,
			Repository:    base.Repository,
			BaseRepo:      base.Repo,
			BaseRevision:  base.Revision,
			TargetPath:    target,
			CreatedAt:     time.Now().UTC(),
		}
		if err := writePrelaunchManifest(manifestPath, manifest); err != nil {
			return nil, err
		}
	} else if err := m.validatePrelaunchManifest(manifest, sessionID, workerID, target, opts); err != nil {
		return nil, err
	}

	info, statErr := os.Lstat(target)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if err := m.createFrozenWorktree(manifest); err != nil {
			return nil, err
		}
	case statErr != nil:
		return nil, fmt.Errorf("inspect worktree target: %w", statErr)
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		return nil, identityMismatch("target is not a real directory: %s", target)
	}
	if err := m.verifyPrelaunchWorktree(manifest); err != nil {
		return nil, err
	}
	return &Worktree{
		Path:         target,
		SessionID:    sessionID,
		WorkerID:     workerID,
		HEAD:         manifest.BaseRevision,
		BaseRepo:     manifest.BaseRepo,
		ManifestPath: manifestPath,
	}, nil
}

func (m *Manager) validatePrelaunchManifest(manifest prelaunchManifest, sessionID, workerID, target string, opts OpenOrCreateOptions) error {
	if manifest.SchemaVersion != prelaunchManifestSchema {
		return identityMismatch("unsupported manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.SessionID != sessionID {
		return identityMismatch("manifest session %q does not match %q", manifest.SessionID, sessionID)
	}
	if manifest.WorkerID != workerID {
		return identityMismatch("manifest worker %q does not match %q", manifest.WorkerID, workerID)
	}
	if manifest.CreatedAt.IsZero() || strings.TrimSpace(manifest.BaseRevision) == "" {
		return identityMismatch("manifest is missing required prelaunch identity")
	}
	if !sameCanonicalPath(manifest.ParentRepo, m.parentRepo) {
		return identityMismatch("manifest parent repository %q does not match %q", manifest.ParentRepo, m.parentRepo)
	}
	if !sameCanonicalPath(manifest.TargetPath, target) {
		return identityMismatch("manifest target %q does not match %q", manifest.TargetPath, target)
	}
	basePath, repository, err := m.resolveBaseRepository(opts.BaseRepo)
	if err != nil {
		return err
	}
	if !sameCanonicalPath(manifest.BaseRepo, basePath) {
		return identityMismatch("manifest base repository %q does not match %q", manifest.BaseRepo, basePath)
	}
	if !sameCanonicalPath(manifest.Repository, repository) {
		return identityMismatch("manifest Git repository %q does not match %q", manifest.Repository, repository)
	}
	resolvedManifestRevision, err := resolveCommit(basePath, manifest.BaseRevision)
	if err != nil {
		return identityMismatch("manifest base revision %q is unavailable: %v", manifest.BaseRevision, err)
	}
	if resolvedManifestRevision != manifest.BaseRevision {
		return identityMismatch("manifest base revision %q does not resolve exactly", manifest.BaseRevision)
	}
	if requested := strings.TrimSpace(opts.BaseRevision); requested != "" {
		resolved, err := resolveCommit(basePath, requested)
		if err != nil {
			return fmt.Errorf("resolve requested base revision %q: %w", requested, err)
		}
		if resolved != manifest.BaseRevision {
			return identityMismatch("requested base revision %q does not match frozen revision %q", resolved, manifest.BaseRevision)
		}
	}
	return nil
}

func (m *Manager) createFrozenWorktree(manifest prelaunchManifest) error {
	if err := os.MkdirAll(filepath.Dir(manifest.TargetPath), 0o755); err != nil {
		return fmt.Errorf("create worktree parent dir: %w", err)
	}
	cmd := exec.Command("git", "worktree", "add", "--detach", manifest.TargetPath, manifest.BaseRevision)
	cmd.Dir = manifest.BaseRepo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add frozen revision failed: %w\n%s", err, out)
	}
	return nil
}

func (m *Manager) verifyPrelaunchWorktree(manifest prelaunchManifest) error {
	registered, err := m.worktreeRegistered(manifest.TargetPath)
	if err != nil {
		return err
	}
	if !registered {
		return identityMismatch("target is not registered as a Git worktree: %s", manifest.TargetPath)
	}
	top, err := gitPathOutput(manifest.TargetPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return identityMismatch("target is not a valid Git worktree: %v", err)
	}
	if !sameCanonicalPath(top, manifest.TargetPath) {
		return identityMismatch("Git worktree root %q does not match target %q", top, manifest.TargetPath)
	}
	repository, err := gitCommonDirectory(manifest.TargetPath)
	if err != nil {
		return identityMismatch("resolve target Git repository: %v", err)
	}
	if !sameCanonicalPath(repository, manifest.Repository) {
		return identityMismatch("target Git repository %q does not match manifest %q", repository, manifest.Repository)
	}
	head, err := resolveCommit(manifest.TargetPath, "HEAD")
	if err != nil {
		return identityMismatch("resolve target HEAD: %v", err)
	}
	if head != manifest.BaseRevision {
		return identityMismatch("target HEAD %q does not match frozen revision %q", head, manifest.BaseRevision)
	}
	branch, err := gitPathOutput(manifest.TargetPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return identityMismatch("inspect target HEAD attachment: %v", err)
	}
	if strings.TrimSpace(branch) != "HEAD" {
		return identityMismatch("target is attached to branch %q instead of detached HEAD", branch)
	}
	status, err := gitPathOutput(manifest.TargetPath, "status", "--porcelain")
	if err != nil {
		return identityMismatch("inspect target status: %v", err)
	}
	if strings.TrimSpace(status) != "" {
		return identityMismatch("target contains changes and is no longer a reusable prelaunch worktree")
	}
	return nil
}

func (m *Manager) resolveBaseRepository(baseRepo string) (string, string, error) {
	source := strings.TrimSpace(baseRepo)
	if source == "" {
		source = m.parentRepo
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", "", fmt.Errorf("resolve base repo: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", fmt.Errorf("inspect base repo %s: %w", abs, err)
	}
	if !info.IsDir() || !isGitRepo(abs) {
		return "", "", fmt.Errorf("base repo is not a Git worktree: %s", abs)
	}
	basePath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize base repo: %w", err)
	}
	repository, err := gitCommonDirectory(basePath)
	if err != nil {
		return "", "", err
	}
	parentRepository, err := gitCommonDirectory(m.parentRepo)
	if err != nil {
		return "", "", err
	}
	if !sameCanonicalPath(repository, parentRepository) {
		return "", "", identityMismatch("base repo %q belongs to Git repository %q, want %q", basePath, repository, parentRepository)
	}
	return filepath.Clean(basePath), filepath.Clean(repository), nil
}

func gitCommonDirectory(dir string) (string, error) {
	value, err := gitPathOutput(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory for %s: %w", dir, err)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(dir, value)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func resolveCommit(dir, revision string) (string, error) {
	ref := strings.TrimSpace(revision)
	if ref == "" {
		return "", errors.New("revision is required")
	}
	return gitPathOutput(dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
}

func gitPathOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func readPrelaunchManifest(path string) (prelaunchManifest, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return prelaunchManifest{}, false, nil
	}
	if err != nil {
		return prelaunchManifest{}, false, fmt.Errorf("read worktree prelaunch manifest: %w", err)
	}
	var manifest prelaunchManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return prelaunchManifest{}, false, identityMismatch("decode manifest %s: %v", path, err)
	}
	return manifest, true, nil
}

func writePrelaunchManifest(path string, manifest prelaunchManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode worktree prelaunch manifest: %w", err)
	}
	if err := securefs.WriteFileAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write worktree prelaunch manifest: %w", err)
	}
	return nil
}

func (m *Manager) prelaunchManifestPath(sessionID, workerID string) string {
	return filepath.Join(m.rootDir, prelaunchManifestDir, sessionID, workerID+".json")
}

func (m *Manager) withPrelaunchLock(work func() error) error {
	lock, err := storelock.Acquire(filepath.Join(m.rootDir, prelaunchManifestDir))
	if err != nil {
		return fmt.Errorf("lock worktree prelaunch store: %w", err)
	}
	defer lock.Release()
	return work()
}

func (m *Manager) removePrelaunchManifest(wt *Worktree) error {
	manifestPath, ok := m.manifestPathForWorktree(wt)
	if !ok {
		return nil
	}
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove worktree prelaunch manifest: %w", err)
	}
	_ = os.Remove(filepath.Dir(manifestPath))
	return nil
}

func (m *Manager) manifestPathForWorktree(wt *Worktree) (string, bool) {
	if m == nil || wt == nil || strings.TrimSpace(wt.Path) == "" {
		return "", false
	}
	rel, err := filepath.Rel(m.rootDir, wt.Path)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return "", false
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	if len(parts) != 2 || parts[0] == ".." || parts[0] == prelaunchManifestDir {
		return "", false
	}
	sessionID, err := validIdentityPart("sessionID", parts[0])
	if err != nil {
		return "", false
	}
	workerID, err := validIdentityPart("workerID", parts[1])
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(wt.SessionID) != "" && wt.SessionID != sessionID {
		return "", false
	}
	if strings.TrimSpace(wt.WorkerID) != "" && wt.WorkerID != workerID {
		return "", false
	}
	path := m.prelaunchManifestPath(sessionID, workerID)
	if strings.TrimSpace(wt.ManifestPath) != "" && !sameCanonicalPath(wt.ManifestPath, path) {
		return "", false
	}
	return path, true
}

func validIdentityPart(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if value == "." || value == ".." || filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%s contains an invalid path component: %q", label, value)
	}
	return value, nil
}

func sameCanonicalPath(left, right string) bool {
	leftPath, leftErr := canonicalWorktreePath(left)
	rightPath, rightErr := canonicalWorktreePath(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftPath, rightPath)
	}
	return leftPath == rightPath
}

func identityMismatch(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPrelaunchIdentityMismatch, fmt.Sprintf(format, args...))
}
