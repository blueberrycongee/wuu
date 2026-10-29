//go:build linux

package processsandbox

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
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

// Other packages can remove their temporary directories after the helper has
// enumerated ordinary siblings of a protected root, but before grants are added.
func TestLinuxProtectedGrantSurvivesDisappearingSibling(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "a-workspace")
	disappearing := filepath.Join(root, "z-disappearing")
	for _, dir := range []string{workspace, disappearing} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	var granted []string
	err := linuxGrantPaths(root, []string{filepath.Join(root, "private")}, unix.LANDLOCK_ACCESS_FS_READ_DIR, func(path string, _ uint64) error {
		if path == workspace {
			if err := os.Remove(disappearing); err != nil {
				t.Fatal(err)
			}
		}
		fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS})
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			return &os.PathError{Op: "open sandbox grant", Path: path, Err: err}
		}
		defer unix.Close(fd)
		granted = append(granted, path)
		return nil
	})
	if err != nil {
		t.Fatalf("unrelated temporary directory cleanup aborted protected grants: %v", err)
	}
	if len(granted) != 1 || granted[0] != workspace {
		t.Fatalf("granted %v, want only the surviving workspace", granted)
	}
}

func TestLinuxProtectedGrantRejectsSymlinkSubstitution(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "a-workspace")
	replaced := filepath.Join(root, "z-replaced")
	protected := filepath.Join(root, "private")
	for _, dir := range []string{workspace, replaced, protected} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	err := linuxGrantPaths(root, []string{protected}, unix.LANDLOCK_ACCESS_FS_READ_DIR, func(path string, _ uint64) error {
		if path == workspace {
			if err := os.Remove(replaced); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(protected, replaced); err != nil {
				t.Fatal(err)
			}
		}
		fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS})
		if err != nil {
			return &os.PathError{Op: "open sandbox grant", Path: path, Err: err}
		}
		return unix.Close(fd)
	})
	if !errors.Is(err, unix.ELOOP) {
		t.Fatalf("symlink substitution must fail closed, got %v", err)
	}
}

func TestLinuxGrantPreservesExplicitRootAndPermissionFailures(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name      string
		protected []string
		failure   error
		explicit  bool
		openError bool
	}{
		{"missing explicit root", []string{filepath.Join(root, "other", "private")}, unix.ENOENT, true, true},
		{"missing root without protected paths", nil, unix.ENOENT, true, true},
		{"denied enumerated sibling", []string{filepath.Join(root, "private")}, unix.EACCES, false, true},
		{"forbidden enumerated sibling", []string{filepath.Join(root, "private")}, unix.EPERM, false, true},
		{"non-open missing sibling", []string{filepath.Join(root, "private")}, unix.ENOENT, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grantRoot := filepath.Join(root, "workspace")
			if !tc.explicit {
				grantRoot = root
				if err := os.MkdirAll(filepath.Join(root, "ordinary"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := linuxGrantPaths(grantRoot, tc.protected, unix.LANDLOCK_ACCESS_FS_READ_DIR, func(path string, _ uint64) error {
				if !tc.openError {
					return tc.failure
				}
				return &os.PathError{Op: "open sandbox grant", Path: path, Err: tc.failure}
			})
			if !errors.Is(err, tc.failure) {
				t.Fatalf("grant failure was ignored: got %v, want %v", err, tc.failure)
			}
		})
	}
}
