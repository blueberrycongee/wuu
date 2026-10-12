//go:build windows

package pluginhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func configurePluginProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func killPluginProcess(cmd *exec.Cmd) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	kill := exec.CommandContext(ctx, "taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t", "/f")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	kill.WaitDelay = pluginPipeDrainTimeout
	if err := kill.Run(); err != nil {
		return errors.Join(err, cmd.Process.Kill())
	}
	return nil
}

func pluginProcessWasKilled(state *os.ProcessState) bool {
	// Forceful Windows termination uses exit code 1. The caller also requires
	// a successful kill, so a failed taskkill cannot hide a runtime exit.
	return state != nil && state.ExitCode() == 1
}
