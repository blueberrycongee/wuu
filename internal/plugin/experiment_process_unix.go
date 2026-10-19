//go:build !windows

package plugin

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

const experimentProcessGroupsSupported = true

func configureExperimentProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateExperimentProcess(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if errors.Is(err, syscall.EPERM) {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	return err
}

func killExperimentProcess(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if errors.Is(err, syscall.EPERM) {
		return cmd.Process.Kill()
	}
	return err
}
