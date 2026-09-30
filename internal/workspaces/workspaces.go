// Package workspaces reads the user's registered workspace roots — the
// projects added through the desktop sidebar's 添加工作区 entry. The desktop
// main process persists them to <wuuHome>/projects.json; the Go core reads
// the same file as its source of truth for the workspace prompt manifest and
// the file-tool scope whitelist.
package workspaces

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is one registered workspace root.
type Workspace struct {
	ID string `json:"id,omitempty"`
	// PreviousPaths identifies former project roots, not arbitrary session CWDs.
	PreviousPaths []string `json:"previous_paths,omitempty"`
	Name          string   `json:"name"`
	Root          string   `json:"path"`
}

type projectStore struct {
	Projects []Workspace `json:"projects"`
}

// projectsFileName matches the desktop ProjectManager's store file
// (desktop/src/main/projects.ts).
const projectsFileName = "projects.json"

// List returns the registered workspaces from <wuuHome>/projects.json.
// A missing store file means no workspaces yet (nil, nil). Entries with an
// empty path are dropped.
func List(wuuHome string) ([]Workspace, error) {
	wuuHome = strings.TrimSpace(wuuHome)
	if wuuHome == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(wuuHome, projectsFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var store projectStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(store.Projects))
	for _, project := range store.Projects {
		root := strings.TrimSpace(project.Root)
		if root == "" {
			continue
		}
		out = append(out, Workspace{
			ID:            strings.TrimSpace(project.ID),
			PreviousPaths: project.PreviousPaths,
			Name:          strings.TrimSpace(project.Name),
			Root:          root,
		})
	}
	return out, nil
}

// Roots returns just the root paths of the registered workspaces.
func Roots(list []Workspace) []string {
	out := make([]string, 0, len(list))
	for _, ws := range list {
		if root := strings.TrimSpace(ws.Root); root != "" {
			out = append(out, root)
		}
	}
	return out
}

// FileScopeRoots assembles the file-tool whitelist for ordinary turns and
// Scoped participant runs:
// the agent home (the runtime root), every registered workspace root, and
// the system temp directory. A missing or unreadable projects store simply
// contributes no workspace roots.
func FileScopeRoots(homeRoot, wuuHome string) []string {
	roots := make([]string, 0, 4)
	if home := strings.TrimSpace(homeRoot); home != "" {
		roots = append(roots, home)
	}
	if list, err := List(wuuHome); err == nil {
		roots = append(roots, Roots(list)...)
	}
	if tmp := strings.TrimSpace(os.TempDir()); tmp != "" {
		roots = append(roots, tmp)
	}
	return roots
}

// BoundaryRoots returns the complete reachable file scope for a runtime: its
// own root, every registered workspace, the system temp directory, and
// caller-owned extras such as memory notebooks or session artifact dirs.
func BoundaryRoots(runtimeRoot, wuuHome string, extra ...string) []string {
	roots := FileScopeRoots(runtimeRoot, wuuHome)
	for _, root := range extra {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}
