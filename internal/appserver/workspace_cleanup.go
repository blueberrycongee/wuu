package appserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// workspaceStateArchiveDirName receives durable extension data that is not
// owned by the core runtime. It is skipped by repeated cleanup sweeps.
const workspaceStateArchiveDirName = ".archived"

// workspaceCoreTransientDirNames are directories whose complete lifecycle is
// owned by the reusable runtime. Unknown top-level directories are archived:
// this preserves current and future plugin data without teaching cleanup any
// product-specific directory names.
var workspaceCoreTransientDirNames = map[string]bool{
	"runtime":   true,
	"sessions":  true,
	"shared":    true,
	"worktrees": true,
}

// handleWorkspaceStateCleanup reclaims the local state of a workspace the
// user has removed from the desktop sidebar: everything under the workspace
// state directory is deleted except durable extension directories, which move
// into .archived/. The serving instance refuses to clean its own active
// workspace; the desktop only issues this call for removed projects, whose
// state dirs are addressed by stable id (or path for legacy path-keyed
// dirs) rather than by the caller's runtime context.
func (s *Server) handleWorkspaceStateCleanup(req Request) error {
	var params WorkspaceStateCleanupParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	home, err := statepath.Home("")
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var stateDir string
	switch {
	case strings.TrimSpace(params.WorkspaceID) != "":
		stateDir, err = statepath.WorkspaceDirByID(home, params.WorkspaceID)
	case strings.TrimSpace(params.WorkspacePath) != "":
		stateDir, err = statepath.WorkspaceDir(home, params.WorkspacePath)
	default:
		err = errors.New("workspace_id or workspace_path is required")
	}
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if own, ownErr := s.workspaceStateDir(); ownErr == nil && filepath.Clean(own) == filepath.Clean(stateDir) {
		return s.writeResponse(req.ID, nil, errors.New("cannot clean up the active workspace's state directory"))
	}
	result, err := cleanupWorkspaceStateDir(stateDir)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, result, nil)
}

// cleanupWorkspaceStateDir deletes core-owned transient directories and loose
// runtime files. Other top-level directories are extension-owned or legacy
// durable data and are archived. A missing state directory is a no-op.
func cleanupWorkspaceStateDir(stateDir string) (WorkspaceStateCleanupResult, error) {
	result := WorkspaceStateCleanupResult{StateDir: stateDir}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("read workspace state dir: %w", err)
	}
	archiveRoot := filepath.Join(stateDir, workspaceStateArchiveDirName)
	for _, entry := range entries {
		name := entry.Name()
		if name == workspaceStateArchiveDirName {
			continue
		}
		path := filepath.Join(stateDir, name)
		if entry.IsDir() && !workspaceCoreTransientDirNames[name] {
			if err := archiveWorkspaceDataDir(archiveRoot, path, name); err != nil {
				return result, err
			}
			result.DataArchived = true
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return result, fmt.Errorf("remove %s: %w", name, err)
		}
	}
	result.Removed = true
	return result, nil
}

func archiveWorkspaceDataDir(archiveRoot, path, name string) error {
	if err := os.MkdirAll(archiveRoot, securefs.DirMode); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	target := filepath.Join(archiveRoot, name)
	if _, err := os.Stat(target); err == nil {
		// A previous cleanup already archived this directory; keep both
		// snapshots by suffixing the new one with a UTC timestamp.
		target = filepath.Join(archiveRoot, name+"-"+time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.Rename(path, target); err != nil {
		return fmt.Errorf("archive %s: %w", name, err)
	}
	return nil
}
