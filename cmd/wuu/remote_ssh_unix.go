//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func prepareRemoteSSHProcess(cmd *exec.Cmd) func() {
	// Keep the shell's foreground group when there is a controlling terminal.
	// OpenSSH reads authentication prompts from /dev/tty even when stdin is
	// redirected. Moving it to a background group breaks those prompts and the
	// shell's Ctrl-Z/fg job control.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		_ = tty.Close()
		return func() {}
	}
	if !errors.Is(err, syscall.ENXIO) && !errors.Is(err, syscall.ENODEV) && !errors.Is(err, os.ErrNotExist) {
		// An inaccessible terminal does not establish that this is a detached job.
		return func() {}
	}

	// Detached callers cannot broadcast terminal signals to SSH's ProxyJump or
	// ProxyCommand children. Own their group so cancellation during login also
	// removes them; OpenSSH has not installed its session cleanup at that point.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return func() {
		// A failed SSH login can exit before its proxy does, without cancellation.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}
