package gitattribution

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const originalPathEnv = "WUU_INTERNAL_GIT_ORIGINAL_PATH"

var wrapperState struct {
	sync.Mutex
	fallbackDir string
}

// EnsureWrapper creates a private PATH entry containing a git launcher. The
// launcher delegates argv parsing to the WUU executable instead of rewriting
// shell source text.
func EnsureWrapper(executable, stateDir string) (string, error) {
	wrapperState.Lock()
	defer wrapperState.Unlock()

	if strings.TrimSpace(executable) == "" {
		resolved, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve WUU executable for git attribution: %w", err)
		}
		executable = resolved
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("resolve WUU executable path: %w", err)
	}
	if strings.TrimSpace(stateDir) == "" {
		if wrapperState.fallbackDir == "" {
			fallbackDir, err := os.MkdirTemp("", "wuu-git-wrapper-")
			if err != nil {
				return "", fmt.Errorf("create temporary git wrapper directory: %w", err)
			}
			wrapperState.fallbackDir = fallbackDir
		}
		stateDir = wrapperState.fallbackDir
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(executable)))[:12]
	binDir := filepath.Join(stateDir, "git-wrapper", digest, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return "", fmt.Errorf("create git wrapper directory: %w", err)
	}

	scriptPath := filepath.Join(binDir, "git")
	script := wrapperScript(executable, scriptPath)
	if existing, readErr := os.ReadFile(scriptPath); readErr == nil && string(existing) == script {
		if err := os.Chmod(scriptPath, 0o700); err != nil {
			return "", fmt.Errorf("secure git wrapper: %w", err)
		}
		return binDir, nil
	}
	temp, err := os.CreateTemp(binDir, ".git-wrapper-*")
	if err != nil {
		return "", fmt.Errorf("create git wrapper: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.WriteString(temp, script); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("write git wrapper: %w", err)
	}
	if err := temp.Chmod(0o700); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("secure git wrapper: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("close git wrapper: %w", err)
	}
	if err := os.Rename(tempPath, scriptPath); err != nil {
		return "", fmt.Errorf("install git wrapper: %w", err)
	}
	return binDir, nil
}

func wrapperScript(executable, scriptPath string) string {
	executable = filepath.ToSlash(executable)
	scriptPath = filepath.ToSlash(scriptPath)
	return "#!/bin/sh\n" +
		"real_git=$(PATH=\"${" + originalPathEnv + ":-$PATH}\" command -v git)\n" +
		"if [ -z \"$real_git\" ]; then\n" +
		"  echo 'wuu: git executable not found' >&2\n" +
		"  exit 127\n" +
		"fi\n" +
		"exec " + shellSingleQuote(executable) + " " + internalCommand + " " + shellSingleQuote(scriptPath) + " \"$real_git\" \"$@\"\n"
}

// ShellPrefix activates the wrapper after login-shell profile loading and
// preserves the unmodified PATH so the launcher can resolve the real Git.
func ShellPrefix(binDir string) string {
	pathEntry := shellSingleQuote(binDir)
	if runtime.GOOS == "windows" {
		pathEntry = "\"$(cygpath -u " + shellSingleQuote(binDir) + ")\""
	}
	return "export " + originalPathEnv + "=\"$PATH\"\n" +
		"export PATH=" + pathEntry + ":\"$PATH\"\n"
}

// Dispatch handles the private launcher command and returns Git's exit code.
func Dispatch(args []string) (bool, int) {
	if len(args) == 0 || args[0] != internalCommand {
		return false, 0
	}
	if len(args) < 3 || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[2]) == "" {
		fmt.Fprintln(os.Stderr, "wuu: internal git wrapper requires the wrapper and real git executable paths")
		return true, 127
	}
	wrapperPath := args[1]
	realGit := resolveRealGitExecutable(args[2])
	if err := validateRealGit(wrapperPath, realGit); err != nil {
		fmt.Fprintf(os.Stderr, "wuu: internal git wrapper rejected executable %q: %v\n", realGit, err)
		return true, 127
	}
	gitArgs, _ := AddToCommitArgs(args[3:])
	cmd := exec.Command(realGit, gitArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if code := exitErr.ExitCode(); code >= 0 {
				return true, code
			}
			return true, 1
		}
		fmt.Fprintf(os.Stderr, "wuu: run git: %v\n", err)
		return true, 126
	}
	return true, 0
}

func resolveRealGitExecutable(realGit string) string {
	if runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(realGit), ".exe") {
		return realGit
	}
	if _, err := os.Stat(realGit); err == nil || !errors.Is(err, os.ErrNotExist) {
		return realGit
	}

	// Git Bash omits .exe from command -v output before MSYS converts the path
	// for this native Windows process.
	executablePath := realGit + ".exe"
	if _, err := os.Stat(executablePath); err == nil {
		return executablePath
	}
	return realGit
}

func validateRealGit(wrapperPath, realGit string) error {
	baseName := strings.ToLower(filepath.Base(realGit))
	if baseName != "git" && baseName != "git.exe" {
		return errors.New("executable name is not git")
	}
	wrapperInfo, err := os.Stat(wrapperPath)
	if err != nil {
		return fmt.Errorf("inspect wrapper: %w", err)
	}
	realGitInfo, err := os.Stat(realGit)
	if err != nil {
		return fmt.Errorf("inspect git executable: %w", err)
	}
	if os.SameFile(wrapperInfo, realGitInfo) {
		return errors.New("resolved to the WUU git wrapper itself")
	}
	return nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
