//go:build unix

package enginecatalog

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const loginShellProbeTimeout = 3 * time.Second

const loginShellProbeTailSize = 256 * 1024

// Interactive and login so both ~/.zshrc (nvm, mise) and ~/.zprofile
// (Homebrew shellenv) contribute. The marker is printed by the -c command,
// after the rc file has finished.
const posixPathScript = `set +x; command printf '%s%s%s' '__WUU_PATH_START__' "$PATH" '__WUU_PATH_END__'`

const fishPathScript = `printf '%s%s%s' '__WUU_PATH_START__' (string join : $PATH) '__WUU_PATH_END__'`

func readLoginShellPath() []string {
	// Test binaries must not execute the developer's login shell.
	if testing.Testing() {
		return nil
	}
	shell := loginShell()
	args := loginShellArgs(shell)
	if shell == "" || len(args) == 0 {
		return nil
	}
	return probeShell(shell, args)
}

func loginShell() string {
	if shell := absoluteExecutable(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	if runtime.GOOS == "darwin" {
		if shell := absoluteExecutable("/bin/zsh"); shell != "" {
			return shell
		}
	}
	if shell := absoluteExecutable("/bin/bash"); shell != "" {
		return shell
	}
	return absoluteExecutable("/bin/sh")
}

func absoluteExecutable(path string) string {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	return path
}

func loginShellArgs(shell string) []string {
	switch filepath.Base(shell) {
	case "fish":
		return []string{"-l", "-c", fishPathScript}
	case "csh", "tcsh", "nu", "nushell":
		return nil
	default:
		return []string{"-i", "-l", "-c", posixPathScript}
	}
}

func probeShell(shell string, args []string) []string {
	if shell == "" || len(args) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginShellProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, args...)
	// Stdin must not be the app-server pipe; a login shell that reads stdin
	// would consume the JSON-RPC stream.
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 200 * time.Millisecond
	configureShellProbe(cmd)
	if home, err := os.UserHomeDir(); err == nil {
		if info, statErr := os.Stat(home); statErr == nil && info.IsDir() {
			cmd.Dir = home
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	tail := readLast(stdout, loginShellProbeTailSize)
	_ = cmd.Wait()
	return parseMarkedPath(string(tail))
}

func configureShellProbe(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// The shell and anything it started share a group. Killing only the
		// leader leaves a child holding the stdout pipe until WaitDelay.
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			return cmd.Process.Kill()
		}
		return nil
	}
}

func readLast(r io.Reader, limit int) []byte {
	buf := make([]byte, 32*1024)
	var tail []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			tail = append(tail, buf[:n]...)
			if len(tail) > limit {
				tail = append([]byte(nil), tail[len(tail)-limit:]...)
			}
		}
		if err != nil {
			return tail
		}
	}
}
