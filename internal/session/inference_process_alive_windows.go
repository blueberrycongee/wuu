//go:build windows

package session

import (
	"errors"

	"golang.org/x/sys/windows"
)

func inferenceRuntimeProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied still proves that the PID currently exists.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		// Stay conservative when Windows cannot report the state of an open
		// process handle; recovery must not race a possibly live inference.
		return true
	}
	return exitCode == uint32(windows.STATUS_PENDING)
}
