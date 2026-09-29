//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

type processGroup struct{}

func configureProcessGroup(cmd *exec.Cmd) *processGroup {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &processGroup{}
}

func startProcessGroup(_ *exec.Cmd, _ *processGroup) error {
	return nil
}

func killProcessTree(cmd *exec.Cmd, _ *processGroup) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

func closeProcessGroup(_ *processGroup) error {
	return nil
}
