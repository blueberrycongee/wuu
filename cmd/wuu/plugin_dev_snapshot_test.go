package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotPluginSourceSkipsIgnoredAndHiddenDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePluginDevTestFile(t, filepath.Join(root, "plugin.json"), `{"id":"x"}`)
	writePluginDevTestFile(t, filepath.Join(root, "src", "index.ts"), "export {}")
	writePluginDevTestFile(t, filepath.Join(root, "dist", "index.js"), "ignored")
	writePluginDevTestFile(t, filepath.Join(root, ".hidden", "x"), "ignored")

	snapshot := snapshotPluginSource(root)

	if _, ok := snapshot["plugin.json"]; !ok {
		t.Fatalf("root manifest missing from snapshot: %v", snapshot)
	}
	if _, ok := snapshot["src/index.ts"]; !ok {
		t.Fatalf("source file missing from snapshot: %v", snapshot)
	}
	if _, ok := snapshot["dist/index.js"]; ok {
		t.Fatalf("dist output should be ignored: %v", snapshot)
	}
	if _, ok := snapshot[".hidden/x"]; ok {
		t.Fatalf("hidden directory should be ignored: %v", snapshot)
	}
}

func TestChangedPluginSourcePathsReportsAddedChangedRemoved(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	before := pluginSourceSnapshot{
		"a.ts":       {ModTime: base, Size: 1},
		"b.ts":       {ModTime: base, Size: 2},
		"removed.ts": {ModTime: base, Size: 1},
	}
	after := pluginSourceSnapshot{
		"a.ts": {ModTime: base.Add(time.Second), Size: 1},
		"b.ts": {ModTime: base, Size: 3},
		"c.ts": {ModTime: base, Size: 1},
	}

	got := changedPluginSourcePaths(before, after)
	want := []string{"a.ts", "b.ts", "c.ts", "removed.ts"}
	if len(got) != len(want) {
		t.Fatalf("changed paths = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("changed paths = %v, want %v", got, want)
		}
	}
}
