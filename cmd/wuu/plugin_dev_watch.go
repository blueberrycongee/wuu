package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// devWatchDebounce collapses editor save bursts into one generation refresh.
const devWatchDebounce = 300 * time.Millisecond

// devWatchIgnoredDirs never trigger refreshes: build output would loop the
// watcher (refresh rebuilds into dist), and dependency/VCS churn is not source.
var devWatchIgnoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
}

// devWatchState records the source seen before an attempt, separately from the
// last successful publication. Failed builds wait for another edit; execution
// lease contention retries without requiring the author to save again.
type devWatchState struct {
	attempted pluginSourceSnapshot
	published pluginSourceSnapshot
	pending   bool
}

func (state *devWatchState) refresh(dir string, refresh func() (pluginDiagnostic, error)) {
	current := snapshotPluginSource(dir)
	if !state.pending && len(changedPluginSourcePaths(state.attempted, current)) == 0 {
		return
	}
	diagnostic, err := refresh()
	if errors.Is(err, errDevGenerationBusy) {
		if !state.pending {
			diagnostic.Message = err.Error()
			printDevDiagnostic(diagnostic)
		}
		state.pending = true
		return
	}
	state.pending = false
	// Capture before building: an edit during the build must trigger another pass.
	state.attempted = current
	if err != nil {
		diagnostic.Message = err.Error()
	}
	printDevDiagnostic(diagnostic)
	if err == nil {
		if changed := changedPluginSourcePaths(state.published, current); len(changed) > 0 {
			printDevReloadHint(dir, changed)
		}
		state.published = current
	}
}

// watchDevDirFS combines low-latency filesystem events with periodic source
// reconciliation. Reconciliation also recovers lost events and replaced roots;
// an unavailable watcher must never require restarting the development command.
func watchDevDirFS(ctx context.Context, wuuHome, dir, packageManager string, pollInterval time.Duration, initial pluginSourceSnapshot, initialPending bool) {
	info, statErr := os.Stat(dir)
	singleFile := statErr == nil && !info.IsDir()
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		defer watcher.Close()
		if err = addDevWatchDirs(watcher, dir); err != nil {
			fmt.Printf("watch setup: %v; source reconciliation remains active\n", err)
		}
	} else {
		fmt.Printf("fsnotify unavailable (%v); polling every %s\n", err, pollInterval)
	}
	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	if watcher != nil {
		events, watchErrors = watcher.Events, watcher.Errors
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	state := devWatchState{attempted: initial, published: initial, pending: initialPending}
	refresh := func() (pluginDiagnostic, error) { return refreshDevGeneration(ctx, wuuHome, dir, packageManager) }
	var debounce *time.Timer
	var debounceC <-chan time.Time
	schedule := func() {
		if debounce == nil {
			debounce = time.NewTimer(devWatchDebounce)
		} else {
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(devWatchDebounce)
		}
		debounceC = debounce.C
	}
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	// Cover edits between the initial build and watcher setup.
	state.refresh(dir, refresh)
	fmt.Printf("Watching for changes (debounce %s, reconciliation %s)... (Ctrl+C to stop)\n", devWatchDebounce, pollInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if singleFile && filepath.Clean(event.Name) != filepath.Clean(dir) {
				continue
			}
			if devWatchIgnoresEvent(dir, event) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				schedule()
			}
		case watchErr, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			fmt.Printf("watch error: %v; source reconciliation remains active\n", watchErr)
			schedule()
		case <-ticker.C:
			if watcher != nil && events != nil {
				// fsnotify is not recursive. Re-register directories after creation or
				// replacement, including saves that raced their initial registration.
				if err := addDevWatchDirs(watcher, dir); err != nil {
					fmt.Printf("watch registration: %v; source reconciliation remains active\n", err)
				}
			}
			// Do not interrupt an editor save burst that is already debouncing.
			if debounceC == nil {
				state.refresh(dir, refresh)
			}
		case <-debounceC:
			debounceC = nil
			state.refresh(dir, refresh)
		}
	}
}

// addDevWatchDirs registers root and every non-ignored subdirectory.
func addDevWatchDirs(watcher *fsnotify.Watcher, root string) error {
	if info, err := os.Stat(root); err == nil && !info.IsDir() {
		// Watch the parent so atomic file replacement does not remove our watch.
		return watcher.Add(filepath.Dir(root))
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && devWatchIgnoredDirs[entry.Name()] {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// devWatchIgnoresEvent filters events under ignored directories, judged
// relative to the watched root so an ancestor directory that happens to be
// named dist/node_modules above the plugin cannot suppress its own events.
func devWatchIgnoresEvent(root string, event fsnotify.Event) bool {
	rel, err := filepath.Rel(root, event.Name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return devWatchPathIgnored(rel)
}

// devWatchPathIgnored reports whether rel (a path relative to the watched
// root) sits under an ignored directory. Pure helper for tests.
func devWatchPathIgnored(rel string) bool {
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for _, part := range parts {
		if devWatchIgnoredDirs[part] {
			return true
		}
	}
	return false
}
