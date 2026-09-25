package processsandbox

import (
	"path/filepath"
	"testing"
)

func TestNormalizedPolicyCanonicalizesAndDeduplicatesWritableRoots(t *testing.T) {
	root := t.TempDir()
	policy := normalizedPolicy(Policy{
		Mode:          ModeWorkspaceWrite,
		WritableRoots: []string{root, filepath.Join(root, "."), ""},
	})
	if len(policy.WritableRoots) != 1 {
		t.Fatalf("writable roots = %#v, want one canonical root", policy.WritableRoots)
	}
	if !filepath.IsAbs(policy.WritableRoots[0]) {
		t.Fatalf("writable root is not absolute: %q", policy.WritableRoots[0])
	}
}

func TestSandboxFailureClassification(t *testing.T) {
	if !IsDenied(1, "bash: x: Operation not permitted") {
		t.Fatal("Seatbelt denial was not classified")
	}
	if IsDenied(0, "Operation not permitted") {
		t.Fatal("successful command was classified as denied")
	}
	if !IsRunnerFailure(1, "sandbox-exec: sandbox_init: invalid profile") {
		t.Fatal("runner failure was not classified")
	}
}
