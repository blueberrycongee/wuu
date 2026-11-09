//go:build linux

package process

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const linuxAuxClockTicks = 17

func readProcessIdentity(pid int) (string, time.Time, time.Duration, error) {
	if pid <= 1 {
		return "", time.Time{}, 0, fmt.Errorf("invalid process id %d", pid)
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", time.Time{}, 0, fmt.Errorf("read identity for process %d: %w", pid, err)
	}
	startTicks, err := parseLinuxProcessStartTicks(stat)
	if err != nil {
		return "", time.Time{}, 0, fmt.Errorf("read identity for process %d: %w", pid, err)
	}

	bootTime, bootErr := readLinuxBootTime()
	bootMarker, markerErr := readLinuxBootMarker(bootTime, bootErr)
	if markerErr != nil {
		return "", time.Time{}, 0, fmt.Errorf("read identity for process %d: %w", pid, markerErr)
	}
	identity := fmt.Sprintf("linux-v1:%s:%d", bootMarker, startTicks)

	clockTicks, clockErr := readLinuxClockTicks()
	if bootErr != nil || clockErr != nil {
		return identity, time.Time{}, 0, nil
	}
	startedAt := bootTime.Add(linuxTicksDuration(startTicks, clockTicks))
	precision := time.Second + time.Second/time.Duration(clockTicks)
	return identity, startedAt, precision, nil
}

func parseLinuxProcessStartTicks(stat []byte) (uint64, error) {
	text := strings.TrimSpace(string(stat))
	closing := strings.LastIndex(text, ") ")
	if closing < 0 {
		return 0, errors.New("malformed /proc process stat")
	}
	fields := strings.Fields(text[closing+2:])
	// The first field after the command is field 3 (state); starttime is field 22.
	if len(fields) <= 19 {
		return 0, errors.New("process stat is missing starttime")
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse process starttime: %w", err)
	}
	return startTicks, nil
}

func readLinuxBootTime() (time.Time, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(string(stat), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		seconds, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse Linux boot time: %w", err)
		}
		return time.Unix(seconds, 0), nil
	}
	return time.Time{}, errors.New("Linux boot time is unavailable")
}

func readLinuxBootMarker(bootTime time.Time, bootErr error) (string, error) {
	if bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		if marker := strings.TrimSpace(string(bootID)); marker != "" {
			return marker, nil
		}
	}
	if bootErr == nil {
		return "btime-" + strconv.FormatInt(bootTime.Unix(), 10), nil
	}
	return "", errors.New("Linux boot identity is unavailable")
}

func readLinuxClockTicks() (uint64, error) {
	auxv, err := os.ReadFile("/proc/self/auxv")
	if err != nil {
		return 0, err
	}
	wordBytes := strconv.IntSize / 8
	entryBytes := wordBytes * 2
	for offset := 0; offset+entryBytes <= len(auxv); offset += entryBytes {
		kind := readLinuxAuxWord(auxv[offset : offset+wordBytes])
		value := readLinuxAuxWord(auxv[offset+wordBytes : offset+entryBytes])
		if kind == linuxAuxClockTicks {
			if value == 0 {
				return 0, errors.New("Linux clock tick rate is zero")
			}
			return value, nil
		}
		if kind == 0 {
			break
		}
	}
	return 0, errors.New("Linux clock tick rate is unavailable")
}

func readLinuxAuxWord(value []byte) uint64 {
	if len(value) == 4 {
		return uint64(binary.NativeEndian.Uint32(value))
	}
	return binary.NativeEndian.Uint64(value)
}

func linuxTicksDuration(ticks, clockTicks uint64) time.Duration {
	seconds := ticks / clockTicks
	remainder := ticks % clockTicks
	return time.Duration(seconds)*time.Second + time.Duration(remainder)*time.Second/time.Duration(clockTicks)
}
