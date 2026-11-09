package statepath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SessionArtifactStateDirs discovers every historical root before migrating or
// deleting session-owned state. It refuses symlink boundaries and keeps custom
// session stores isolated from the global workspace catalog.
func SessionArtifactStateDirs(home, sessionsDir, own, threadID string) ([]string, error) {
	if threadID == "." || threadID == ".." || filepath.Base(threadID) != threadID {
		return nil, errors.New("invalid thread id for artifact lookup")
	}
	var err error
	stateDirs := []string{own}
	home = strings.TrimSpace(home)
	if home == "" {
		home, err = Home("")
		if err != nil {
			return nil, err
		}
	}
	// Custom stores may reuse IDs from the global store. Their delete request
	// must never inspect or reclaim another store's workspace artifacts.
	if filepath.Clean(sessionsDir) != filepath.Clean(SessionsDir(home)) {
		return stateDirs, nil
	}
	workspaceRoot := filepath.Join(home, "workspaces")
	rootInfo, err := os.Lstat(workspaceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return stateDirs, nil
	}
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("workspace artifact root is not a directory: %s", workspaceRoot)
	}
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("workspace artifact path is a symbolic link: %s", filepath.Join(workspaceRoot, entry.Name()))
		}
		if !entry.IsDir() {
			continue
		}
		stateDir := filepath.Join(workspaceRoot, entry.Name())
		matched := false
		for _, name := range []string{"sessions", "worktrees"} {
			root := filepath.Join(stateDir, name)
			for _, path := range []string{root, filepath.Join(root, threadID)} {
				info, err := os.Lstat(path)
				if errors.Is(err, os.ErrNotExist) {
					break
				}
				if err != nil {
					return nil, err
				}
				// Lstat refuses links at both boundaries before reading contents.
				if !info.IsDir() {
					return nil, fmt.Errorf("thread artifact path is not a directory: %s", path)
				}
				if path != root {
					matched = true
				}
			}
		}
		if matched && filepath.Clean(stateDir) != filepath.Clean(own) {
			stateDirs = append(stateDirs, stateDir)
		}
	}
	return stateDirs, nil
}
