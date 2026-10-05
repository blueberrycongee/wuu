package agentcontrol

import (
	"os"
	"path/filepath"

	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

// SharedDirName is the workspace-state directory that agents use as a
// cross-agent data plane.
const SharedDirName = "shared"

// SharedSubdirs are the conventional subdirectories the system prompt
// suggests agents use. They are not required — an agent can pick any
// path under the shared state directory.
var SharedSubdirs = []string{
	"findings", // investigation reports
	"plans",    // designs / todos / decisions
	"status",   // progress tracking
	"reports",  // final summaries / verdicts
}

// EnsureSharedDir creates shared/{findings,plans,status,reports}
// under the given workspace state directory if any of them are missing. The
// directories are owner-only; agents share the same OS identity. Existing files are not
// touched. Returns nil on success.
//
// This is a one-shot called at session bootstrap. Workers running
// later don't need to re-ensure — they just write to whatever path
// they pick under the shared directory.
func EnsureSharedDir(workspaceStateDir string) error {
	base := statepath.SharedDir(workspaceStateDir)
	for _, sub := range SharedSubdirs {
		if err := os.MkdirAll(filepath.Join(base, sub), securefs.DirMode); err != nil {
			return err
		}
	}
	return nil
}

// SharedDirPath returns the absolute path to the shared dir for the
// given workspace state directory. The directory may not exist yet; callers
// that need it created should call EnsureSharedDir first.
func SharedDirPath(workspaceStateDir string) string {
	return statepath.SharedDir(workspaceStateDir)
}
