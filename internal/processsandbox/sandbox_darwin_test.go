//go:build darwin

package processsandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeatbeltWorkspaceWriteAllowsRootAndDeniesSibling(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	insideFile := filepath.Join(workspace, "inside")
	outsideFile := filepath.Join(outside, "outside")
	cmd := exec.Command("/bin/sh", "-c", `printf inside > "$1"; printf outside > "$2"`, "_", insideFile, outsideFile)
	if err := Apply(cmd, Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{workspace}}); err != nil {
		t.Fatalf("apply sandbox: %v", err)
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("outside write unexpectedly succeeded")
	}
	if !IsDenied(cmd.ProcessState.ExitCode(), string(output)) {
		t.Fatalf("outside write was not reported as denied: %s", output)
	}
	if got, err := os.ReadFile(insideFile); err != nil || string(got) != "inside" {
		t.Fatalf("inside write = %q, %v", got, err)
	}
	if _, err := os.Stat(outsideFile); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}
}

func TestSeatbeltReadOnlyDeniesWrite(t *testing.T) {
	target := filepath.Join(t.TempDir(), "blocked")
	cmd := exec.Command("/bin/sh", "-c", `printf blocked > "$1"`, "_", target)
	if err := Apply(cmd, Policy{Mode: ModeReadOnly}); err != nil {
		t.Fatalf("apply sandbox: %v", err)
	}
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(strings.ToLower(string(output)), "operation not permitted") {
		t.Fatalf("read-only write result err=%v output=%s", err, output)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("read-only target exists or stat failed unexpectedly: %v", err)
	}
}

func TestSeatbeltWorkspaceWriteDeniesSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "blocked")
	cmd := exec.Command("/bin/sh", "-c", `printf blocked > "$1"`, "_", target)
	if err := Apply(cmd, Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{workspace}}); err != nil {
		t.Fatalf("apply sandbox: %v", err)
	}
	output, err := cmd.CombinedOutput()
	if err == nil || !IsDenied(cmd.ProcessState.ExitCode(), string(output)) {
		t.Fatalf("symlink escape result err=%v output=%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(outside, "blocked")); !os.IsNotExist(err) {
		t.Fatalf("symlink escape wrote outside or stat failed unexpectedly: %v", err)
	}
}

// System root certificates exercise Keychain's MDS dependency without reading
// user credentials or changing the user's keychain search list.
func TestSeatbeltAllowsSystemKeychainRead(t *testing.T) {
	const keychain = "/System/Library/Keychains/SystemRootCertificates.keychain"
	args := []string{"find-certificate", "-c", "Apple Root CA", keychain}
	if output, err := exec.Command("/usr/bin/security", args...).CombinedOutput(); err != nil {
		t.Fatalf("system keychain baseline: %v: %s", err, output)
	}
	for _, mode := range []Mode{ModeReadOnly, ModeWorkspaceWrite} {
		t.Run(string(mode), func(t *testing.T) {
			cmd := exec.Command("/usr/bin/security", args...)
			if err := Apply(cmd, Policy{Mode: mode, WritableRoots: []string{t.TempDir()}}); err != nil {
				t.Fatal(err)
			}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("confined system keychain read: %v: %s", err, output)
			}
		})
	}
}

func TestSeatbeltDeniesWritesToOtherUserCaches(t *testing.T) {
	output, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_CACHE_DIR").Output()
	if err != nil {
		t.Fatal(err)
	}
	outside, err := os.MkdirTemp(strings.TrimSpace(string(output)), "wuu-sandbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	for _, mode := range []Mode{ModeReadOnly, ModeWorkspaceWrite} {
		t.Run(string(mode), func(t *testing.T) {
			target := filepath.Join(outside, string(mode))
			cmd := exec.Command("/bin/sh", "-c", `printf blocked > "$1"`, "_", target)
			if err := Apply(cmd, Policy{Mode: mode, WritableRoots: []string{t.TempDir()}}); err != nil {
				t.Fatal(err)
			}
			output, err := cmd.CombinedOutput()
			if err == nil || !IsDenied(cmd.ProcessState.ExitCode(), string(output)) {
				t.Fatalf("cache write result err=%v output=%s", err, output)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("cache target exists or stat failed: %v", err)
			}
		})
	}
}
