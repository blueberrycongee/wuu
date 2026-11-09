package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/storagecodec"
)

const (
	searchCursorRetention         = 7 * 24 * time.Hour
	searchCursorQuietAge          = time.Hour
	searchCursorStateBudget int64 = 512 * 1024 * 1024
)

type SearchCursorMaintenanceResult struct {
	Deleted     int
	Compressed  int
	BytesBefore int64
	BytesAfter  int64
}

// MaintainSearchCursorStorage removes expired pagination caches and
// losslessly compresses inactive recent caches. Search cursors are derived
// data: deleting one only causes the search tool to recompute its result.
func MaintainSearchCursorStorage(workspaceStateDir string, now time.Time) (SearchCursorMaintenanceResult, error) {
	var result SearchCursorMaintenanceResult
	if workspaceStateDir == "" {
		return result, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	pattern := filepath.Join(workspaceStateDir, "sessions", "*", "tool-results", "search-cursors", "*.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return result, fmt.Errorf("list search cursor storage: %w", err)
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, fmt.Errorf("stat search cursor %s: %w", path, err)
		}
		age := now.Sub(info.ModTime())
		if age >= searchCursorRetention {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return result, fmt.Errorf("remove expired search cursor %s: %w", path, err)
			}
			result.Deleted++
			continue
		}
		if age < searchCursorQuietAge {
			continue
		}
		changed, before, after, err := storagecodec.CompressFile(path)
		if err != nil {
			return result, fmt.Errorf("compress search cursor %s: %w", path, err)
		}
		if changed {
			result.Compressed++
			result.BytesBefore += before
			result.BytesAfter += after
		}
	}
	type storedCursor struct {
		path    string
		size    int64
		modTime time.Time
	}
	paths, err = filepath.Glob(pattern)
	if err != nil {
		return result, fmt.Errorf("relist search cursor storage: %w", err)
	}
	stored := make([]storedCursor, 0, len(paths))
	var totalBytes int64
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, fmt.Errorf("restat search cursor %s: %w", path, err)
		}
		stored = append(stored, storedCursor{path: path, size: info.Size(), modTime: info.ModTime()})
		totalBytes += info.Size()
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].modTime.Before(stored[j].modTime) })
	for _, cursor := range stored {
		if totalBytes <= searchCursorStateBudget {
			break
		}
		if now.Sub(cursor.modTime) < searchCursorQuietAge {
			continue
		}
		if err := os.Remove(cursor.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return result, fmt.Errorf("remove over-budget search cursor %s: %w", cursor.path, err)
		}
		totalBytes -= cursor.size
		result.Deleted++
	}
	return result, nil
}
