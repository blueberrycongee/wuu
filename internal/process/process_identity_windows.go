//go:build windows

package process

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// readProcessIdentity fingerprints pid by its kernel creation time
// (100ns FILETIME units). Creation time survives for the life of the
// process and changes on pid reuse, matching the role /proc starttime
// plays on Linux.
func readProcessIdentity(pid int) (string, time.Time, time.Duration, error) {
	if pid <= 1 {
		return "", time.Time{}, 0, fmt.Errorf("invalid process id %d", pid)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", time.Time{}, 0, fmt.Errorf("read identity for process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", time.Time{}, 0, fmt.Errorf("read identity for process %d: %w", pid, err)
	}
	ticks := (uint64(creation.HighDateTime) << 32) | uint64(creation.LowDateTime)
	if ticks == 0 {
		return "", time.Time{}, 0, fmt.Errorf("process %d has no start time", pid)
	}
	identity := fmt.Sprintf("windows-v1:%d", ticks)
	startedAt := time.Unix(0, creation.Nanoseconds())
	return identity, startedAt, 100 * time.Nanosecond, nil
}
