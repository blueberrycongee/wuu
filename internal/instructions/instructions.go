// Package instructions discovers project and user instruction files for the
// session system prompt.
//
// The default path stays Wuu-native and cache-friendly:
//
//   - Wuu user instructions live under the unified wuu home (~/.wuu, or
//     WUU_HOME when set); the legacy ~/.config/wuu directory is still read for
//     backward compatibility.
//   - Shared project instructions take priority within each directory.
//   - Local-only overrides are supported for machine-specific notes.
//   - Legacy cross-harness layouts are opt-in for migration only.
//
// Project root detection walks up from the workspace root looking for marker
// directories (.git, .hg, .jj, .svn).
// Instruction files are only collected between the project root and the
// workspace root, never above the project root. If no marker is found,
// only the workspace root itself contributes.
package instructions

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// File holds one loaded instruction file.
type File struct {
	Path    string // absolute path on disk
	Content string // raw file contents
	Source  string // "user", "project", "local", "claude_auto", etc.
	Name    string // base name (AGENTS.md, CLAUDE.md, ...)
}

// Options configures instruction discovery. Use DefaultOptions() to get
// sensible defaults; callers can override individual fields to extend
// or customize the behavior.
type Options struct {
	// Filenames to look for in each scanned directory, in priority order.
	// Defaults include shared, local override, and legacy instruction names.
	Filenames []string

	// ProjectRootMarkers stop the upward walk through project ancestors.
	// The first directory containing any of these (file or directory)
	// is treated as the project root. Defaults: .git, .hg, .jj, .svn.
	ProjectRootMarkers []string

	// UserDirs are absolute or home-relative directories scanned for
	// user-level instruction files (no hierarchy walk). Empty entries and
	// missing directories are silently skipped. Defaults cover Wuu's
	// unified user home plus the legacy ~/.config/wuu directory. Callers
	// that need WUU_HOME-aware absolute paths (see statepath.UserInstructionDirs)
	// should set this explicitly, since DefaultOptions only knows the
	// home-relative form.
	UserDirs []string

	// Enables import of legacy markdown instruction layouts from Claude-style
	// rules, local files, and auto-memory. It defaults to false; callers
	// should enable it only for explicit migration.
	IncludeLegacyInstructions *bool
}

// DefaultOptions returns the recommended configuration.
func DefaultOptions() Options {
	return Options{
		Filenames:                 []string{"AGENTS.md", "AGENTS.override.md", "CLAUDE.md"},
		ProjectRootMarkers:        []string{".git", ".hg", ".jj", ".svn"},
		UserDirs:                  []string{"~/.wuu", "~/.config/wuu"},
		IncludeLegacyInstructions: boolPtr(false),
	}
}

// Discover scans both the configured user directories and the project
// hierarchy (bounded by project root markers) for instruction files. Files
// are returned in priority order:
//
//  1. User-level files (one per UserDirs entry x Filenames)
//  2. Project files from project root to workspace root. Within one project
//     directory, the preferred shared instruction file wins over legacy names.
//     Local override files are still loaded when present.
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
	if opts.IncludeLegacyInstructions == nil {
		opts.IncludeLegacyInstructions = DefaultOptions().IncludeLegacyInstructions
	}
	includeLegacyInstructions := opts.IncludeLegacyInstructions != nil && *opts.IncludeLegacyInstructions

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
		addInstructionFiles(dir, opts.Filenames, "user", add)
		if includeLegacyInstructions {
			addRulesDir(filepath.Join(dir, "rules"), "user", add)
		}
	}

	// 2. Project hierarchy bounded by project root markers.
	var projectRoot string
	var absRoot string
	if rootDir != "" {
		var err error
		absRoot, err = filepath.Abs(rootDir)
		if err == nil {
			projectRoot = findProjectRoot(absRoot, opts.ProjectRootMarkers)
			dirs := walkBetween(projectRoot, absRoot)
			for _, dir := range dirs {
				addInstructionFiles(dir, opts.Filenames, "project", add)
				if includeLegacyInstructions {
					add(filepath.Join(dir, ".claude", "CLAUDE.md"), "project")
					addRulesDir(filepath.Join(dir, ".claude", "rules"), "project", add)
					add(filepath.Join(dir, "CLAUDE.local.md"), "local")
				}
			}
		}
	}

	if includeLegacyInstructions && absRoot != "" {
		for _, path := range claudeCodeLegacyInstructionPaths(absRoot, projectRoot, homeDir) {
			add(path, "claude_auto")
		}
	}

	return out
}

func addInstructionFiles(dir string, filenames []string, source string, add func(path, source string)) {
	wanted := make(map[string]struct{}, len(filenames))
	for _, name := range filenames {
		wanted[name] = struct{}{}
	}
	_, wantsAgents := wanted["AGENTS.md"]
	hasAgents := wantsAgents && fileExists(filepath.Join(dir, "AGENTS.md"))
	if hasAgents {
		add(filepath.Join(dir, "AGENTS.md"), source)
	}
	if _, ok := wanted["CLAUDE.md"]; ok && !hasAgents {
		add(filepath.Join(dir, "CLAUDE.md"), source)
	}
	if _, ok := wanted["AGENTS.override.md"]; ok {
		add(filepath.Join(dir, "AGENTS.override.md"), source)
	}
	for _, name := range filenames {
		switch name {
		case "AGENTS.md", "CLAUDE.md", "AGENTS.override.md":
			continue
		default:
			add(filepath.Join(dir, name), source)
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func addRulesDir(dir, source string, add func(path, source string)) {
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	for _, path := range paths {
		add(path, source)
	}
}

func claudeCodeLegacyInstructionPaths(absRoot, projectRoot, homeDir string) []string {
	if override := strings.TrimSpace(os.Getenv("CLAUDE_COWORK_MEMORY_PATH_OVERRIDE")); override != "" {
		return []string{filepath.Join(override, "MEMORY.md")}
	}
	base := strings.TrimSpace(os.Getenv("CLAUDE_CODE_REMOTE_MEMORY_DIR"))
	if base == "" {
		base = claudeConfigHomeDir(homeDir)
	}
	if strings.TrimSpace(base) == "" {
		return nil
	}
	projectKeyRoot := projectRoot
	if strings.TrimSpace(projectKeyRoot) == "" {
		projectKeyRoot = absRoot
	}
	if ev, err := filepath.EvalSymlinks(projectKeyRoot); err == nil {
		projectKeyRoot = ev
	}
	projectKeyRoot = filepath.Clean(projectKeyRoot)
	return []string{
		filepath.Join(base, "projects", claudeCodeSanitizePath(projectKeyRoot), "memory", "MEMORY.md"),
	}
}

func claudeConfigHomeDir(homeDir string) string {
	if override := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); override != "" {
		return override
	}
	if strings.TrimSpace(homeDir) == "" {
		return ""
	}
	return filepath.Join(homeDir, ".claude")
}

func claudeCodeSanitizePath(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	const maxSanitizedLength = 200
	if len(out) <= maxSanitizedLength {
		return out
	}
	return out[:maxSanitizedLength] + "-" + base36AbsFNV32(path)
}

func base36AbsFNV32(value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return strings.ToLower(strconv.FormatUint(uint64(h.Sum32()), 36))
}

func boolPtr(v bool) *bool {
	return &v
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
