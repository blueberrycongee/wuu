//go:build darwin

package processsandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const seatbeltExecutable = "/usr/bin/sandbox-exec"

func platformSupported() bool { return true }

func applyPlatform(cmd *exec.Cmd, policy Policy) error {
	info, err := os.Stat(seatbeltExecutable)
	if err != nil {
		return fmt.Errorf("%w: inspect %s: %v", ErrUnavailable, seatbeltExecutable, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("%w: %s is not executable", ErrUnavailable, seatbeltExecutable)
	}

	// Keychain reads update the per-user Module Directory Services cache.
	// Resolve the OS cache location rather than TMPDIR, which callers replace
	// with a private command directory. Keep this exception out of other caches
	// and the actual keychain stores.
	cacheOutput, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_CACHE_DIR").Output()
	if err != nil {
		return fmt.Errorf("resolve Keychain cache directory: %w", err)
	}
	cacheDir := strings.TrimSpace(string(cacheOutput))
	if !filepath.IsAbs(cacheDir) || filepath.Clean(cacheDir) == "/" {
		return fmt.Errorf("invalid Keychain cache directory %q", cacheDir)
	}
	cacheDir, err = filepath.EvalSymlinks(cacheDir)
	if err != nil {
		return fmt.Errorf("resolve Keychain cache path: %w", err)
	}
	profile := seatbeltProfile(policy) + "\n" + fmt.Sprintf(
		"(allow file-write* (subpath %s))", seatbeltString(filepath.Join(cacheDir, "mds")))

	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	if originalPath == "" || len(originalArgs) == 0 {
		return fmt.Errorf("filesystem process sandbox requires a complete command argv")
	}
	args := []string{seatbeltExecutable, "-p", profile, "--", originalPath}
	args = append(args, originalArgs[1:]...)
	cmd.Path = seatbeltExecutable
	cmd.Args = args
	return nil
}
