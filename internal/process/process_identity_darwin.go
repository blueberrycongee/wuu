//go:build darwin

package process

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func readProcessIdentity(pid int) (string, time.Time, time.Duration, error) {
	if pid <= 1 {
		return "", time.Time{}, 0, fmt.Errorf("invalid process id %d", pid)
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", time.Time{}, 0, fmt.Errorf("read identity for process %d: %w", pid, err)
	}
	if int(info.Proc.P_pid) != pid {
		return "", time.Time{}, 0, fmt.Errorf("process %d has no identity", pid)
	}
	started := info.Proc.P_starttime
	if started.Sec <= 0 {
		return "", time.Time{}, 0, fmt.Errorf("process %d has no start time", pid)
	}
	startedAt := time.Unix(started.Sec, int64(started.Usec)*int64(time.Microsecond))
	identity := fmt.Sprintf("darwin-v1:%d:%d", started.Sec, started.Usec)
	return identity, startedAt, time.Microsecond, nil
}
