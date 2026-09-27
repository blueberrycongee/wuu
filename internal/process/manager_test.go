//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func mustManagedCommand(t *testing.T, command, cwd string) *exec.Cmd {
	t.Helper()
	cmd, err := managedCommand(command, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestStartListAndPersist(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "state", "runtime")
	m, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(runtimeDir, "processes"), filepath.Join(runtimeDir, "logs")} {
		info, statErr := os.Stat(dir)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("process runtime directory %q mode = %o, want 700", dir, got)
		}
	}
	p, err := m.Start(context.Background(), StartOptions{Command: "echo hello; sleep 1", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerKind != OwnerMainAgent || p.Lifecycle != LifecycleManaged || p.Status != StatusRunning {
		t.Fatalf("unexpected process: %+v", p)
	}
	if p.ProcessStartTime == "" {
		t.Fatal("started process is missing its persisted identity")
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 process, got %d", len(list))
	}
	info, err := os.Stat(filepath.Join(runtimeDir, "processes", p.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("process record mode = %o, want 600", got)
	}
	logInfo, err := os.Stat(p.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := logInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("process log mode = %o, want 600", got)
	}
}

func TestSetRootDirMovesFutureLaunches(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	m, err := NewManager(oldRoot, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	m.SetRootDir(newRoot)
	p, err := m.Start(context.Background(), StartOptions{
		Command:   "pwd",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if p.CWD != want {
		t.Fatalf("process cwd = %q, want %q", p.CWD, want)
	}
	deadline := time.Now().Add(time.Second)
	for {
		current, err := m.Get(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == StatusStopped || current.Status == StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process did not reach terminal status: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartDoesNotRequirePSBinary(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "state", "runtime")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bashPath, filepath.Join(binDir, "bash")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	m, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Start(context.Background(), StartOptions{
		Command:   "read -r _",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop(p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestListRejectsCorruptProcessRecords(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "state", "runtime")
	m, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(runtimeDir, "processes", "proc-corrupt.json")
	if err := os.WriteFile(recordPath, []byte(`{"id":`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.List(); err == nil || !strings.Contains(err.Error(), "decode process record") {
		t.Fatalf("expected corrupt registry error, got %v", err)
	}
}

func TestProcessRecordPathsRejectEscapingIDs(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"../outside", `..\\outside`, ".", "..", ""} {
		if _, err := m.load(id); err == nil || !strings.Contains(err.Error(), "invalid process id") {
			t.Fatalf("load(%q) error = %v, want invalid process id", id, err)
		}
		if err := m.save(&Process{ID: id}); err == nil || !strings.Contains(err.Error(), "invalid process id") {
			t.Fatalf("save(%q) error = %v, want invalid process id", id, err)
		}
	}
}

func TestStartResolvesCWDInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "server")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}

	p, err := m.Start(context.Background(), StartOptions{Command: "pwd -P; sleep 1", CWD: "server", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Stop(p.ID) }()

	want := canonicalTestPath(t, subdir)
	got := canonicalTestPath(t, p.CWD)
	if got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
	offset := int64(0)
	snapshot, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{MaxBytes: 1024, OffsetBytes: &offset, Wait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Output, want) {
		t.Fatalf("process did not run in cwd %q: %q", want, snapshot.Output)
	}
}

func TestStartRejectsInvalidCWD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "not-a-dir"), []byte("file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		cwd     string
		wantErr string
	}{
		{name: "missing", cwd: "missing", wantErr: "does not exist"},
		{name: "file", cwd: "not-a-dir", wantErr: "not a directory"},
		{name: "outside absolute", cwd: outside, wantErr: "escapes workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Start(context.Background(), StartOptions{Command: "sleep 1", CWD: tc.cwd, OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected cwd error containing %q, got %v", tc.wantErr, err)
			}
		})
	}

	link := filepath.Join(root, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = m.Start(context.Background(), StartOptions{Command: "sleep 1", CWD: "escape-link", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("expected symlink escape cwd rejection, got %v", err)
	}
}

func TestStartAllowsOutsideWorkspaceWhenRequested(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}

	p, err := m.Start(context.Background(), StartOptions{
		Command:               "pwd -P; sleep 1",
		CWD:                   outside,
		OwnerKind:             OwnerMainAgent,
		OwnerID:               "main",
		Lifecycle:             LifecycleSession,
		AllowOutsideWorkspace: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Stop(p.ID) }()

	want := canonicalTestPath(t, outside)
	got := canonicalTestPath(t, p.CWD)
	if got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		path = evaluated
	}
	return filepath.Clean(path)
}

func TestStartDetachesProcessLifecycleFromStartContext(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := m.Start(ctx, StartOptions{Command: "sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(200 * time.Millisecond)

	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	var status Status
	for _, proc := range list {
		if proc.ID == p.ID {
			status = proc.Status
			break
		}
	}
	if status != StatusRunning {
		t.Fatalf("expected process to keep running after start context cancel, got %s", status)
	}

	stopped, err := m.Stop(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped == nil || (stopped.Status != StatusStopped && stopped.Status != StatusFailed) {
		t.Fatalf("expected manager stop to end process, got %+v", stopped)
	}
}

func TestStopStopsProcessGroup(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	p, err := m.Start(context.Background(), StartOptions{Command: "sleep 30 & wait", OwnerKind: OwnerSubagent, OwnerID: "worker-1", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Stop(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	proc, _ := os.FindProcess(p.PID)
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		// allow a brief settle, then fail if still alive
		time.Sleep(200 * time.Millisecond)
		if err := proc.Signal(syscall.Signal(0)); err == nil {
			t.Fatal("process still alive after stop")
		}
	}
}

func TestStopRefusesReusedProcessID(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	record := &Process{
		ID:               "proc-stale",
		Status:           StatusRunning,
		PID:              os.Getpid(),
		PGID:             pgid,
		ProcessStartTime: "a different process start time",
	}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	got, err := m.Stop(record.ID)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("expected reused-pid rejection, got process=%+v err=%v", got, err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("rejected stop changed status to %s", got.Status)
	}
}

func TestStopReconcilesMissingProcess(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	record := &Process{
		ID:               "proc-missing",
		Status:           StatusRunning,
		PID:              99999999,
		PGID:             99999999,
		ProcessStartTime: "no longer running",
	}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	got, err := m.Stop(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped || got.StoppedAt.IsZero() {
		t.Fatalf("missing process was not reconciled: %+v", got)
	}
}

func TestStopReconcilesProcessStartedByAnotherManager(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
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
	defer syscall.Kill(-pgid, syscall.SIGKILL)
	started, _, _, err := readProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	record := &Process{
		ID:               "proc-external",
		Status:           StatusRunning,
		PID:              cmd.Process.Pid,
		PGID:             pgid,
		ProcessStartTime: started,
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
		t.Fatalf("external process was not reconciled: %+v", got)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("external process was not reaped")
	}
}

func TestFinishWaitPreservesReconciledStoppedStatus(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	record := &Process{ID: "proc-reconciled", Status: StatusStopped}
	if err := m.save(record); err != nil {
		t.Fatal(err)
	}

	m.finishWait(record.ID, mustManagedCommand(t, "true", root), errors.New("signal: terminated"), 0)
	got, err := m.load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped {
		t.Fatalf("finishWait changed reconciled status to %s", got.Status)
	}
}

func TestCleanupSessionOnlyStopsSessionLifecycle(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	sessionProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	managedProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupSession(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	list, _ := m.List()
	var gotSession, gotManaged Status
	for _, p := range list {
		if p.ID == sessionProc.ID {
			gotSession = p.Status
		}
		if p.ID == managedProc.ID {
			gotManaged = p.Status
		}
	}
	if gotSession != StatusStopped && gotSession != StatusFailed {
		t.Fatalf("session process not stopped: %s", gotSession)
	}
	if gotManaged != StatusRunning {
		t.Fatalf("managed process should keep running, got %s", gotManaged)
	}
	_, _ = m.Stop(managedProc.ID)
}

func TestReadLogWindowPagesForwardFromExplicitOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}

	offset := int64(4)
	output, truncated, start, end, total, err := readLogWindow(path, 5, &offset)
	if err != nil {
		t.Fatal(err)
	}
	if output != "45678" || !truncated || start != 4 || end != 9 || total != 16 {
		t.Fatalf("unexpected first page: output=%q truncated=%t start=%d end=%d total=%d", output, truncated, start, end, total)
	}

	offset = end
	output, truncated, start, end, total, err = readLogWindow(path, 5, &offset)
	if err != nil {
		t.Fatal(err)
	}
	if output != "9abcd" || !truncated || start != 9 || end != 14 || total != 16 {
		t.Fatalf("unexpected second page: output=%q truncated=%t start=%d end=%d total=%d", output, truncated, start, end, total)
	}

	offset = end
	output, truncated, start, end, total, err = readLogWindow(path, 5, &offset)
	if err != nil {
		t.Fatal(err)
	}
	if output != "ef" || truncated || start != 14 || end != 16 || total != 16 {
		t.Fatalf("unexpected final page: output=%q truncated=%t start=%d end=%d total=%d", output, truncated, start, end, total)
	}
}

func TestStartTTYProvidesTerminalSemantics(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	ttyProc, err := m.Start(context.Background(), StartOptions{
		Command:   "if test -t 1; then echo MODE_TTY; else echo MODE_PIPE; fi; printf 'ENV=%s|%s|%s|%s\\n' \"$TERM\" \"$COLORTERM\" \"$CLICOLOR\" \"$FORCE_COLOR\"; printf '\\033[31mCOLOR_RED\\033[0m\\n'; sleep 1",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleSession,
		TTY:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(ttyProc.ID)
	pipeProc, err := m.Start(context.Background(), StartOptions{
		Command:   "if test -t 1; then echo MODE_TTY; else echo MODE_PIPE; fi; sleep 1",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleSession,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(pipeProc.ID)

	ttyOffset := int64(0)
	ttyOut, err := m.ReadOutputSnapshot(context.Background(), ttyProc.ID, OutputReadOptions{OffsetBytes: &ttyOffset, Wait: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !ttyOut.Process.TTY || !strings.Contains(ttyOut.Output, "MODE_TTY") {
		t.Fatalf("expected tty process output, got %+v output=%q", ttyOut.Process, ttyOut.Output)
	}
	if !strings.Contains(ttyOut.Output, "ENV=xterm-256color|truecolor|1|1") {
		t.Fatalf("expected terminal color environment, got %q", ttyOut.Output)
	}
	if !strings.Contains(ttyOut.Output, "\x1b[31mCOLOR_RED\x1b[0m") {
		t.Fatalf("expected raw ANSI color output, got %q", ttyOut.Output)
	}
	pipeOffset := int64(0)
	pipeOut, err := m.ReadOutputSnapshot(context.Background(), pipeProc.ID, OutputReadOptions{OffsetBytes: &pipeOffset, Wait: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if pipeOut.Process.TTY || !strings.Contains(pipeOut.Output, "MODE_PIPE") {
		t.Fatalf("expected pipe process output, got %+v output=%q", pipeOut.Process, pipeOut.Output)
	}
}

func TestWriteStdinToTTY(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	p, err := m.Start(context.Background(), StartOptions{
		Command:   "read line; echo GOT_TTY:$line; sleep 1",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleSession,
		TTY:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop(p.ID)
	if _, err := m.WriteStdin(p.ID, "hello\n"); err != nil {
		t.Fatalf("WriteStdin: %v", err)
	}
	offset := int64(0)
	deadline := time.Now().Add(2 * time.Second)
	var output strings.Builder
	for time.Now().Before(deadline) {
		snapshot, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{
			OffsetBytes: &offset,
			Wait:        200 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(snapshot.Output)
		offset = snapshot.EndOffset
		if strings.Contains(output.String(), "GOT_TTY:hello") {
			return
		}
	}
	if !strings.Contains(output.String(), "GOT_TTY:hello") {
		t.Fatalf("unexpected tty stdin output: %q", output.String())
	}
}

func TestReadOutputSnapshotWaitsForNewOutputAfterOffset(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	p, err := m.Start(context.Background(), StartOptions{Command: "sleep 0.2; printf ready; sleep 1", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	offset := int64(0)
	snapshot, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{
		MaxBytes:    4096,
		OffsetBytes: &offset,
		Wait:        2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TimedOut {
		t.Fatalf("expected wait to receive output, got timeout: %+v", snapshot)
	}
	if snapshot.StartOffset != 0 || snapshot.EndOffset <= 0 || snapshot.TotalBytes != snapshot.EndOffset {
		t.Fatalf("unexpected offsets: %+v", snapshot)
	}
	if !strings.Contains(snapshot.Output, "ready") {
		t.Fatalf("unexpected output: %q", snapshot.Output)
	}
	_, _ = m.Stop(p.ID)
}

func TestReadOutputSnapshotTimesOutWhenNoNewOutput(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	p, err := m.Start(context.Background(), StartOptions{Command: "sleep 1", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	offset := int64(0)
	snapshot, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{
		MaxBytes:    4096,
		OffsetBytes: &offset,
		Wait:        100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.TimedOut {
		t.Fatalf("expected timeout waiting for new output: %+v", snapshot)
	}
	if snapshot.Output != "" || snapshot.StartOffset != 0 || snapshot.EndOffset != 0 {
		t.Fatalf("unexpected output after timeout: %+v", snapshot)
	}
	_, _ = m.Stop(p.ID)
}

func TestWriteStdin(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root, filepath.Join(root, "state", "runtime"))
	p, err := m.Start(context.Background(), StartOptions{Command: "read line; echo got:$line; sleep 1", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.WriteStdin(p.ID, "hello\n"); err != nil {
		t.Fatalf("WriteStdin: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for {
		out, _, err := m.ReadOutput(p.ID, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "got:hello") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for stdin echo; output=%q", out)
		case <-time.After(50 * time.Millisecond):
		}
	}
	_, _ = m.Stop(p.ID)
}

func TestManagerPublishesLifecycleEventsAndCleanupSkipsManaged(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	m.Subscribe(events)

	sessionProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 5", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleSession})
	if err != nil {
		t.Fatal(err)
	}
	managedProc, err := m.Start(context.Background(), StartOptions{Command: "sleep 5", OwnerKind: OwnerMainAgent, OwnerID: "main", Lifecycle: LifecycleManaged})
	if err != nil {
		t.Fatal(err)
	}

	if got := (<-events); got.Type != EventStarted || got.Process.ID != sessionProc.ID {
		t.Fatalf("unexpected first event: %+v", got)
	}
	if got := (<-events); got.Type != EventStarted || got.Process.ID != managedProc.ID {
		t.Fatalf("unexpected second event: %+v", got)
	}

	result, err := m.CleanupSessionWithResult()
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Cleaned) != 1 || result.Cleaned[0].ID != sessionProc.ID {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}

	gotCleanup := false
	gotStopped := false
	deadline := time.After(5 * time.Second)
	for !(gotCleanup && gotStopped) {
		select {
		case ev := <-events:
			switch {
			case ev.Type == EventCleanedUp && ev.Process.ID == sessionProc.ID:
				gotCleanup = true
			case ev.Type == EventStopped && ev.Process.ID == sessionProc.ID:
				gotStopped = true
			case ev.Process.ID == managedProc.ID && ev.Type == EventCleanedUp:
				t.Fatalf("managed process should not be cleaned up: %+v", ev)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for cleanup events; cleanup=%v stopped=%v", gotCleanup, gotStopped)
		}
	}

	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]Status{}
	for _, proc := range list {
		statuses[proc.ID] = proc.Status
	}
	if statuses[sessionProc.ID] != StatusStopped {
		t.Fatalf("expected session process stopped, got %s", statuses[sessionProc.ID])
	}
	if statuses[managedProc.ID] != StatusRunning {
		t.Fatalf("expected managed process still running, got %s", statuses[managedProc.ID])
	}
	_, _ = m.Stop(managedProc.ID)
}

func TestManagerDistinguishesNaturalExitFromRequestedStop(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 16)
	m.Subscribe(events)

	natural, err := m.Start(context.Background(), StartOptions{
		Command:   "exit 0",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProcessEvent(t, events, natural.ID, EventStopped, EventCauseNaturalExit)

	stopped, err := m.Start(context.Background(), StartOptions{
		Command:   "sleep 5",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Stop(stopped.ID); err != nil {
		t.Fatal(err)
	}
	waitForProcessEvent(t, events, stopped.ID, EventStopped, EventCauseRequestedStop)
}

func TestManagerUnsubscribeStopsLifecycleDelivery(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "state", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 4)
	m.Subscribe(events)
	m.Unsubscribe(events)

	if _, err := m.Start(context.Background(), StartOptions{
		Command:   "exit 0",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: LifecycleManaged,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("received event after unsubscribe: %+v", event)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNaturalExitCompletionPersistsUntilDelivered(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "state", "runtime")
	m, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 8)
	m.Subscribe(events)
	started, err := m.Start(context.Background(), StartOptions{
		Command:   "printf 'done\\n'",
		OwnerKind: OwnerMainAgent,
		OwnerID:   "thread-1",
		Lifecycle: LifecycleManaged,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProcessEvent(t, events, started.ID, EventStopped, EventCauseNaturalExit)

	restarted, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := restarted.PendingCompletions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != started.ID {
		t.Fatalf("pending completions after restart = %+v, want %s", pending, started.ID)
	}
	delivered, err := restarted.MarkCompletionDelivered(started.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if delivered.CompletionDeliveredAt.IsZero() || delivered.CompletionConsumedBy != "test" {
		t.Fatalf("completion acknowledgement was not recorded: %+v", delivered)
	}
	delivered, err = restarted.MarkCompletionDelivered(started.ID, "duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if delivered.CompletionConsumedBy != "test" {
		t.Fatalf("duplicate acknowledgement changed the first consumer: %+v", delivered)
	}

	reopened, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	pending, err = reopened.PendingCompletions()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("delivered completion replayed after restart: %+v", pending)
	}
}

func TestDetachedNaturalExitDoesNotCreateCompletionObligation(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 8)
	m.Subscribe(events)
	started, err := m.Start(context.Background(), StartOptions{
		Command:        "exit 0",
		OwnerKind:      OwnerMainAgent,
		OwnerID:        "thread-detached",
		Lifecycle:      LifecycleManaged,
		CompletionMode: CompletionModeDetached,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProcessEvent(t, events, started.ID, EventStopped, EventCauseNaturalExit)
	pending, err := m.CompletionPending(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("detached process created an automatic completion obligation")
	}
	if _, err := m.MarkCompletionDelivered(started.ID, "test"); err == nil {
		t.Fatal("detached process completion should not be acknowledgeable")
	}
}

func TestNewManagerReconcilesPersistedManagedProcessExit(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "state", "runtime")
	seed, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := mustManagedCommand(t, "sleep 0.4", root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	identity, _, _, err := readProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := &Process{
		ID:               "proc-restart-watch",
		OwnerKind:        OwnerMainAgent,
		OwnerID:          "thread-restart",
		Lifecycle:        LifecycleManaged,
		Status:           StatusRunning,
		PID:              cmd.Process.Pid,
		PGID:             pgid,
		ProcessStartTime: identity,
		LogPath:          filepath.Join(seed.logDir, "proc-restart-watch.log"),
		Command:          "sleep 0.4",
		CWD:              root,
		StartedAt:        now,
		UpdatedAt:        now,
		ExitCode:         -1,
	}
	if err := os.WriteFile(record.LogPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := seed.save(record); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewManager(root, runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("external managed process did not exit")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		pending, err := restarted.PendingCompletions()
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 1 && pending[0].ID == record.ID {
			if pending[0].TerminalCause != EventCauseNaturalExit || pending[0].ExitCode != -1 {
				t.Fatalf("unexpected reconciled completion: %+v", pending[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("persisted managed process exit was not reconciled: %+v", pending)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForProcessEvent(t *testing.T, events <-chan Event, processID string, eventType EventType, cause EventCause) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Process.ID == processID && event.Type == eventType && event.Cause == cause {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for process %s event %s/%s", processID, eventType, cause)
		}
	}
}
