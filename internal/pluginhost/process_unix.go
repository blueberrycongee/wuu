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
	return err
}
