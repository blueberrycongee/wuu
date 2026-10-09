package main

import (
	"path/filepath"
	"testing"
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

	before := pluginSourceSnapshot{
		"a.ts":       {1},
		"b.ts":       {2},
		"removed.ts": {1},
	}
	after := pluginSourceSnapshot{
		"a.ts": {4},
		"b.ts": {3},
		"c.ts": {1},
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
