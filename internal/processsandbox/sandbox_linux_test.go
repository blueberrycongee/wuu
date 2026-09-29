//go:build linux

package processsandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLinuxFilesystemBoundary(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	if err := os.Mkdir(allowed, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, command string
		policy        Policy
		success       bool
	}{
		{"allowed write", "printf yes > \"$1/allowed/result\"", Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{allowed}}, true},
		{"outside create", "touch \"$1/escape\"", Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{allowed}}, false},
		{"outside truncate", ": > \"$1/outside\"", Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{allowed}}, false},
		{"readonly write", "touch \"$1/allowed/denied\"", Policy{Mode: ModeReadOnly}, false},
		{"readonly read", "cat \"$1/outside\"", Policy{Mode: ModeReadOnly}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", tc.command, "sh", root)
			if err := Apply(cmd, tc.policy); err != nil {
				if errors.Is(err, ErrUnavailable) {
					t.Skip(err)
				}
				t.Fatal(err)
			}
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.success {
				t.Fatalf("success=%v output=%s error=%v", tc.success, out, err)
			}
		})
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}
