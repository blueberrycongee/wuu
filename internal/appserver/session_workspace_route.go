package appserver

import (
	"errors"
	"path/filepath"
	"strings"
)

// Every thread derives its runtime from its own CWD. Explicit workspace
// selection therefore works even when the calling identity lives elsewhere.
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
		return (id == "" || id == candidateID) && (root == "" || filepath.Clean(root) == filepath.Clean(candidateRoot))
	}
	if match(s.rt.WorkspaceID, s.rt.RootDir) {
		return s.rt.RootDir, s.rt.WorkspaceID, nil
	}
	for _, workspace := range s.registeredWorkspaces() {
		if match(workspace.ID, workspace.Root) {
			return workspace.Root, workspace.ID, nil
		}
	}
	return "", "", errors.New("workspace must be the current or a registered project; workspace_id and workspace_root must agree")
}
