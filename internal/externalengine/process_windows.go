//go:build windows

package externalengine

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func configureChild(cmd *exec.Cmd) {}
func killChild(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
