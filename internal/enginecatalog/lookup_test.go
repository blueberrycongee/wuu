package enginecatalog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func withIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg"))
	t.Setenv("MISE_DATA_DIR", filepath.Join(home, "mise"))
	// A directory outside the home so the process PATH looks like a graphical
	// launch: no user bin directory and no Homebrew.
	t.Setenv("PATH", t.TempDir())
	resetPathProbeState()
	t.Cleanup(resetPathProbeState)
	return home
}

func writeExec(t *testing.T, path string) string {
	t.Helper()
	if runtime.GOOS == "windows" && filepath.Ext(path) == "" {
		path += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		content = []byte("placeholder")
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookBinaryFindsHomeInstallWhenPathIsSystemOnly(t *testing.T) {
	home := withIsolatedHome(t)
	binary := writeExec(t, filepath.Join(home, ".local", "bin", "claude"))
	got, err := LookBinary("claude")
	if err != nil {
		t.Fatalf("LookBinary: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binary) {
		t.Fatalf("LookBinary = %q, want %q", got, binary)
	}
}

func TestLookBinaryPrefersPathOverInstallDir(t *testing.T) {
	home := withIsolatedHome(t)
	pathDir := t.TempDir()
	onPath := writeExec(t, filepath.Join(pathDir, "claude"))
	writeExec(t, filepath.Join(home, ".local", "bin", "claude"))
	t.Setenv("PATH", pathDir)
	resetPathProbeState()

	got, err := LookBinary("claude")
	if err != nil {
		t.Fatalf("LookBinary: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(onPath) {
		t.Fatalf("LookBinary = %q, want PATH entry %q", got, onPath)
	}
}

func TestLookBinaryUsesLoginShellWhenInstallDirsMiss(t *testing.T) {
	withIsolatedHome(t)
	dir := t.TempDir()
	binary := writeExec(t, filepath.Join(dir, "wuu-shell-fixture"))
	prev := loadLoginShellDirs
	loadLoginShellDirs = func() []string { return []string{dir} }
	t.Cleanup(func() { loadLoginShellDirs = prev })

	got, err := LookBinary("wuu-shell-fixture")
	if err != nil {
		t.Fatalf("LookBinary: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binary) {
		t.Fatalf("LookBinary = %q, want %q", got, binary)
	}
}

func TestLookBinarySkipsLoginShellWhenPathHasUserDir(t *testing.T) {
	home := withIsolatedHome(t)
	userBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(userBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", userBin)
	resetPathProbeState()
	prev := loadLoginShellDirs
	loadLoginShellDirs = func() []string {
		t.Error("login shell probed even though PATH already has a user directory")
		return []string{t.TempDir()}
	}
	t.Cleanup(func() { loadLoginShellDirs = prev })

	if _, err := LookBinary("wuu-lookup-not-installed"); err == nil {
		t.Fatal("missing executable was resolved")
	}
}

func TestLookBinaryFindsNvmDefaultAlias(t *testing.T) {
	home := withIsolatedHome(t)
	root := filepath.Join(home, ".nvm")
	t.Setenv("NVM_DIR", root)
	if err := os.WriteFile(mustAlias(t, filepath.Join(root, "alias", "default")), []byte("lts/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mustAlias(t, filepath.Join(root, "alias", "lts", "*")), []byte("v22.14.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An older install must not win over the default alias.
	writeExec(t, filepath.Join(root, "versions", "node", "v20.11.0", "bin", "wuu-nvm-fixture"))
	binary := writeExec(t, filepath.Join(root, "versions", "node", "v22.14.0", "bin", "wuu-nvm-fixture"))

	got, err := LookBinary("wuu-nvm-fixture")
	if err != nil {
		t.Fatalf("LookBinary: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binary) {
		t.Fatalf("LookBinary = %q, want default alias %q", got, binary)
	}
}

func TestLookBinaryNvmPartialVersionPicksHighest(t *testing.T) {
	home := withIsolatedHome(t)
	root := filepath.Join(home, ".nvm")
	t.Setenv("NVM_DIR", root)
	if err := os.WriteFile(mustAlias(t, filepath.Join(root, "alias", "default")), []byte("20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(root, "versions", "node", "v20.9.0", "bin", "wuu-nvm-fixture"))
	binary := writeExec(t, filepath.Join(root, "versions", "node", "v20.11.0", "bin", "wuu-nvm-fixture"))

	got, err := LookBinary("wuu-nvm-fixture")
	if err != nil {
		t.Fatalf("LookBinary: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binary) {
		t.Fatalf("LookBinary = %q, want highest v20 %q", got, binary)
	}
}

func TestLookBinaryIgnoresNvmVersionsWithoutDefaultAlias(t *testing.T) {
	home := withIsolatedHome(t)
	root := filepath.Join(home, ".nvm")
	t.Setenv("NVM_DIR", root)
	writeExec(t, filepath.Join(root, "versions", "node", "v22.14.0", "bin", "wuu-nvm-fixture"))
	if _, err := LookBinary("wuu-nvm-fixture"); err == nil {
		t.Fatal("an nvm version without a default alias must not be selected")
	}
}

func TestResolveFindsCursorAgentInLocalBin(t *testing.T) {
	home := withIsolatedHome(t)
	t.Setenv("WUU_CURSOR_BINARY", "")
	binary := writeExec(t, filepath.Join(home, ".local", "bin", "cursor-agent"))
	entry, ok := Lookup("cursor")
	if !ok {
		t.Fatal("cursor catalog entry missing")
	}
	got, err := entry.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binary) {
		t.Fatalf("Resolve = %q, want %q", got, binary)
	}
}

func TestResolveInvalidOverrideDoesNotSearchInstallDirs(t *testing.T) {
	home := withIsolatedHome(t)
	writeExec(t, filepath.Join(home, ".local", "bin", "cursor-agent"))
	entry, ok := Lookup("cursor")
	if !ok {
		t.Fatal("cursor catalog entry missing")
	}
	override := filepath.Join(home, "missing", "cursor-agent")
	if _, err := entry.Resolve(override); err == nil {
		t.Fatal("invalid override fell through to ~/.local/bin")
	}
}

func TestInstallUserPathPrependsInstallDirs(t *testing.T) {
	home := withIsolatedHome(t)
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	original := os.Getenv("PATH")
	InstallUserPath()
	got := filepath.SplitList(os.Getenv("PATH"))
	if len(got) == 0 || filepath.Clean(got[0]) != filepath.Clean(localBin) {
		t.Fatalf("PATH = %q, want %s first", os.Getenv("PATH"), localBin)
	}
	foundOriginal := false
	count := 0
	for _, dir := range got {
		if filepath.Clean(dir) == filepath.Clean(localBin) {
			count++
		}
		if filepath.Clean(dir) == filepath.Clean(original) {
			foundOriginal = true
		}
	}
	if count != 1 {
		t.Fatalf("install dir appears %d times in PATH %q", count, os.Getenv("PATH"))
	}
	if !foundOriginal {
		t.Fatalf("PATH = %q, dropped original %s", os.Getenv("PATH"), original)
	}
	InstallUserPath()
	if strings.Count(os.Getenv("PATH"), localBin) != 1 {
		t.Fatalf("second install duplicated PATH: %s", os.Getenv("PATH"))
	}
}

func TestParseMarkedPathKeepsLastAbsolutePair(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "old")
	second := filepath.Join(root, "real")
	sep := string(os.PathListSeparator)
	noise := pathMarkerStart + strings.Join([]string{first, "relative"}, sep) + pathMarkerEnd
	real := pathMarkerStart + strings.Join([]string{second, "relative", second}, sep) + pathMarkerEnd
	got := parseMarkedPath("startup " + noise + "\n" + real + "\n")
	want := []string{second}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("parseMarkedPath = %#v, want %#v", got, want)
	}
}

func mustAlias(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
