package persistence

import (
	"errors"
	"os"
	"path/filepath"
)

// Propagate unsupported directory barriers instead of acknowledging a durable
// binding on a filesystem that cannot establish it.
// A freshly created store may have freshly created parents too. Sync every
// directory entry up to the filesystem root before acknowledging publication.
func syncAncestors(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		if err := syncDirectory(path); err != nil {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
