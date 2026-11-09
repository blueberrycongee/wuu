package shellpath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// gitBashInstallHint is surfaced whenever Windows command execution cannot
// find a bash interpreter.
const gitBashInstallHint = "Git Bash not found: install Git for Windows (https://git-scm.com/downloads/win) or set WUU_GIT_BASH_PATH to a bash.exe"

// findGitBash resolves the Git Bash interpreter on Windows. The chain is
// dependency-injected so the logic itself stays testable on every
// platform: env override → standard install roots → derive from git.exe →
// bare bash.exe on PATH (excluding the WSL shim under the system root,
// which cannot run commands against Windows paths).
func findGitBash(getenv func(string) string, fileExists func(string) bool, lookPath func(string) (string, error)) (string, error) {
	if override := strings.TrimSpace(getenv("WUU_GIT_BASH_PATH")); override != "" {
		if fileExists(override) {
			return override, nil
		}
		return "", fmt.Errorf("WUU_GIT_BASH_PATH %q does not exist", override)
	}
	roots := []string{
		getenv("ProgramFiles"),
		getenv("ProgramFiles(x86)"),
	}
	if localAppData := strings.TrimSpace(getenv("LOCALAPPDATA")); localAppData != "" {
		roots = append(roots, filepath.Join(localAppData, "Programs"))
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		candidate := filepath.Join(root, "Git", "bin", "bash.exe")
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	// Derive <install>\bin\bash.exe from a git.exe on PATH
	// (<install>\cmd\git.exe or <install>\bin\git.exe). LookPath results
	// that are not absolute (current-directory matches) are never trusted.
	if gitPath, err := lookPath("git"); err == nil && filepath.IsAbs(gitPath) {
		candidate := filepath.Join(filepath.Dir(filepath.Dir(gitPath)), "bin", "bash.exe")
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	if bashPath, err := lookPath("bash"); err == nil && filepath.IsAbs(bashPath) {
		if !pathWithin(getenv("SystemRoot"), bashPath) {
			return bashPath, nil
		}
	}
	return "", errors.New(gitBashInstallHint)
}

// pathWithin reports whether path sits under root, comparing
// case-insensitively because Windows paths are.
func pathWithin(root, path string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(root), strings.ToLower(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// gitBashCommandEnv fronts PATH with the Git install's tool directories
// so coreutils resolve deterministically without a login shell. Only
// directories that actually exist are injected: portable and MSYS2
// layouts differ, and a phantom PATH entry helps nothing. MSYS path/
// argument conversion is deliberately left at its defaults — models
// speak POSIX here, and /c/... arguments must keep reaching native
// programs converted.
func gitBashCommandEnv(env []string, bashPath string, dirExists func(string) bool) []string {
	binDir := filepath.Dir(bashPath)
	installRoot := filepath.Dir(binDir)
	// usr\bin\bash.exe (MinGit, MSYS2) sits one level deeper than
	// bin\bash.exe (Git for Windows).
	if strings.EqualFold(filepath.Base(binDir), "bin") && strings.EqualFold(filepath.Base(installRoot), "usr") {
		installRoot = filepath.Dir(installRoot)
	}
	var toolDirs []string
	for _, candidate := range []string{
		filepath.Join(installRoot, "usr", "bin"),
		filepath.Join(installRoot, "mingw64", "bin"),
	} {
		if dirExists(candidate) {
			toolDirs = append(toolDirs, candidate)
		}
	}
	if len(toolDirs) == 0 {
		return env
	}
	prepend := strings.Join(toolDirs, string(filepath.ListSeparator))

	out := make([]string, 0, len(env)+1)
	sawPath := false
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") && !sawPath {
			entry = key + "=" + prepend + string(filepath.ListSeparator) + value
			sawPath = true
		}
		out = append(out, entry)
	}
	if !sawPath {
		out = append(out, "PATH="+prepend)
	}
	return out
}
