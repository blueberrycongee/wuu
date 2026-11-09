package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestDevWatchPathIgnored(t *testing.T) {
	t.Parallel()
	ignored := []string{
		"dist/index.js",
		"node_modules/dep/index.js",
		".git/HEAD",
		"src/nested/node_modules/x.js",
	}
	for _, rel := range ignored {
		if !devWatchPathIgnored(rel) {
			t.Errorf("devWatchPathIgnored(%q) = false, want true", rel)
		}
	}
	watched := []string{
		"index.ts",
		"plugin.json",
		"src/index.ts",
		"src/theme/tokens.json",
	}
	for _, rel := range watched {
		if devWatchPathIgnored(rel) {
			t.Errorf("devWatchPathIgnored(%q) = true, want false", rel)
		}
	}
}

func TestDevWatchIgnoresEvent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	inside := filepath.Join(root, "dist", "index.js")
	if !devWatchIgnoresEvent(root, fsnotify.Event{Name: inside, Op: fsnotify.Write}) {
		t.Errorf("event under dist should be ignored")
	}
	source := filepath.Join(root, "src", "index.ts")
	if devWatchIgnoresEvent(root, fsnotify.Event{Name: source, Op: fsnotify.Write}) {
		t.Errorf("source event should not be ignored")
	}
	// An ignored-looking component above the watched root must not suppress events.
	aboveRoot := filepath.Join(filepath.Dir(root), "dist")
	if err := os.MkdirAll(aboveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	nestedRoot := filepath.Join(aboveRoot, "plugin")
	if err := os.MkdirAll(nestedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	event := filepath.Join(nestedRoot, "index.ts")
	if devWatchIgnoresEvent(nestedRoot, fsnotify.Event{Name: event, Op: fsnotify.Write}) {
		t.Errorf("event should not be ignored because of an ancestor directory name")
	}
	// Events outside the root are not ours to filter.
	if devWatchIgnoresEvent(root, fsnotify.Event{Name: filepath.Join(t.TempDir(), "dist", "x.js"), Op: fsnotify.Write}) {
		t.Errorf("unrelated event outside root should not be ignored")
	}
}

func TestAddDevWatchDirsRecursesAndSkipsIgnored(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, dir := range []string{"src", "src/deep", "dist", "node_modules/dep"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skipf("fsnotify unavailable: %v", err)
	}
	defer watcher.Close()

	if err := addDevWatchDirs(watcher, root); err != nil {
		t.Fatalf("addDevWatchDirs: %v", err)
	}

	// A write in src/deep must produce an event; a write in dist must not
	// (dist is never registered, so no event can arrive from it).
	writeFile := func(rel string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	waitEvent := func(d time.Duration) (fsnotify.Event, bool) {
		t.Helper()
		select {
		case event := <-watcher.Events:
			return event, true
		case <-time.After(d):
			return fsnotify.Event{}, false
		}
	}

	writeFile("src/deep/index.ts")
	event, ok := waitEvent(3 * time.Second)
	if !ok {
		t.Fatalf("expected fsnotify event for src/deep write")
	}
	if devWatchIgnoresEvent(root, event) {
		t.Fatalf("source event %s was filtered", event.Name)
	}

	// Drain any stragglers, then confirm dist writes stay silent.
	for {
		if _, ok := waitEvent(50 * time.Millisecond); !ok {
			break
		}
	}
	writeFile("dist/index.js")
	if event, ok := waitEvent(300 * time.Millisecond); ok {
		t.Fatalf("unexpected event from ignored dist tree: %s", event.Name)
	}
}

// Recovery contracts: a failed build must wait for the next edit, a busy
// generation must retry without another edit, and edits during a build must
// not be swallowed by the snapshot recorded after that build.
func TestDevWatchRefreshRecoversWithoutReplacingLastGoodSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src.ts")
	writePluginDevTestFile(t, source, "old")
	initial := snapshotPluginSource(root)
	state := devWatchState{attempted: initial, published: initial}
	calls := 0
	refresh := func() (pluginDiagnostic, error) {
		calls++
		if calls == 1 {
			return pluginDiagnostic{Level: "fail", Check: "dev.build"}, errors.New("broken source; previous generation preserved")
		}
		return pluginDiagnostic{Level: "pass", Check: "dev.refresh"}, nil
	}
	writePluginDevTestFile(t, source, "broken edit")
	state.refresh(root, refresh)
	if calls != 1 || len(changedPluginSourcePaths(initial, state.published)) != 0 {
		t.Fatalf("failed build changed last good snapshot: calls=%d", calls)
	}
	state.refresh(root, refresh)
	if calls != 1 {
		t.Fatal("unchanged failing source was rebuilt")
	}
	writePluginDevTestFile(t, source, "fixed source edit")
	state.refresh(root, refresh)
	if calls != 2 || len(changedPluginSourcePaths(initial, state.published)) == 0 {
		t.Fatalf("fixed edit was not published: calls=%d", calls)
	}
	state.refresh(root, refresh)
	if calls != 2 {
		t.Fatal("duplicate source event caused another build")
	}
}

func TestDevWatchRefreshRetriesBusyAndKeepsMidBuildEdits(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src.ts")
	writePluginDevTestFile(t, source, "initial")
	initial := snapshotPluginSource(root)
	state := devWatchState{attempted: initial, published: initial, pending: true}
	calls := 0
	refresh := func() (pluginDiagnostic, error) {
		calls++
		if calls == 1 {
			return pluginDiagnostic{Level: "fail", Check: "dev.mutation"}, errDevGenerationBusy
		}
		if calls == 2 {
			writePluginDevTestFile(t, source, "edit while building")
		}
		return pluginDiagnostic{Level: "pass", Check: "dev.refresh"}, nil
	}
	state.refresh(root, refresh)
	if !state.pending {
		t.Fatal("busy generation lost its retry")
	}
	state.refresh(root, refresh)
	state.refresh(root, refresh)
	if calls != 3 {
		t.Fatalf("mid-build edit was lost: refresh calls=%d", calls)
	}
	state.refresh(root, refresh)
	if calls != 3 {
		t.Fatal("unchanged source was rebuilt")
	}
}

func TestDevWatchSnapshotDetectsPreservedTimestampEdits(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugin.ts")
	writePluginDevTestFile(t, path, "first")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotPluginSource(path)
	writePluginDevTestFile(t, path, "other")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after := snapshotPluginSource(path)
	if len(changedPluginSourcePaths(before, after)) != 1 {
		t.Fatal("same-size preserved-timestamp edit was missed")
	}
	if err := os.Chtimes(path, info.ModTime().Add(time.Hour), info.ModTime().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(changedPluginSourcePaths(after, snapshotPluginSource(path))) != 0 {
		t.Fatal("touching unchanged source triggered rebuild")
	}
}
