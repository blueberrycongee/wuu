package coordinator

import (
	"os"
	"path/filepath"
)

// SharedDirName is the directory under .wuu/ that agents use as a
// cross-agent data plane. The system prompt teaches the convention,
// but the directory itself is just a normal folder — agents read and
// write it with the standard file tools, no new primitives required.
const SharedDirName = "shared"

// SharedSubdirs are the conventional subdirectories the system prompt
// suggests agents use. They are not required — an agent can pick any
// path under .wuu/shared/ — but they exist eagerly so the model sees
// the convention reflected on disk and so list_files returns something
// sensible on a fresh session.
var SharedSubdirs = []string{
	"findings", // investigation reports
	"plans",    // designs / todos / decisions
	"status",   // progress tracking
	"reports",  // final summaries / verdicts
}

// EnsureSharedDir creates .wuu/shared/{findings,plans,status,reports}
// under the given workspace root if any of them are missing. The
// directories are created with mode 0o755. Existing files are not
// touched. Returns nil on success.
//
// This is a one-shot called at session bootstrap. Workers running
// later don't need to re-ensure — they just write to whatever path
// they pick under .wuu/shared/.
func EnsureSharedDir(rootDir string) error {
	base := filepath.Join(rootDir, ".wuu", SharedDirName)
	for _, sub := range SharedSubdirs {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// SharedDirPath returns the absolute path to the shared dir for the
// given workspace root. The directory may not exist yet; callers
// that need it created should call EnsureSharedDir first.
func SharedDirPath(rootDir string) string {
	return filepath.Join(rootDir, ".wuu", SharedDirName)
}
