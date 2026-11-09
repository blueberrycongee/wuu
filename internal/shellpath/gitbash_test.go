package shellpath

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func chainEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func existsSet(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(path string) bool { return set[path] }
}

func noLookPath(string) (string, error) { return "", errors.New("not found") }

func TestFindGitBashEnvOverrideWins(t *testing.T) {
	override := filepath.Join("D:", "tools", "bash.exe")
	got, err := findGitBash(
		chainEnv(map[string]string{"WUU_GIT_BASH_PATH": override}),
		existsSet(override),
		noLookPath,
	)
	if err != nil {
		t.Fatalf("findGitBash: %v", err)
	}
	if got != override {
		t.Fatalf("got %q, want %q", got, override)
	}
}

func TestFindGitBashEnvOverrideMissingFails(t *testing.T) {
	_, err := findGitBash(
		chainEnv(map[string]string{"WUU_GIT_BASH_PATH": "X:\\missing\\bash.exe"}),
		existsSet(),
		noLookPath,
	)
	if err == nil || !strings.Contains(err.Error(), "WUU_GIT_BASH_PATH") {
		t.Fatalf("want override error, got %v", err)
	}
}

func TestFindGitBashStandardInstallRoots(t *testing.T) {
	programFiles := filepath.Join("C:", "Program Files")
	installed := filepath.Join(programFiles, "Git", "bin", "bash.exe")
	got, err := findGitBash(
		chainEnv(map[string]string{"ProgramFiles": programFiles}),
		existsSet(installed),
		noLookPath,
	)
	if err != nil {
		t.Fatalf("findGitBash: %v", err)
	}
	if got != installed {
		t.Fatalf("got %q, want %q", got, installed)
	}
}

func TestFindGitBashDerivesFromGitExe(t *testing.T) {
	gitExe := filepath.Join(string(filepath.Separator)+"custom", "Git", "cmd", "git.exe")
	derived := filepath.Join(string(filepath.Separator)+"custom", "Git", "bin", "bash.exe")
	got, err := findGitBash(
		chainEnv(nil),
		existsSet(derived),
		func(name string) (string, error) {
			if name == "git" {
				return gitExe, nil
			}
			return "", errors.New("not found")
		},
	)
	if err != nil {
		t.Fatalf("findGitBash: %v", err)
	}
	if got != derived {
		t.Fatalf("got %q, want %q", got, derived)
	}
}

func TestFindGitBashRejectsRelativeLookPath(t *testing.T) {
	_, err := findGitBash(
		chainEnv(nil),
		func(string) bool { return true },
		func(name string) (string, error) {
			// A current-directory match must never be trusted.
			return name + ".exe", nil
		},
	)
	if err == nil {
		t.Fatal("want resolution failure for relative LookPath results")
	}
}

func TestFindGitBashSkipsSystemRootBash(t *testing.T) {
	systemRoot := string(filepath.Separator) + filepath.Join("Windows")
	wslBash := filepath.Join(systemRoot, "System32", "bash.exe")
	_, err := findGitBash(
		chainEnv(map[string]string{"SystemRoot": systemRoot}),
		existsSet(),
		func(name string) (string, error) {
			if name == "bash" {
				return wslBash, nil
			}
			return "", errors.New("not found")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "Git Bash not found") {
		t.Fatalf("want install hint after skipping WSL shim, got %v", err)
	}
}

func TestFindGitBashAcceptsPathBashOutsideSystemRoot(t *testing.T) {
	msysBash := string(filepath.Separator) + filepath.Join("msys64", "usr", "bin", "bash.exe")
	got, err := findGitBash(
		chainEnv(map[string]string{"SystemRoot": string(filepath.Separator) + "Windows"}),
		existsSet(),
		func(name string) (string, error) {
			if name == "bash" {
				return msysBash, nil
			}
			return "", errors.New("not found")
		},
	)
	if err != nil {
		t.Fatalf("findGitBash: %v", err)
	}
	if got != msysBash {
		t.Fatalf("got %q, want %q", got, msysBash)
	}
}

func dirSet(dirs ...string) func(string) bool {
	set := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		set[d] = true
	}
	return func(path string) bool { return set[path] }
}

func TestGitBashCommandEnvPrependsToolDirsOnce(t *testing.T) {
	root := string(filepath.Separator) + "git-install"
	bash := filepath.Join(root, "bin", "bash.exe")
	usrBin := filepath.Join(root, "usr", "bin")
	mingwBin := filepath.Join(root, "mingw64", "bin")
	env := gitBashCommandEnv([]string{"Path=C:\\Windows", "HOME=C:\\Users\\dev"}, bash, dirSet(usrBin, mingwBin))

	var pathEntries []string
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PATH") {
			pathEntries = append(pathEntries, value)
		}
	}
	if len(pathEntries) != 1 {
		t.Fatalf("want exactly one PATH entry, got %d in %v", len(pathEntries), env)
	}
	if !strings.HasPrefix(pathEntries[0], usrBin) {
		t.Fatalf("PATH %q does not lead with %q", pathEntries[0], usrBin)
	}
	if !strings.HasSuffix(pathEntries[0], "C:\\Windows") {
		t.Fatalf("PATH %q lost original value", pathEntries[0])
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "MSYS_NO_PATHCONV") || strings.EqualFold(key, "MSYS2_ARG_CONV_EXCL") {
			t.Fatalf("MSYS conversion settings must stay at their defaults: %v", env)
		}
	}
}

func TestGitBashCommandEnvHandlesUsrBinLayout(t *testing.T) {
	// MinGit and MSYS2 place bash at <root>\usr\bin\bash.exe; the tool
	// dirs hang off <root>, not <root>\usr.
	root := string(filepath.Separator) + "min-git"
	bash := filepath.Join(root, "usr", "bin", "bash.exe")
	usrBin := filepath.Join(root, "usr", "bin")
	env := gitBashCommandEnv([]string{"PATH=/base"}, bash, dirSet(usrBin))

	wantPrefix := "PATH=" + usrBin + string(filepath.ListSeparator)
	if env[0] != wantPrefix+"/base" {
		t.Fatalf("PATH = %q, want prefix %q", env[0], wantPrefix)
	}
}

func TestGitBashCommandEnvSkipsMissingToolDirs(t *testing.T) {
	root := string(filepath.Separator) + "git-install"
	bash := filepath.Join(root, "bin", "bash.exe")
	usrBin := filepath.Join(root, "usr", "bin")
	env := gitBashCommandEnv([]string{"PATH=/base"}, bash, dirSet(usrBin))
	if env[0] != "PATH="+usrBin+string(filepath.ListSeparator)+"/base" {
		t.Fatalf("PATH = %q, want only the existing tool dir prepended", env[0])
	}

	// No tool dirs at all: env passes through untouched.
	env = gitBashCommandEnv([]string{"PATH=/base"}, bash, dirSet())
	if env[0] != "PATH=/base" {
		t.Fatalf("PATH = %q, want unchanged", env[0])
	}
}
