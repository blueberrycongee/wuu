package appserver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
)

func (s *Server) rebindThreadWorkspace(threadID, requestedRoot string) error {
	if s == nil || s.rt == nil {
		return errors.New("runtime session is required")
	}
	if s.thread(threadID) == nil {
		return session.ErrSessionNotFound
	}
	current, found, err := session.Find(s.rt.SessionDir, threadID)
	if err != nil {
		return err
	}
	if !found {
		return session.ErrSessionNotFound
	}
	if current.Source == fusionSideSource {
		return errors.New("the Fusion Lead owns the pair's shared workspace")
	}
	var sides []session.Session
	if current.Fusion != nil {
		// Lead may call this tool during its own turn. Hold creation and the
		// idle Side's execution lease, rather than rejecting the running Lead.
		s.sessionCreateMu.Lock()
		defer s.sessionCreateMu.Unlock()
		sides, err = s.fusionSides(threadID)
		if err != nil {
			return err
		}
		for _, side := range sides {
			_, release, err := s.beginThreadRuntimeSelectionMutation(side.ID)
			if err != nil {
				return fmt.Errorf("Sidekick must be idle before moving the Fusion workspace: %w", err)
			}
			defer release()
		}
	}
	baseRoot, err := canonicalWorkspaceDirectory(s.rt.RootDir)
	if err != nil {
		return fmt.Errorf("resolve project workspace: %w", err)
	}
	targetRoot, err := canonicalWorkspaceDirectory(requestedRoot)
	if err != nil {
		return fmt.Errorf("resolve session workspace: %w", err)
	}

	worktreePath := ""
	worktreeBaseHEAD := ""
	worktreeBaseRepo := ""
	baseCommonDir, err := gitAbsolutePath(baseRoot, "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve project Git repository: %w", err)
	}
	targetTopLevel, err := gitAbsolutePath(targetRoot, "--show-toplevel")
	if err != nil {
		return fmt.Errorf("resolve session Git worktree: %w", err)
	}
	if targetTopLevel != canonicalGitPath(targetRoot) {
		return errors.New("session workspace must be a Git worktree root")
	}
	targetCommonDir, err := gitAbsolutePath(targetRoot, "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve session Git repository: %w", err)
	}
	if targetCommonDir != baseCommonDir {
		return errors.New("session workspace must be a linked worktree of the current project")
	}
	if canonicalGitPath(targetRoot) == canonicalGitPath(baseRoot) {
		// Moving back to the project, possibly named through a symlink: keep
		// the path the project is registered with.
		targetRoot = baseRoot
	} else {
		worktreeBaseHEAD, err = gitText(targetRoot, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("resolve session worktree HEAD: %w", err)
		}
		worktreePath = targetRoot
		worktreeBaseRepo = baseRoot
	}

	metadata, err := session.UpdateWorkspaceBinding(
		s.rt.SessionDir,
		threadID,
		targetRoot,
		worktreePath,
		worktreeBaseHEAD,
		worktreeBaseRepo,
	)
	if err != nil {
		return err
	}
	thread, err := s.threadAfterMetadataUpdate(metadata)
	if err != nil {
		// Child-agent enrichment is presentation data. The returned thread
		// already contains the persisted workspace metadata, so keep the
		// runtime move successful and notify with that partial snapshot.
		providers.DebugLogf("enrich rebound thread %q: %v", threadID, err)
	}
	if err := s.notifyThreadUpdated(thread); err != nil {
		// Persistence and the authoritative in-memory thread already moved.
		// A broken output stream must not report the operation as rejected and
		// leave the executing toolkit on its old root.
		providers.DebugLogf("notify rebound thread %q: %v", threadID, err)
	}
	for _, side := range sides {
		side.CWD, side.WorktreePath = metadata.CWD, metadata.WorktreePath
		side.WorktreeBaseHEAD, side.WorktreeBaseRepo = metadata.WorktreeBaseHEAD, metadata.WorktreeBaseRepo
		thread, err := s.threadAfterMetadataUpdate(side)
		if err != nil {
			providers.DebugLogf("enrich rebound Sidekick %q: %v", side.ID, err)
		}
		if err := s.notifyThreadUpdated(thread); err != nil {
			providers.DebugLogf("notify rebound Sidekick %q: %v", side.ID, err)
		}
	}
	return nil
}

func canonicalWorkspaceDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace path must be a directory")
	}
	return filepath.Clean(abs), nil
}

func gitAbsolutePath(cwd, arg string) (string, error) {
	value, err := gitText(cwd, "rev-parse", "--path-format=absolute", arg)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(cwd, value)
	}
	return canonicalGitPath(value), nil
}

func gitText(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", errors.New("git returned an empty path")
	}
	return value, nil
}

func canonicalGitPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}
