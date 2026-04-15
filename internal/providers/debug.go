package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	debugLog  *os.File
	debugOnce sync.Once
)

// InitDebugLog opens a debug log file at .wuu/debug.log in the given directory.
func InitDebugLog(dir string) {
	debugOnce.Do(func() {
		logDir := filepath.Join(dir, ".wuu")
		os.MkdirAll(logDir, 0o755)
		path := filepath.Join(logDir, "debug.log")
		// Rotate if the log exceeds 2 MB to prevent unbounded growth.
		if info, err := os.Stat(path); err == nil && info.Size() > 2*1024*1024 {
			prev := path + ".1"
			os.Remove(prev)
			os.Rename(path, prev)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		debugLog = f
		DebugLogf("=== wuu debug log started at %s ===", time.Now().Format(time.RFC3339))
	})
}

// DebugLogf writes a formatted line to the debug log.
func DebugLogf(format string, args ...any) {
	if debugLog == nil {
		return
	}
	fmt.Fprintf(debugLog, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	debugLog.Sync()
}
