//go:build windows

package hooks

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func configureHookProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
	cmd.Cancel = func() error {
		// Use an independent, bounded context: the hook context is already done.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		kill := exec.CommandContext(ctx, "taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t", "/f")
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := kill.Run(); err != nil {
			return errors.Join(err, cmd.Process.Kill())
		}
		return nil
	}
}
