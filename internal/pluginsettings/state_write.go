//go:build !windows

package pluginsettings

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

func writeStateFile(path string, data []byte) error {
	if err := securefs.WriteFileAtomic(path, data); err != nil {
		return fmt.Errorf("write plugin storage: %w", err)
	}
	return syncStateDirectory(path)
}

// Confirm a previously visible receipt without rewriting bytes or metadata.
func syncStateFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open plugin storage for sync: %w", err)
	}
	defer file.Close() //nolint:errcheck -- the sync result is authoritative
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plugin storage: %w", err)
	}
	return syncStateDirectory(path)
}

func syncStateDirectory(path string) error {
	// Syncing only the temporary file does not make its rename durable. In
	// particular, the source fence must survive before any target is committed.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open plugin storage directory: %w", err)
	}
	defer dir.Close() //nolint:errcheck -- the sync result is authoritative
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync plugin storage directory: %w", err)
	}
	return nil
}
