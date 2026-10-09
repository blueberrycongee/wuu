package tools

import (
	"os"
	"path/filepath"
	"sync"
)

type fileMutationQueueEntry struct {
	mu   sync.Mutex
	refs int
}

var fileMutationQueues = struct {
	sync.Mutex
	entries map[string]*fileMutationQueueEntry
}{entries: make(map[string]*fileMutationQueueEntry)}

// withFileMutationQueue serializes mutations to the same physical file while
// allowing unrelated files to proceed independently. The queue is process-wide
// so cloned toolkits targeting one workspace share the same mutation boundary.
func withFileMutationQueue[T any](path string, mutate func() (T, error)) (T, error) {
	key := fileMutationQueueKey(path)

	fileMutationQueues.Lock()
	entry := fileMutationQueues.entries[key]
	if entry == nil {
		entry = &fileMutationQueueEntry{}
		fileMutationQueues.entries[key] = entry
	}
	entry.refs++
	fileMutationQueues.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		fileMutationQueues.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(fileMutationQueues.entries, key)
		}
		fileMutationQueues.Unlock()
	}()

	return mutate()
}

func fileMutationQueueKey(path string) string {
	cleaned := filepath.Clean(path)
	if canonical, err := fileMutationPath(cleaned); err == nil {
		return canonical
	}
	return cleaned
}

// Resolve aliases through existing ancestors even when an add or move target's
// parent directories do not exist yet. The same key is used for conflict
// detection and mutation queues; execution retains permission-checked paths.
func fileMutationPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(path)
	if !os.IsNotExist(err) || parent == path {
		return "", err
	}
	resolved, err = fileMutationPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
