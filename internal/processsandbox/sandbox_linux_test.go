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

// The deny boundary includes future roots: no grant may cover a protected
// ancestor, a protected descendant, or a symlink alias into the protected tree.
func TestLinuxProtectedGrantDecomposition(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"allowed", "state/private", "state/public"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "state", "ordinary"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "state", "private"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	protected := []string{filepath.Join(root, "state", "private"), filepath.Join(root, "future", "private")}
	grants, err := linuxAllowedPaths(root, protected)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, grant := range grants {
		allowed[grant] = true
		if grant == filepath.Join(root, "alias") {
			t.Fatal("symlink alias was granted")
		}
		for _, denied := range protected {
			if pathWithin(grant, denied) || pathWithin(denied, grant) {
				t.Fatalf("grant %q overlaps exclusion %q", grant, denied)
			}
		}
	}
	for _, path := range []string{filepath.Join(root, "allowed"), filepath.Join(root, "state", "public"), filepath.Join(root, "state", "ordinary")} {
		if !allowed[path] {
			t.Fatalf("ordinary sibling missing from grants: %q (%v)", path, grants)
		}
	}
}

func TestLinuxProtectedBoundaryPreservesOrdinaryWorkspaceShell(t *testing.T) {
	if !Supported() {
		t.Skip("host kernel lacks Landlock; real protected-shell enforcement is unverified here")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "not-yet-provisioned", "private")
	cmd := exec.Command("/bin/sh", "-c", `printf ordinary > "$1/created"; cat "$1/created"`, "sh", workspace)
	if err := Apply(cmd, Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{workspace}, ProtectedPaths: []string{protected}}); err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "ordinary" {
		t.Fatalf("ordinary workspace shell failed: %s %v", out, err)
	}
}
