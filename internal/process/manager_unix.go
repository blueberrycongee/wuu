//go:build !windows

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ptySupported reports whether this platform can run tty-mode processes.
func ptySupported() bool { return true }

// startPTYProcess starts cmd attached to a fresh pty in its own session,
// so the whole job is addressable as one process group.
func startPTYProcess(cmd *exec.Cmd) (*ptySession, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer slave.Close()
	tty, err := preparePTYSession(master)
	if err != nil {
		_ = master.Close()
		return nil, err
	}
	if err := resizePTY(master, 80, 24); err != nil {
		tty.close()
		return nil, err
	}
	if cmd.Stdin == nil {
		cmd.Stdin = slave
	}
	if cmd.Stdout == nil {
		cmd.Stdout = slave
	}
	if cmd.Stderr == nil {
		cmd.Stderr = slave
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		tty.close()
		return nil, err
	}
	return tty, nil
}

func preparePTYSession(master *os.File) (*ptySession, error) {
	exitRead, exitWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	tty := &ptySession{master: master, exitRead: exitRead, exitWrite: exitWrite}
	// Use our own poll loop rather than Go's PTY poller (not supported on
	// every Unix platform). Nonblocking reads keep readiness races cancellable.
	err = unix.SetNonblock(int(master.Fd()), true)
	if err == nil {
		tty.conn, err = master.SyscallConn()
	}
	if err == nil {
		tty.exitConn, err = exitRead.SyscallConn()
	}
	if err != nil {
		tty.close()
		return nil, err
	}
	return tty, nil
}

// copyPTYOutput owns the sole PTY reader. The exit pipe wakes an idle poll
// without closing the master before its buffered output has been copied.
func copyPTYOutput(logf *os.File, tty *ptySession) {
	buf := make([]byte, 32*1024)
	timeout := -1
	var deadline time.Time
	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return
		}
		ready, exited, err := tty.poll(unix.POLLIN, timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return
		}
		if exited != 0 && deadline.IsZero() {
			// A descendant can retain or keep writing the slave after the
			// leader exits. Drain ready bytes, with a cap for continuous output.
			deadline = time.Now().Add(100 * time.Millisecond)
			timeout = 0
		}
		if ready&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
			if timeout == 0 || ready&unix.POLLNVAL != 0 {
				return
			}
			continue
		}
		var n int
		var readErr error
		if err := tty.conn.Control(func(fd uintptr) { n, readErr = unix.Read(int(fd), buf) }); err != nil {
			return
		}
		if n > 0 {
			if _, err := logf.Write(buf[:n]); err != nil {
				return
			}
		}
		if errors.Is(readErr, syscall.EAGAIN) || errors.Is(readErr, syscall.EINTR) {
			continue
		}
		if readErr != nil || n == 0 {
			return
		}
	}
}

func (tty *ptySession) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		var n int
		var writeErr error
		if err := tty.conn.Control(func(fd uintptr) { n, writeErr = unix.Write(int(fd), data) }); err != nil {
			return total, err
		}
		if n > 0 {
			total += n
			data = data[n:]
		}
		if errors.Is(writeErr, syscall.EINTR) {
			continue
		}
		if errors.Is(writeErr, syscall.EAGAIN) {
			for {
				ready, exited, err := tty.poll(unix.POLLOUT, -1)
				if errors.Is(err, syscall.EINTR) {
					continue
				}
				if err != nil {
					return total, err
				}
				if exited != 0 || ready&unix.POLLNVAL != 0 {
					return total, os.ErrClosed
				}
				break
			}
			continue
		}
		if writeErr != nil {
			return total, writeErr
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (tty *ptySession) poll(events int16, timeout int) (ready, exited int16, err error) {
	// Keep both descriptors alive through poll: completion can concurrently
	// close them while a blocked stdin writer is waking up.
	var exitErr, pollErr error
	err = tty.conn.Control(func(fd uintptr) {
		exitErr = tty.exitConn.Control(func(exitFD uintptr) {
			fds := []unix.PollFd{{Fd: int32(fd), Events: events}, {Fd: int32(exitFD), Events: unix.POLLIN}}
			_, pollErr = unix.Poll(fds, timeout)
			ready, exited = fds[0].Revents, fds[1].Revents
		})
	})
	return ready, exited, errors.Join(err, exitErr, pollErr)
}

func resizePTY(file *os.File, cols, rows int) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
	}); err != nil {
		return err
	}
	return ioctlErr
}

// configureProcessGroup makes the child lead its own process group so
// group signals reach every descendant.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// lookupProcessGroup returns the child's process group, falling back to
// the pid itself when the group cannot be read.
func lookupProcessGroup(pid int) int {
	if pgid, err := syscall.Getpgid(pid); err == nil {
		return pgid
	}
	return pid
}

// terminateProcessGroup asks the whole group to exit. A group that is
// already gone is success, not an error.
func terminateProcessGroup(pgid int) error {
	return signalProcessGroup(pgid, syscall.SIGTERM)
}

// killProcessGroup force-kills the whole group. A group that is already
// gone is success, not an error.
func killProcessGroup(pgid int) error {
	return signalProcessGroup(pgid, syscall.SIGKILL)
}

func signalProcessGroup(pgid int, signal syscall.Signal) error {
	if err := syscall.Kill(-pgid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// verifyProcessGroup reports whether pid still leads the recorded group.
// A changed group means the pid was reused; it must never be signaled.
func verifyProcessGroup(pid, pgid int) (bool, error) {
	currentPGID, err := syscall.Getpgid(pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}
	if currentPGID != pgid {
		return false, fmt.Errorf("group changed from %d to %d; refusing to signal it", pgid, currentPGID)
	}
	return true, nil
}

func processExists(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
