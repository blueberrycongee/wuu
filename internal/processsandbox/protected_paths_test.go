package processsandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A provider compiled before protected paths existed must not be accepted just
// because it reports the older write-boundary contract as fully enforced.
func TestProtectedPathsRejectLegacyProvider(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "echo bypass")
	_, err := ApplyWithProvider(context.Background(), cmd, Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{"/"}, ProtectedPaths: []string{t.TempDir()}}, testProvider{result: ConfinedCommand{Argv: []string{"/bin/sh", "-c", "echo bypass"}, Enforcement: EnforcementFull}})
	if err == nil {
		t.Fatal("legacy provider silently dropped protected-path enforcement")
	}
}

func TestProtectedPathsProcessReadWriteBoundary(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "private")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(protected, "memory")
	if err := os.WriteFile(secret, []byte("DOMAIN_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{secret, filepath.Join(alias, "memory")} {
		for _, command := range []string{`cat "$1"`, `printf overwritten > "$1"`, `/bin/sh -c 'cat "$1"' child "$1"`} {
			cmd := exec.Command("/bin/sh", "-c", command, "sh", target)
			policy := Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{root}, ProtectedPaths: []string{protected}}
			if err := Apply(cmd, policy); err != nil {
				t.Logf("protected process failed closed before launch: %v", err)
				continue
			}
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("protected command succeeded: %q output=%s", command, out)
			}
			if string(out) == "DOMAIN_SECRET" {
				t.Fatal("protected memory escaped")
			}
		}
	}
	data, err := os.ReadFile(secret)
	if err != nil || string(data) != "DOMAIN_SECRET" {
		t.Fatalf("protected data modified: %q %v", data, err)
	}
}

func TestProtectedPathsProcessAllowsOrdinarySibling(t *testing.T) {
	if !Supported() {
		t.Skip("host sandbox unavailable; actual OS enforcement remains unverified")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `printf ordinary > "$1/file"; cat "$1/file"`, "sh", workspace)
	if err := Apply(cmd, Policy{Mode: ModeWorkspaceWrite, WritableRoots: []string{workspace}, ProtectedPaths: []string{filepath.Join(root, "future-private")}}); err != nil {
		t.Fatal(err)
	}
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "ordinary" {
		t.Fatalf("ordinary operation with protected roots failed: %s %v", out, err)
	}
}
