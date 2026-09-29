//go:build linux

package processsandbox

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	attr := struct{ Access uint64 }{access}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return errno
	}
	defer unix.Close(int(fd))
	add := func(name string, allowed uint64) error {
		pathFD, err := unix.Open(name, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer unix.Close(pathFD)
		// The kernel ABI packs an aligned u64 followed by a signed 32-bit fd.
		rule := struct {
			Access  uint64
			Parent  int32
			Padding uint32
		}{Access: allowed, Parent: int32(pathFD)}
		_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return errno
		}
		return nil
	}
	if policy.Mode == ModeWorkspaceWrite {
		for _, root := range policy.WritableRoots {
			if err := add(root, access); err != nil {
				return err
			}
		}
	}
	if err = add("/dev/null", unix.LANDLOCK_ACCESS_FS_WRITE_FILE|unix.LANDLOCK_ACCESS_FS_TRUNCATE); err != nil {
		return err
	}
	if err = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	if _, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return errno
	}
	return unix.Exec(args[1], args[1:], os.Environ())
}
