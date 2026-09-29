//go:build !darwin && !linux

package processsandbox

import (
	"fmt"
	"os/exec"
	"runtime"
)

func platformSupported() bool { return false }

func applyPlatform(_ *exec.Cmd, _ Policy) error {
	return fmt.Errorf("%w on %s", ErrUnavailable, runtime.GOOS)
}
