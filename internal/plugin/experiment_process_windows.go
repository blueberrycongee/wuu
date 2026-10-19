//go:build windows

package plugin

import (
	"errors"
	"os/exec"
)

// taskkill cannot reliably retire descendants after their leader has exited.
// Keep evaluation unavailable until Windows has equivalent job-object ownership.
const experimentProcessGroupsSupported = false

func configureExperimentProcess(_ *exec.Cmd) {}

func terminateExperimentProcess(_ *exec.Cmd) error {
	return errors.New("experiment process groups are unavailable on Windows")
}

func killExperimentProcess(_ *exec.Cmd) error {
	return errors.New("experiment process groups are unavailable on Windows")
}
