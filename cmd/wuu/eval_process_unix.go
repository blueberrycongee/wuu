//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Evaluation subprocesses cannot leave candidate-owned children behind after
// the parent exits. Cleanup applies to both successful and failed processes.
func prepareEvalProcess(command *exec.Cmd) (func(), error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return func() {
		if command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
	}, nil
}
