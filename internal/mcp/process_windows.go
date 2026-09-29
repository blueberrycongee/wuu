//go:build windows

package mcp

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
