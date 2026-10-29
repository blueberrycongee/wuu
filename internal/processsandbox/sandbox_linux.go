//go:build linux

package processsandbox

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const helperArgument = "--wuu-filesystem-sandbox"

// The helper re-execs before application startup. Restriction and exec happen
// on one locked OS thread; a Go scheduler migration would lose the policy.
func init() {
	if len(os.Args) < 2 || os.Args[1] != helperArgument {
		return
	}
	runtime.LockOSThread()
	if err := runLinuxHelper(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "wuu-filesystem-sandbox:", err)
		os.Exit(126)
	}
	os.Exit(126)
}

func landlockABI() (uintptr, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0, errno
	}
	if abi < 3 {
		return 0, fmt.Errorf("Landlock ABI 3 or later is required")
	}
	return abi, nil
}

func platformSupported() bool { _, err := landlockABI(); return err == nil }

func applyPlatform(cmd *exec.Cmd, policy Policy) error {
	if _, err := landlockABI(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if cmd.Path == "" || len(cmd.Args) == 0 {
		return fmt.Errorf("filesystem process sandbox requires a complete command argv")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	args := []string{executable, helperArgument, base64.RawURLEncoding.EncodeToString(data), cmd.Path}
	args = append(args, cmd.Args[1:]...)
	cmd.Path, cmd.Args = executable, args
	return nil
}

func runLinuxHelper(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("missing policy or command")
	}
	data, err := base64.RawURLEncoding.DecodeString(args[0])
	if err != nil {
		return err
	}
	var policy Policy
	if err = json.Unmarshal(data, &policy); err != nil {
		return err
	}
	if err = validatePolicy(policy); err != nil {
		return err
	}
	if _, err = landlockABI(); err != nil {
		return err
	}
	access := uint64(unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM | unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE)
	readAccess := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_EXECUTE)
	handledAccess := access
	if len(policy.ProtectedPaths) > 0 {
		handledAccess |= readAccess
	}
	attr := struct{ Access uint64 }{handledAccess}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("create Landlock ruleset: %w", errno)
	}
	defer unix.Close(int(fd))
	add := func(name string, allowed uint64) error {
		var pathFD int
		var err error
		if len(policy.ProtectedPaths) > 0 {
			pathFD, err = unix.Openat2(unix.AT_FDCWD, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS})
		} else {
			pathFD, err = unix.Open(name, unix.O_PATH|unix.O_CLOEXEC, 0)
		}
		if err != nil {
			return &os.PathError{Op: "open sandbox grant", Path: name, Err: err}
		}
		defer unix.Close(pathFD)
		var info unix.Stat_t
		if err := unix.Fstat(pathFD, &info); err != nil {
			return fmt.Errorf("inspect sandbox grant %q: %w", name, err)
		}
		if info.Mode&unix.S_IFMT != unix.S_IFDIR {
			allowed &= unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE
		}
		if allowed == 0 {
			return nil
		}
		// The kernel ABI packs an aligned u64 followed by a signed 32-bit fd.
		rule := struct {
			Access  uint64
			Parent  int32
			Padding uint32
		}{Access: allowed, Parent: int32(pathFD)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("add Landlock grant %q: %w", name, errno)
		}
		return nil
	}

	if len(policy.ProtectedPaths) > 0 {
		if err := linuxGrantPaths("/", policy.ProtectedPaths, readAccess, add); err != nil {
			return err
		}
	}
	if policy.Mode == ModeWorkspaceWrite {
		for _, root := range policy.WritableRoots {
			if err := linuxGrantPaths(root, policy.ProtectedPaths, access, add); err != nil {
				return err
			}
		}
	}
	if err = linuxGrantPaths("/dev/null", policy.ProtectedPaths, unix.LANDLOCK_ACCESS_FS_WRITE_FILE|unix.LANDLOCK_ACCESS_FS_TRUNCATE, add); err != nil {
		return err
	}
	if err = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs for sandbox: %w", err)
	}
	if _, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("install Landlock ruleset: %w", errno)
	}
	if err := unix.Exec(args[1], args[1:], os.Environ()); err != nil {
		return fmt.Errorf("execute confined command %q: %w", args[1], err)
	}
	return nil
}

func linuxGrantPaths(root string, protected []string, rights uint64, add func(string, uint64) error) error {
	root = filepath.Clean(root)
	roots := []string{root}
	if len(protected) > 0 {
		var err error
		roots, err = linuxAllowedPaths(root, protected)
		if err != nil {
			return err
		}
	}
	for _, candidate := range roots {
		if err := add(candidate, rights); err != nil {
			var pathErr *os.PathError
			if candidate != root && errors.As(err, &pathErr) && pathErr.Op == "open sandbox grant" && errors.Is(err, unix.ENOENT) {
				// An enumerated sibling (for example another process's /tmp
				// directory) can disappear before it is opened. Omitting its
				// grant stays fail-closed even if the path is recreated later.
				// Explicit roots and all other grant failures remain errors.
				continue
			}
			return err
		}
	}
	return nil
}

// linuxAllowedPaths subtracts protected subtrees from a grant. Landlock rights
// are additive, so ancestors of exclusions must never receive a broad grant.
// Their existing disjoint children can receive grants; missing children remain
// denied when created later. Symlink aliases are omitted: their real targets
// receive their own grants if accessible. Ancestor directory listing and writes
// are intentionally narrower than the ordinary policy.
func linuxAllowedPaths(root string, protected []string) ([]string, error) {
	root = filepath.Clean(root)
	overlaps := false
	for _, denied := range protected {
		if pathWithin(denied, root) {
			return nil, nil
		}
		if pathWithin(root, denied) {
			overlaps = true
		}
	}
	if !overlaps {
		return []string{root}, nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enumerate protected-path sandbox grants under %q: %w", root, err)
	}
	var allowed []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		paths, err := linuxAllowedPaths(filepath.Join(root, entry.Name()), protected)
		if err != nil {
			return nil, err
		}
		allowed = append(allowed, paths...)
	}
	return allowed, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
