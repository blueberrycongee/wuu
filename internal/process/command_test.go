//go:build !windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandHandleStopAllowsCleanupAndKillsDescendants(t *testing.T) {
	readyMarker := t.TempDir() + "/ready"
	cleanupMarker := t.TempDir() + "/cleanup"
	descendantMarker := t.TempDir() + "/descendant"
	cmd := exec.Command("/bin/sh", "-c", `
(trap '' TERM; while :; do sleep 1; done) &
printf '%s' "$!" > "$DESCENDANT_MARKER"
trap 'printf cleaned > "$CLEANUP_MARKER"; exit 0' TERM
printf ready > "$READY_MARKER"
while :; do sleep 1; done
`)
	cmd.Env = append(os.Environ(),
		"READY_MARKER="+readyMarker,
		"CLEANUP_MARKER="+cleanupMarker,
		"DESCENDANT_MARKER="+descendantMarker,
	)

	handle, err := StartCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Stop(100 * time.Millisecond) })
	waitForTestFile(t, readyMarker)

	descendantText, err := os.ReadFile(descendantMarker)
	if err != nil {
		t.Fatal(err)
	}
	descendantPID, err := strconv.Atoi(strings.TrimSpace(string(descendantText)))
	if err != nil {
		t.Fatal(err)
	}

	if err := handle.Stop(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := handle.Wait(); err != nil {
		t.Fatalf("TERM-aware parent did not exit cleanly: %v", err)
	}
	cleanup, err := os.ReadFile(cleanupMarker)
	if err != nil {
		t.Fatalf("SIGTERM cleanup did not run: %v", err)
	}
	if string(cleanup) != "cleaned" {
		t.Fatalf("cleanup marker = %q, want cleaned", cleanup)
	}
	waitForProcessExit(t, descendantPID)
}

func TestCommandHandleWaitsForNaturalExit(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	handle, err := StartCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	err = handle.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("wait error = %v, want exit code 7", err)
	}
}

func TestProcessTreeRejectsUnsafeID(t *testing.T) {
	if err := ProcessTreeFromID(1).Kill(); err == nil || !strings.Contains(err.Error(), "unsafe process tree id") {
		t.Fatalf("Kill error = %v, want unsafe process tree id", err)
	}
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("descendant process %d is still alive", pid)
}
