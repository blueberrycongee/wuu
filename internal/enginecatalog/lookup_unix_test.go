//go:build unix

package enginecatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProbeShellReadsMarkedPath(t *testing.T) {
	shell := "/bin/sh"
	if _, err := os.Stat(shell); err != nil {
		t.Skip(err)
	}
	first := filepath.Join(t.TempDir(), "bin")
	second := filepath.Join(t.TempDir(), "usr-bin")
	t.Setenv("PATH", first+string(os.PathListSeparator)+second)
	// Non-login so the developer rc file cannot replace PATH.
	got := probeShell(shell, []string{"-c", posixPathScript})
	want := []string{first, second}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("probeShell = %#v, want %#v", got, want)
	}
}
