package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// pluginSourceFile identifies bytes rather than timestamps: atomic editors can
// preserve modification times, and touching unchanged source should not rebuild.
type pluginSourceFile [sha256.Size]byte

// pluginSourceSnapshot maps slash-normalized package-relative paths to their
// current file identity. Ignored directories are excluded so build output and
// dependencies never show up as source changes.
type pluginSourceSnapshot map[string]pluginSourceFile

func snapshotPluginSource(root string) pluginSourceSnapshot {
	out := make(pluginSourceSnapshot)
	roots, err := pluginDevSourceRoots(root)
	if err != nil {
		roots = []string{root}
	}
	for _, input := range roots {
		_ = filepath.WalkDir(input, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				if path != input && (devWatchIgnoredDirs[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			out[filepath.ToSlash(rel)] = sha256.Sum256(content)
			return nil
		})
	}
	return out
}

// changedPluginSourcePaths returns the sorted package-relative paths that were
// added, removed, or changed between two snapshots.
func changedPluginSourcePaths(before, after pluginSourceSnapshot) []string {
	var out []string
	for rel, current := range after {
		previous, ok := before[rel]
		if !ok || previous != current {
			out = append(out, rel)
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// pluginDevSourceRoots includes explicitly declared monorepo build inputs.
// These development-only paths do not become runtime plugin permissions.
func pluginDevSourceRoots(root string) ([]string, error) {
	roots := []string{root}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return roots, nil
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if os.IsNotExist(err) {
		return roots, nil
	}
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Wuu struct {
			Dev struct {
				Watch []string `json:"watch"`
			} `json:"dev"`
		} `json:"wuu"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, fmt.Errorf("development watch metadata: %w", err)
	}
	if len(pkg.Wuu.Dev.Watch) > 64 {
		return nil, fmt.Errorf("development watch supports at most 64 additional inputs")
	}
	seen := map[string]bool{filepath.Clean(root): true}
	for _, relative := range pkg.Wuu.Dev.Watch {
		if strings.TrimSpace(relative) == "" || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
			return nil, fmt.Errorf("development watch input must be a relative path: %q", relative)
		}
		path := filepath.Clean(filepath.Join(root, relative))
		if seen[path] {
			continue
		}
		// Watching an ancestor includes unrelated projects and generated outputs.
		if rel, err := filepath.Rel(path, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("development watch input cannot be a package ancestor: %q", relative)
		}
		common := root
		for {
			rel, err := filepath.Rel(common, path)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				break
			}
			parent := filepath.Dir(common)
			if parent == common {
				break
			}
			common = parent
		}
		for current := path; current != common; current = filepath.Dir(current) {
			entry, err := os.Lstat(current)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if err == nil && entry.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("development watch input must not follow a symlink: %q", relative)
			}
		}
		seen[path] = true
		roots = append(roots, path)
	}
	return roots, nil
}
