//go:build !windows

package tools

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
)

func TestExecuteShellCommandCancellationStopsProcessTree(t *testing.T) {
	root := t.TempDir()
	readyMarker := root + "/ready"
	cleanupMarker := root + "/cleanup"
	descendantMarker := root + "/descendant"
	t.Setenv("WUU_TEST_READY_MARKER", readyMarker)
	t.Setenv("WUU_TEST_CLEANUP_MARKER", cleanupMarker)
	t.Setenv("WUU_TEST_DESCENDANT_MARKER", descendantMarker)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan shellTestResult, 1)
	go func() {
		result, err := executeShellCommandInDir(ctx, newShellTestEnv(root), `
(trap '' TERM; while :; do sleep 1; done) &
printf '%s' "$!" > "$WUU_TEST_DESCENDANT_MARKER"
trap 'printf cleaned > "$WUU_TEST_CLEANUP_MARKER"; exit 0' TERM
printf ready > "$WUU_TEST_READY_MARKER"
while :; do sleep 1; done
`, 30, root)
		resultCh <- shellTestResult{result: result, err: err}
	}()

	waitForShellTestFile(t, readyMarker)
	descendantPID := readShellTestPID(t, descendantMarker)
	t.Cleanup(func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) })
	cancel()

	got := waitForShellTestResult(t, resultCh)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.TimedOut {
		t.Fatal("cancelled command was reported as timed out")
	}
	if got.result.ExitCode == 0 {
		t.Fatal("cancelled command was reported as successful")
	}
	cleanup, err := os.ReadFile(cleanupMarker)
	if err != nil {
		t.Fatalf("SIGTERM cleanup did not run: %v", err)
	}
	if string(cleanup) != "cleaned" {
		t.Fatalf("cleanup marker = %q, want cleaned", cleanup)
	}
	waitForShellTestProcessExit(t, descendantPID)
}

func TestExecuteShellCommandTimeoutStopsProcessTree(t *testing.T) {
	t.Setenv("WUU_HOME", t.TempDir())
	root := t.TempDir()
	readyMarker := root + "/ready"
	descendantMarker := root + "/descendant"
	t.Setenv("WUU_TEST_READY_MARKER", readyMarker)
	t.Setenv("WUU_TEST_DESCENDANT_MARKER", descendantMarker)

	resultCh := make(chan shellTestResult, 1)
	go func() {
		result, err := executeShellCommandInDir(context.Background(), newShellTestEnv(root), `
trap '' TERM
(trap '' TERM; while :; do sleep 1; done) &
printf '%s' "$!" > "$WUU_TEST_DESCENDANT_MARKER"
printf ready > "$WUU_TEST_READY_MARKER"
while :; do sleep 1; done
`, 1, root)
		resultCh <- shellTestResult{result: result, err: err}
	}()

	waitForShellTestFile(t, readyMarker)
	descendantPID := readShellTestPID(t, descendantMarker)
	t.Cleanup(func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) })
	got := waitForShellTestResult(t, resultCh)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if !got.result.TimedOut {
		t.Fatal("timed-out command was not reported as timed out")
	}
	if got.result.ExitCode == 0 {
		t.Fatal("timed-out command was reported as successful")
	}
	// A timeout no longer kills the tree outright: the run is promoted to a
	// managed background process with its output so far. Stopping that
	// process must still take the whole tree down.
	if got.result.PromotedProcessID == "" {
		t.Fatal("timed-out command should be promoted to a background process")
	}
	mgr, err := (&Env{RootDir: root}).ProcessManager()
	if err != nil {
		t.Fatalf("process manager: %v", err)
	}
	promoted, err := mgr.Get(got.result.PromotedProcessID)
	if err != nil {
		t.Fatalf("get promoted process: %v", err)
	}
	if processsandbox.Supported() && promoted.SandboxMode != processsandbox.ModeWorkspaceWrite {
		t.Fatalf("promoted sandbox mode = %q, want workspace-write", promoted.SandboxMode)
	}
	if _, err := mgr.Stop(got.result.PromotedProcessID); err != nil {
		t.Fatalf("stop promoted process: %v", err)
	}
	waitForShellTestProcessExit(t, descendantPID)
}

type shellTestResult struct {
	result shellExecutionResult
	err    error
}

func waitForShellTestResult(t *testing.T, resultCh <-chan shellTestResult) shellTestResult {
	t.Helper()
	select {
	case result := <-resultCh:
		return result
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for shell execution result")
		return shellTestResult{}
	}
}

func waitForShellTestFile(t *testing.T, path string) {
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

func readShellTestPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitForShellTestProcessExit(t *testing.T, pid int) {
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
