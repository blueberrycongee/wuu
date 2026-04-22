package context

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkSnapshot measures the cost of wuu's per-turn git context injection.
// This runs git rev-parse --abbrev-ref HEAD and git status --short every call.
func BenchmarkSnapshot(b *testing.B) {
	cwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	// Walk up to find the git root (bench runs from package dir)
	root := cwd
	for {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			b.Skip("not in a git repo")
		}
		root = parent
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Snapshot(root)
	}
}

// BenchmarkGitBranchOnly isolates the branch lookup cost.
func BenchmarkGitBranchOnly(b *testing.B) {
	cwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	root := cwd
	for {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			b.Skip("not in a git repo")
		}
		root = parent
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = gitBranch(root)
	}
}

// BenchmarkGitStatusOnly isolates the status lookup cost.
func BenchmarkGitStatusOnly(b *testing.B) {
	cwd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	root := cwd
	for {
		if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			b.Skip("not in a git repo")
		}
		root = parent
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = gitStatusSummary(root)
	}
}
