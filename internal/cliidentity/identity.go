// Package cliidentity records the authoring CLI explicitly provided by the
// process bootstrap. Library hosts do not implicitly become Wuu CLIs.
package cliidentity

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const EnvironmentVariable = "WUU_CLI_EXECUTABLE"

var executable atomic.Pointer[string]

var registerCurrentExecutable = sync.OnceValue(func() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	// The public shell tool uses Bash, including Git Bash on Windows.
	path = filepath.ToSlash(path)
	executable.Store(&path)
	return nil
})

// RegisterCurrentExecutable is called only by a bootstrap implementing the
// public Wuu CLI. Registration is immutable for the process lifetime.
func RegisterCurrentExecutable() error { return registerCurrentExecutable() }

// Executable is empty in hosts that have not registered an authoring CLI.
func Executable() string {
	if path := executable.Load(); path != nil {
		return *path
	}
	return ""
}
