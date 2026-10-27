//go:build windows

package main

import (
	"errors"
	"os/exec"
)

func prepareEvalProcess(_ *exec.Cmd) (func(), error) {
	return nil, errors.New("independent experiment subprocesses require POSIX process-group cleanup")
}
