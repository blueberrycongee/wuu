package gitattribution

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateRealGitRejectsWrapperItself(t *testing.T) {
	binDir := t.TempDir()
	wrapperPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(wrapperPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := validateRealGit(wrapperPath, wrapperPath); err == nil || !strings.Contains(err.Error(), "wrapper itself") {
		t.Fatalf("validateRealGit() error = %v, want self-wrapper rejection", err)
	}
}

func TestValidateRealGitAcceptsDifferentGitExecutable(t *testing.T) {
	root := t.TempDir()
	wrapperPath := filepath.Join(root, "wrapper", "git")
	realGit := filepath.Join(root, "real", "git")
	for _, path := range []string{wrapperPath, realGit} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := validateRealGit(wrapperPath, realGit); err != nil {
		t.Fatalf("validateRealGit() error = %v", err)
	}
}

func TestResolveRealGitExecutableAddsWindowsSuffix(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific executable resolution")
	}

	realGit := filepath.Join(t.TempDir(), "git")
	executablePath := realGit + ".exe"
	if err := os.WriteFile(executablePath, nil, 0o700); err != nil {
		t.Fatal(err)
	}

	if got := resolveRealGitExecutable(realGit); got != executablePath {
		t.Fatalf("resolveRealGitExecutable() = %q, want %q", got, executablePath)
	}
}

func TestResolveRealGitExecutableKeepsExistingWindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific executable resolution")
	}

	realGit := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(realGit, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realGit+".exe", nil, 0o700); err != nil {
		t.Fatal(err)
	}

	if got := resolveRealGitExecutable(realGit); got != realGit {
		t.Fatalf("resolveRealGitExecutable() = %q, want original path %q", got, realGit)
	}
}

func TestResolveRealGitExecutableKeepsMissingWindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific executable resolution")
	}

	realGit := filepath.Join(t.TempDir(), "git")
	if got := resolveRealGitExecutable(realGit); got != realGit {
		t.Fatalf("resolveRealGitExecutable() = %q, want original missing path %q", got, realGit)
	}
}

func TestResolveRealGitExecutableKeepsWindowsExeExtension(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific executable resolution")
	}

	realGit := filepath.Join(t.TempDir(), "git.EXE")
	if got := resolveRealGitExecutable(realGit); got != realGit {
		t.Fatalf("resolveRealGitExecutable() = %q, want original .EXE path %q", got, realGit)
	}
}

func TestResolveRealGitExecutableStillRejectsWrapperExe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific executable resolution")
	}

	realGit := filepath.Join(t.TempDir(), "git")
	wrapperPath := realGit + ".exe"
	if err := os.WriteFile(wrapperPath, nil, 0o700); err != nil {
		t.Fatal(err)
	}

	resolved := resolveRealGitExecutable(realGit)
	if err := validateRealGit(wrapperPath, resolved); err == nil || !strings.Contains(err.Error(), "wrapper itself") {
		t.Fatalf("validateRealGit() error = %v, want self-wrapper rejection after resolving %q", err, resolved)
	}
}

func TestWrapperScriptPassesItsPathToDispatcher(t *testing.T) {
	script := wrapperScript("/opt/wuu", "/state/git-wrapper/bin/git")
	if !strings.Contains(script, "__wuu_internal_git_wrapper '/state/git-wrapper/bin/git' \"$real_git\"") {
		t.Fatalf("wrapper script does not pass its own path:\n%s", script)
	}
}
