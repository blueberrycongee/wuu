package config

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// retiredToolLoadingWarned de-duplicates the deprecation notice across the
// process lifetime. The app-server resolves the tool-loading preference on
// every session build and on every provider switch, so without this the same
// line would repeat for the whole run. Keyed by the configured spelling.
var retiredToolLoadingWarned sync.Map

// retiredToolLoadingWarnWriter is the sink for the notice. It defaults to
// stderr and is a test seam so the output can be asserted without pipe
// plumbing, matching the mcp_json diagnostics above.
var retiredToolLoadingWarnWriter io.Writer = os.Stderr

func warnRetiredToolLoadingOnce(key, msg string) {
	if _, seen := retiredToolLoadingWarned.LoadOrStore(key, struct{}{}); seen {
		return
	}
	fmt.Fprintln(retiredToolLoadingWarnWriter, msg)
}

// resetRetiredToolLoadingWarnings clears the process-level dedupe set.
// Test-only.
func resetRetiredToolLoadingWarnings() {
	retiredToolLoadingWarned.Range(func(k, _ any) bool {
		retiredToolLoadingWarned.Delete(k)
		return true
	})
}
