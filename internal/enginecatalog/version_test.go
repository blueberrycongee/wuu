//go:build unix

package enginecatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Version discovery is a read-only protocol boundary: failures and arbitrary
// diagnostic output must never masquerade as an installed CLI version.
func TestObserveVersion(t *testing.T) {
	for _, tc := range []struct{ name, id, script, want string }{
		{"codex", "codex", `test "$#" = 1 && test "$1" = --version || exit 9; echo 'codex-cli 0.114.0'`, "0.114.0"},
		{"claude", "claude", `echo '2.1.226 (Claude Code)'`, "2.1.226"},
		{"unexpected suffix", "claude", `echo '2.1.226 (fake)'`, ""},
		{"failure", "codex", `echo 'codex-cli 0.114.0'; exit 1`, ""},
		{"diagnostic", "codex", `echo 'Please sign in with your account'`, ""},
		{"multiline", "codex", `printf 'codex-cli 0.114.0\nextra output\n'`, ""},
		{"oversized", "codex", `printf "codex-cli 0.114.0"; i=0; while [ "$i" -lt 5000 ]; do printf " "; i=$((i+1)); done`, ""},
		{"unsupported", "unknown", `echo ran > "$0.ran"; echo '1.2.3'`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "fake-cli")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+tc.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if got := ObserveVersion(context.Background(), tc.id, binary); got != tc.want {
				t.Fatalf("version = %q, want %q", got, tc.want)
			}
			if _, err := os.Stat(binary + ".ran"); !os.IsNotExist(err) {
				t.Fatal("unsupported engine was executed")
			}
		})
	}
}

func TestObserveVersionCancellation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake-cli")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if got := ObserveVersion(ctx, "codex", binary); got != "" {
		t.Fatalf("cancelled version = %q", got)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("version observation ignored cancellation")
	}
}
