package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// WireEnvVar is the environment switch that opts into raw wire dumps in
// the debug log. Off by default — debug.log carries metadata only
// (event name, provider, latency, status, token counts). Setting
// WUU_DEBUG_WIRE=1 (or true) re-enables the full request / response
// payloads and SSE lines. Read on every WireEnabled call (not cached)
// so tests can flip the switch via t.Setenv and observe both states in
// the same binary.
const WireEnvVar = "WUU_DEBUG_WIRE"

// WireEnabled reports whether raw wire dumps are allowed in this
// process. Callers that gate raw data on this switch must use
// DebugLogfWire (which internally calls WireEnabled and short-circuits)
// rather than checking the flag inline — that way a future refactor
// can move the env read without breaking every call site.
func WireEnabled() bool {
	v := os.Getenv(WireEnvVar)
	if v == "" {
		return false
	}
	return v == "1" || strings.EqualFold(v, "true")
}

// DebugLogfWire writes a formatted line to the debug log only when
// WireEnabled is true. Use this for any line that includes raw
// request bodies, response bodies, SSE event data, or anything else a
// user wouldn't want sitting in a 0o600 debug.log by default. The
// underlying DebugLogf already no-ops when the log file hasn't been
// initialized, so this layer only adds the wire opt-in gate.
func DebugLogfWire(format string, args ...any) {
	if !WireEnabled() {
		return
	}
	DebugLogf(format, args...)
}

var (
	debugLog  *os.File
	debugOnce sync.Once
	debugMu   sync.Mutex
)

// InitDebugLog opens a debug log file in the given user-level log directory.
func InitDebugLog(logDir string) {
	debugOnce.Do(func() {
		// Directory at 0o700, file at 0o600 — the debug log captures raw
		// request / response metadata and provider latency, both of which
		// are credential-adjacent even when the body itself is opt-in.
		if err := securefs.Mkdir(logDir); err != nil {
			return
		}
		path := filepath.Join(logDir, "debug.log")
		// Rotate if the log exceeds 2 MB to prevent unbounded growth.
		if info, err := os.Stat(path); err == nil && info.Size() > 2*1024*1024 {
			prev := path + ".1"
			os.Remove(prev)
			os.Rename(path, prev)
		}
		f, err := securefs.OpenAppend(path)
		if err != nil {
			return
		}
		debugMu.Lock()
		debugLog = f
		debugMu.Unlock()
		DebugLogf("=== wuu debug log started at %s ===", time.Now().Format(time.RFC3339))
	})
}

// DebugLogf writes a formatted line to the debug log. It is diagnostics only
// and must never gate the user-facing stream: the write goes straight to the
// file descriptor (so a process crash still lands the line via the OS page
// cache) but is not fsync'd, because a per-line fsync on the streaming path
// stalls event delivery for a durability guarantee the debug log does not need.
// The mutex only serializes concurrent writers so lines do not interleave.
func DebugLogf(format string, args ...any) {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugLog == nil {
		return
	}
	fmt.Fprintf(debugLog, "[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}
