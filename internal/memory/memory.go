// Package memory discovers and loads project / user memory files
// (AGENTS.md, AGENTS.override.md, CLAUDE.md) so they can be injected
// into the system prompt at session start.
//
// The design intentionally avoids locking wuu into a single ecosystem:
//
//   - AGENTS.md is the cross-tool convention (codex, GitHub, Cursor,
//     and others use it). It takes priority by default.
//   - AGENTS.override.md is the local-only override (gitignored), same
//     idea as Codex's LOCAL_PROJECT_DOC_FILENAME.
//   - CLAUDE.md is supported for compatibility with Claude Code users.
//   - User-level memory is read from ~/.config/wuu/, ~/.claude/, and
//     ~/.codex/ so existing CC or Codex users get their files picked
//     up automatically.
//
// Project root detection follows Codex's approach: walk up from the
// workspace root looking for marker directories (.git, .hg, .jj, .svn).
// Memory files are only collected between the project root and the
// workspace root, never above the project root. If no marker is found,
// only the workspace root itself contributes.
package memory

import (
	"os"
	"path/filepath"
)

// File holds one loaded memory file.
type File struct {
	Path    string // absolute path on disk
	Content string // raw file contents
	Source  string // "user" or "project"
	Name    string // base name (AGENTS.md, CLAUDE.md, ...)
}

// Options configures memory discovery. Use DefaultOptions() to get
// sensible defaults; callers can override individual fields to extend
// or customize the behavior.
type Options struct {
	// Filenames to look for in each scanned directory, in priority order.
	// Defaults: AGENTS.md, AGENTS.override.md, CLAUDE.md.
	Filenames []string

	// ProjectRootMarkers stop the upward walk through project ancestors.
	// The first directory containing any of these (file or directory)
	// is treated as the project root. Defaults: .git, .hg, .jj, .svn.
	ProjectRootMarkers []string

	// UserDirs are absolute or home-relative directories scanned for
	// user-level memory files (no hierarchy walk). Empty entries and
	// missing directories are silently skipped. Defaults expand to
	// ~/.config/wuu, ~/.claude, ~/.codex.
	UserDirs []string
}

// DefaultOptions returns the recommended configuration.
func DefaultOptions() Options {
	return Options{
		Filenames:          []string{"AGENTS.md", "AGENTS.override.md", "CLAUDE.md"},
		ProjectRootMarkers: []string{".git", ".hg", ".jj", ".svn"},
		UserDirs:           []string{"~/.config/wuu", "~/.claude", "~/.codex"},
	}
}

// Discover scans both the configured user directories and the project
// hierarchy (bounded by project root markers) for memory files. Files
// are returned in priority order:
//
//  1. User-level files (one per UserDirs entry × Filenames)
//  2. Project files from project root → workspace root
//
// Files are deduplicated by absolute path so the same file is never
// counted twice when user dirs and the project tree overlap.
func Discover(rootDir, homeDir string, opts Options) []File {
	if len(opts.Filenames) == 0 {
		opts.Filenames = DefaultOptions().Filenames
	}
	if opts.ProjectRootMarkers == nil {
		opts.ProjectRootMarkers = DefaultOptions().ProjectRootMarkers
	}
	if opts.UserDirs == nil {
		opts.UserDirs = DefaultOptions().UserDirs
	}

	var out []File
	seen := make(map[string]struct{})

	add := func(path, source string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, File{
			Path:    abs,
			Content: string(data),
			Source:  source,
			Name:    filepath.Base(abs),
		})
	}

	// 1. User dirs.
	for _, ud := range opts.UserDirs {
		dir := expandHome(ud, homeDir)
		if dir == "" {
			continue
		}
		for _, name := range opts.Filenames {
			add(filepath.Join(dir, name), "user")
		}
	}

	// 2. Project hierarchy bounded by project root markers.
	if rootDir != "" {
		absRoot, err := filepath.Abs(rootDir)
		if err == nil {
			projectRoot := findProjectRoot(absRoot, opts.ProjectRootMarkers)
			dirs := walkBetween(projectRoot, absRoot)
			for _, dir := range dirs {
				for _, name := range opts.Filenames {
					add(filepath.Join(dir, name), "project")
				}
			}
		}
	}

	return out
}

// findProjectRoot walks up from start looking for any of the marker
// names. Returns the directory containing the marker, or empty string
// if no marker is found.
func findProjectRoot(start string, markers []string) string {
	cur := start
	for {
		for _, m := range markers {
			if _, err := os.Lstat(filepath.Join(cur, m)); err == nil {
				return cur
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// walkBetween returns the chain of directories from root down to leaf,
// inclusive on both ends. If root is empty, only leaf is returned.
// If leaf is not under root, only leaf is returned.
func walkBetween(root, leaf string) []string {
	if root == "" {
		return []string{leaf}
	}
	if !isDescendantOrSelf(leaf, root) {
		return []string{leaf}
	}
	var dirs []string
	cur := leaf
	for {
		dirs = append(dirs, cur)
		if cur == root {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	// Reverse so root is first.
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}
	return dirs
}

// isDescendantOrSelf returns true if path equals base or is a descendant
// of base.
func isDescendantOrSelf(path, base string) bool {
	if path == base {
		return true
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' {
		return false
	}
	return true
}

// expandHome replaces a leading ~ with homeDir. Returns empty string if
// the input is empty or homeDir is empty when needed.
func expandHome(path, homeDir string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		return homeDir
	}
	if len(path) > 1 && path[0] == '~' && (path[1] == '/' || path[1] == filepath.Separator) {
		if homeDir == "" {
			return ""
		}
		return filepath.Join(homeDir, path[2:])
	}
	return path
}
