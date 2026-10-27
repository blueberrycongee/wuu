package appserver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

const orphanWorktreeMinimumAge = 10 * time.Minute

// cleanupOrphanWorktrees removes managed worktree groups whose owning thread
// no longer exists. Live threads, reviewable completed threads, and any group
// with a pending terminal recovery record are preserved.
func (s *Server) cleanupOrphanWorktrees(binding *runtime.State) (int, error) {
	if s == nil || s.rt == nil {
		return 0, nil
	}
	stateDir := strings.TrimSpace(s.rt.StateDir)
	if stateDir == "" {
		return 0, nil
	}
	root := statepath.WorktreeRoot(stateDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read managed worktree root: %w", err)
	}
	manager, err := s.worktreeManager(s.rt.RootDir)
	if err != nil {
		return 0, nil
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "manifests" {
			continue
		}
		ownerID := strings.TrimSpace(entry.Name())
		if ownerID == "" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || time.Since(info.ModTime()) < orphanWorktreeMinimumAge {
			continue
		}
		if _, found, findErr := binding.Sessions.Find(ownerID); findErr != nil {
			return removed, fmt.Errorf("inspect worktree owner %q: %w", ownerID, findErr)
		} else if found {
			continue
		}
		harnessDir := filepath.Join(statepath.SessionArtifactDir(stateDir, ownerID), "harness")
		pending, pendingErr := agentcontrol.WorkerTerminalFinalizationsPending(harnessDir)
		if pendingErr != nil || pending {
			continue
		}
		// Worktree creation precedes the session row commit. Recheck after the
		// filesystem inspections so a concurrently committed owner is preserved.
		if _, found, findErr := binding.Sessions.Find(ownerID); findErr != nil {
			return removed, fmt.Errorf("reinspect worktree owner %q: %w", ownerID, findErr)
		} else if found {
			continue
		}
		kept, cleanupErr := manager.CleanupSessionIfClean(ownerID)
		if cleanupErr != nil || kept {
			// Dirty worktrees and status/cleanup failures are both preservation
			// conditions for automatic orphan collection.
			continue
		}
		removed++
	}
	return removed, nil
}
