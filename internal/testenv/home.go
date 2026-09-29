// Package testenv isolates test processes from developer configuration and credentials.
package testenv

import (
	"fmt"
	"os"
	"testing"
)

// Run isolates user state before running a package's tests. Individual tests can
// still select their own HOME or WUU_HOME without inheriting the launching app's state.
func Run(m *testing.M) int {
	home, err := os.MkdirTemp("", "wuu-test-home-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(home)
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	// Overrides outrank explicit home arguments in the state and OAuth stores.
	for _, key := range []string{"WUU_HOME", "CODEX_HOME"} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}
