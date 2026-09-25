package providers

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestDebugLogfConcurrentWritesAreSerialized exercises DebugLogf from many
// goroutines. With the per-line mutex, every line lands intact; without it (and
// under -race) the writes would interleave or race. It also implicitly confirms
// DebugLogf writes straight to the fd (no fsync, no buffering): the content is
// readable immediately from a separate handle.
func TestDebugLogfConcurrentWritesAreSerialized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	debugMu.Lock()
	prev := debugLog
	debugLog = f
	debugMu.Unlock()
	t.Cleanup(func() {
		debugMu.Lock()
		debugLog = prev
		debugMu.Unlock()
		f.Close()
	})

	const writers, perWriter = 8, 64
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				DebugLogf("entry w=%d i=%d", w, i)
			}
		}(w)
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "entry w="); got != writers*perWriter {
		t.Fatalf("wrote %d entries, want %d", got, writers*perWriter)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Count(line, "entry w=") > 1 {
			t.Fatalf("interleaved writers on one line: %q", line)
		}
	}
}

func TestWireEnabled_DefaultOff(t *testing.T) {
	// Default: env var unset → wire dumps are off.
	orig, hadOrig := os.LookupEnv(WireEnvVar)
	os.Unsetenv(WireEnvVar)
	t.Cleanup(func() {
		if hadOrig {
			os.Setenv(WireEnvVar, orig)
		}
	})
	if WireEnabled() {
		t.Fatalf("WireEnabled() = true with %s unset; want false", WireEnvVar)
	}
}

func TestWireEnabled_On(t *testing.T) {
	t.Setenv(WireEnvVar, "1")
	if !WireEnabled() {
		t.Fatalf("WireEnabled() = false with %s=1; want true", WireEnvVar)
	}
}

func TestWireEnabled_TrueCaseInsensitive(t *testing.T) {
	for _, v := range []string{"True", "TRUE", "tRuE"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(WireEnvVar, v)
			if !WireEnabled() {
				t.Fatalf("WireEnabled() = false with %s=%q; want true", WireEnvVar, v)
			}
		})
	}
}

func TestWireEnabled_OtherText(t *testing.T) {
	// Anything that isn't "1" / "true" (case-insensitive) is treated as
	// off. We don't want a typo like "yes" or "on" to silently turn on
	// raw wire dumps.
	for _, v := range []string{"yes", "on", "2", "wire", "enabled"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(WireEnvVar, v)
			if WireEnabled() {
				t.Fatalf("WireEnabled() = true with %s=%q; want false", WireEnvVar, v)
			}
		})
	}
}
