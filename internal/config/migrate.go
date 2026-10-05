package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// migrateLegacyGlobalStore performs a one-time, non-destructive copy of the
// user's pre-unification global config into the unified wuu home. It is
// idempotent and best-effort: if the new file already exists nothing happens,
// and any failure leaves the legacy file in place so callers can still fall
// back to the legacy read path. The legacy files are never deleted.
func migrateLegacyGlobalStore(home string) {
	if home == "" {
		return
	}
	if newPath, err := statepath.ConfigPath(home); err == nil {
		migrateGlobalFile(newPath, statepath.LegacyConfigPath(home), securefs.FileMode, "config.json")
	}
}

// migrateGlobalFile copies legacyPath to newPath when newPath is missing and
// legacyPath exists, preserving the requested permissions and printing a
// single migration notice to stderr. It is a no-op when the new file already
// exists, when the legacy file is absent, or when either path is empty.
func migrateGlobalFile(newPath, legacyPath string, perm os.FileMode, label string) {
	if newPath == "" || legacyPath == "" || newPath == legacyPath {
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		return // new location already populated; never overwrite it
	} else if !errors.Is(err, os.ErrNotExist) {
		return // unexpected stat error; stay safe and skip migration
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return // no legacy file to migrate
	}
	if err := os.MkdirAll(filepath.Dir(newPath), securefs.DirMode); err != nil {
		return
	}
	if err := os.WriteFile(newPath, data, perm); err != nil {
		return
	}
	// WriteFile applies perm subject to umask; re-assert it explicitly so a
	// migrated credential store keeps its 0600 mode regardless of umask.
	_ = os.Chmod(newPath, perm)
	fmt.Fprintf(os.Stderr, "wuu: migrated %s to %s (legacy copy kept at %s)\n", label, newPath, legacyPath)
}
