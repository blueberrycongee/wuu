//go:build windows

package main

import "os/exec"

func prepareRemoteSSHProcess(_ *exec.Cmd) func() {
	return func() {}
}
