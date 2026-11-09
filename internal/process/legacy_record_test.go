//go:build !windows

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startExternalProcessGroup launches a process the manager did not start, so
// tests can attach hand-written records to it.
func startExternalProcessGroup(t *testing.T, root string) (*exec.Cmd, int, chan error) {
	t.Helper()
	cmd := mustManagedCommand(t, "sleep 30", root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return cmd, pgid, done
}

func TestStopRetiresLegacyRecordWithoutIdentity(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	cmd, pgid, _ := startExternalProcessGroup(t, root)

	// Even a record whose StartedAt matches the live process must not be
	// signaled: without a stored identity the match cannot be proven.
	record := &Process{
		ID:        "proc-legacy-no-identity",
		Lifecycle: LifecycleSession,
		Status:    StatusRunning,
		PID:       cmd.Process.Pid,
		PGID:      pgid,
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
		ExitCode:  -1,
	}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	got, err := m.Stop(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.StoppedAt.IsZero() {
		t.Fatalf("identity-less record was not retired: %+v", got)
	}
	if !strings.Contains(got.LastError, "no recorded identity") {
		t.Fatalf("retired record does not explain its cause: %+v", got)
	}
	process, err := os.FindProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("retiring the record signaled the process: %v", err)
	}
	again, err := m.Stop(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != StatusFailed {
		t.Fatalf("repeat stop reopened the retired record: %+v", again)
	}
}

func TestStopMigratesLegacyStartTimeRecord(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	// Legacy records store verbatim `ps -o lstart=` output, which is
	// locale-dependent; pin the C locale so the fixture matches on any host.
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "C")
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	cmd, pgid, done := startExternalProcessGroup(t, root)
	currentIdentity, _, _, err := readProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	legacyStart, err := readLegacyProcessStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if !isLegacyStartTimeIdentity(legacyStart) {
		t.Fatalf("start time %q is not in the legacy record format", legacyStart)
	}

	record := &Process{
		ID:               "proc-legacy-start-time",
		Lifecycle:        LifecycleSession,
		Status:           StatusRunning,
		PID:              cmd.Process.Pid,
		PGID:             pgid,
		ProcessStartTime: legacyStart,
		StartedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		ExitCode:         -1,
	}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	got, err := m.Stop(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped || got.StoppedAt.IsZero() {
		t.Fatalf("legacy process was not stopped: %+v", got)
	}
	if got.ProcessStartTime != currentIdentity {
		t.Fatalf("migrated process identity = %q, want %q", got.ProcessStartTime, currentIdentity)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy process was not reaped")
	}
}

func TestStopReconcilesDeadLegacyProcessRecord(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	record := &Process{
		ID:        "proc-legacy-dead",
		Lifecycle: LifecycleSession,
		Status:    StatusRunning,
		PID:       99999999,
		PGID:      99999999,
		StartedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	got, err := m.Stop(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped || got.StoppedAt.IsZero() {
		t.Fatalf("dead legacy process was not reconciled: %+v", got)
	}
}

func TestStopRefusesLegacyRecordForDifferentProcess(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	cmd, pgid, _ := startExternalProcessGroup(t, root)

	record := &Process{
		ID:               "proc-legacy-reused",
		Lifecycle:        LifecycleSession,
		Status:           StatusRunning,
		PID:              cmd.Process.Pid,
		PGID:             pgid,
		ProcessStartTime: time.Now().Add(-time.Hour).Local().Format(legacyStartTimeLayout),
		StartedAt:        time.Now().Add(-time.Hour),
		UpdatedAt:        time.Now(),
	}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	got, err := m.Stop(record.ID)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("expected legacy identity rejection, got process=%+v err=%v", got, err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("rejected stop changed status to %s", got.Status)
	}
	process, err := os.FindProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("identity rejection signaled the unrelated process: %v", err)
	}
}
