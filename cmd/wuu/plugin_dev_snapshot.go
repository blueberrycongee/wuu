package main

import (
	"crypto/sha256"
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
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && (devWatchIgnoredDirs[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
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
