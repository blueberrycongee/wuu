package appserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/workspaces"
)

func (s *Server) registeredWorkspaces() []workspaces.Workspace {
	if s == nil || s.rt == nil {
		return nil
	}
	list, err := workspaces.List(s.rt.WuuHome)
	if err != nil {
		providers.DebugLogf("read registered workspaces: %v", err)
		return nil
	}
	return list
}

// Resolve a project binding independently of the calling identity's home.
// Resolving metadata does not authorize execution in this server's runtime.
func (s *Server) resolveSessionWorkspace(id, root string) (string, string, error) {
	id, root = strings.TrimSpace(id), strings.TrimSpace(root)
	if s == nil || s.rt == nil {
		return "", "", errors.New("session runtime unavailable")
	}
	if id == "" && root == "" {
		return s.rt.RootDir, s.rt.WorkspaceID, nil
	}
	if root != "" && !filepath.IsAbs(root) {
		return "", "", errors.New("workspace_root must be absolute")
	}
	match := func(candidateID, candidateRoot string) bool {
		return (id == "" || id == candidateID) && (root == "" || sessionWorkspacePath(root) == sessionWorkspacePath(candidateRoot))
	}
	currentRegistered := false
	for _, workspace := range s.registeredWorkspaces() {
		currentRegistered = currentRegistered || workspace.ID != "" && workspace.ID == s.rt.WorkspaceID
		if match(workspace.ID, workspace.Root) {
			return availableSessionWorkspace(workspace.Root, workspace.ID)
		}
	}
	// Standalone hosts may explicitly bind a workspace without a desktop
	// registry. A registered project's current path wins over stale hosts.
	if !currentRegistered && match(s.rt.WorkspaceID, s.rt.RootDir) {
		return availableSessionWorkspace(s.rt.RootDir, s.rt.WorkspaceID)
	}
	return "", "", errors.New("workspace must be the current or a registered project; workspace_id and workspace_root must agree")
}

func availableSessionWorkspace(root, id string) (string, string, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("workspace %q is unavailable", root)
	}
	return filepath.Clean(root), id, nil
}

// The persisted workspace ID owns configuration and state; CWD is the execution
// directory and may be a subdirectory or a local fork's shared worktree.
func (s *Server) sessionWorkspace(m session.Session) (string, string, error) {
	if strings.TrimSpace(m.WorkspaceID) != "" {
		return s.resolveSessionWorkspace(m.WorkspaceID, m.WorktreeBaseRepo)
	}
	return s.resolveSessionWorkspace(m.WorkspaceID, firstNonEmpty(m.WorktreeBaseRepo, m.CWD))
}

func (s *Server) ownsSessionWorkspace(root, id string) bool {
	return sessionWorkspacePath(s.rt.RootDir) == sessionWorkspacePath(root) && s.rt.WorkspaceID == id
}

func sessionWorkspacePath(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return filepath.Clean(root)
}
