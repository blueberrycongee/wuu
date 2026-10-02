//go:build linux

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Observe the child's exit without reaping it. Its PTY output is already
// buffered before the manager starts its reader, so no sleeps decide the race.
func waitForPTYTestExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWaitPTYDrainsBufferedOutputAfterLeaderExit(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	id, logPath := m.ReserveProcessLog()
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("sh", "-c", "printf PTY_FINAL_EVIDENCE")
	master, err := startPTYProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer master.close()
	defer cmd.Process.Kill()
	waitForPTYTestExit(t, cmd)
	m.waitPTY(id, cmd, log, master)
	output, err := os.ReadFile(logPath)
	if err != nil || string(output) != "PTY_FINAL_EVIDENCE" {
		t.Fatalf("natural exit lost buffered PTY output: %q, %v", output, err)
	}
}

func TestWaitPTYReturnsWithAnotherSlaveHolder(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	id, logPath := m.ReserveProcessLog()
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	tty, err := preparePTYSession(master)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.close()
	cmd := exec.Command("sh", "-c", "printf PTY_LEADER_DONE")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	waitForPTYTestExit(t, cmd)
	// This retained descriptor models a descendant holding the slave open.
	// Closing it is test cleanup, not what allows the manager to finish.
	done := make(chan struct{})
	go func() { m.waitPTY(id, cmd, log, tty); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = slave.Close()
		<-done
		t.Fatal("PTY completion waited for another slave holder")
	}
	output, err := os.ReadFile(logPath)
	if err != nil || string(output) != "PTY_LEADER_DONE" {
		t.Fatalf("retained slave lost buffered PTY output: %q, %v", output, err)
	}
}
