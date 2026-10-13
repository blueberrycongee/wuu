//go:build !windows

package pluginhost

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configurePluginProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killPluginProcess(cmd *exec.Cmd) error {
	// The group remains addressable after its leader exits while descendants
	// still hold the runtime's log pipes or other generation-owned resources.
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if errors.Is(err, syscall.EPERM) {
		// Darwin rejects a group whose members have all exited but are not yet
		// reaped. The leader is still our unreaped child, so signal it directly;
		// Wait then reports its real exit status.
		return cmd.Process.Kill()
	}
	return err
}

func pluginProcessWasKilled(state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}
