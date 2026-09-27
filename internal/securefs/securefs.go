// Package securefs centralizes file-mode conventions for credential-bearing
// and sensitive files: config, auth, debug logs, session databases, and
// their parent directories. Every helper in this package writes at
// mode 0o600 (files) or 0o700 (directories) — the same defaults the
// Codex reference uses (codex-rs/login/src/auth/storage.rs:213 and
// codex-rs/tui/src/session_log.rs:40, `OpenOptionsExt::mode(0o600)`).
//
// The Go stdlib applies the mode argument to OpenFile / MkdirAll, so we
// don't need a build-tag dance or syscall imports — the wrapper just
// picks the right flag set and forwards the call. The single point of
// contact also makes the one-time startup permission migration straightforward.
//
// No backwards compatibility is offered. Wuu is pre-launch, so the
// previous 0o644 / 0o755 defaults are simply wrong; nothing should
// read a 0o644 file expecting world-readability.
package securefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileMode is the permission bits applied to credential-bearing regular
// files: owner read+write only. Matches the Codex reference's 0o600.
const FileMode os.FileMode = 0o600

// DirMode is the permission bits applied to directories that hold
// credential-bearing files: owner read+write+execute only. Matches the
// Codex reference's 0o700.
const DirMode os.FileMode = 0o700

// WriteFileAtomic writes data to path with FileMode (0o600). The write
// goes through a sibling temp file and rename(2) so a crashed write
// never leaves a half-written credential file behind. Existing
// destination files are replaced; existing parents are created at DirMode.
func WriteFileAtomic(path string, data []byte) error {
	if err := Mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".securefs-*.tmp")
	if err != nil {
		return fmt.Errorf("securefs: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securefs: write temp: %w", err)
	}
	if err := tmp.Chmod(FileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securefs: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("securefs: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("securefs: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("securefs: rename: %w", err)
	}
	// Belt and suspenders: if rename preserved a pre-existing mode (it
	// shouldn't on POSIX, but some FUSE / overlay layers have surprised
	// us before) re-assert the target mode after the swap.
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() != FileMode {
		if err := os.Chmod(path, FileMode); err != nil {
			return fmt.Errorf("securefs: chmod final: %w", err)
		}
	}
	return nil
}

// OpenAppend opens path for appending, creating it at FileMode (0o600)
// if it doesn't exist. Mirrors the original pattern in
// internal/providers/debug.go: an append-mode log file created with
// O_CREATE|O_WRONLY|O_APPEND.
func OpenAppend(path string) (*os.File, error) {
	if err := Mkdir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, FileMode)
	if err != nil {
		return nil, fmt.Errorf("securefs: open append: %w", err)
	}
	return f, nil
}

// OpenFile is a thin wrapper over os.OpenFile for callers that need
// both the file handle and an explicit perm (sqlite precreate, custom
// log rotation, etc.). The parent directory is created at DirMode
// first so callers don't need to remember Mkdir-before-open.
//
// The wrapper does NOT force a particular mode on the file — that is
// the caller's responsibility. Pair it with FileMode for any
// credential-bearing file. The daemon also calls syscall.Umask(0o077)
// at startup (see cmd/wuu/main.go), which means even accidentally-
// passed 0o644 perms land as 0o600 after the umask is applied; the
// wrapper just makes the intent explicit. Existing files are not
// chmod'd — that mirrors os.OpenFile semantics and avoids silently
// re-tightening a file the caller deliberately set wider.
func OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	if err := Mkdir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, fmt.Errorf("securefs: open %s: %w", path, err)
	}
	return f, nil
}

// Mkdir creates dir at DirMode (0o700), including parents. Returns nil
// if dir already exists with any mode — callers shouldn't fail on a
// pre-existing directory; TightenRecursive (called once at startup)
// will normalize the mode if needed.
func Mkdir(dir string) error {
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("securefs: mkdir %s: %w", dir, err)
	}
	return nil
}

// PreCreateFile creates path at FileMode (0o600) if it doesn't already
// exist, and re-asserts FileMode if a pre-existing file's mode drifted.
// This is the bridge for SQLite (and other database drivers) whose
// underlying OpenFile call ignores mode arguments or applies its own
// umask-bounded default: the file is materialized with our mode before
// the driver ever touches it.
func PreCreateFile(path string) error {
	if err := Mkdir(filepath.Dir(path)); err != nil {
		return err
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, FileMode)
		if err != nil {
			return fmt.Errorf("securefs: precreate: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("securefs: precreate close: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("securefs: precreate stat: %w", err)
	case info.Mode().Perm() != FileMode:
		return os.Chmod(path, FileMode)
	}
	return nil
}

// TightenRecursive walks root and tightens any file or directory whose
// mode is wider than FileMode / DirMode. Wider here means: the
// "group" or "other" read / write / execute bits are set. Returns the
// first error encountered; symbolic links are skipped. On POSIX the operation
// is best-effort and the caller is expected to log + continue.
//
// This exists because pre-launch installs left 0o644 / 0o755 files in
// place. Future installs land at the right mode via the helpers above;
// existing files get normalized by a one-time startup migration.
func TightenRecursive(root string) (retErr error) {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("securefs: stat root %s: %w", root, err)
	}
	if !info.IsDir() {
		return tightenOne(root, info)
	}
	// Walk breadth-first so a single failed entry doesn't block the rest.
	type pending struct {
		path  string
		isDir bool
	}
	queue := []pending{{root, true}}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		entry, err := os.Lstat(next.path)
		if err != nil {
			if retErr == nil {
				retErr = fmt.Errorf("securefs: walk stat %s: %w", next.path, err)
			}
			continue
		}
		if err := tightenOne(next.path, entry); err != nil && retErr == nil {
			retErr = err
		}
		if entry.IsDir() {
			entries, err := os.ReadDir(next.path)
			if err != nil {
				if retErr == nil {
					retErr = fmt.Errorf("securefs: readdir %s: %w", next.path, err)
				}
				continue
			}
			for _, child := range entries {
				queue = append(queue, pending{
					path:  filepath.Join(next.path, child.Name()),
					isDir: child.IsDir(),
				})
			}
		}
	}
	return retErr
}

const permissionMigrationMarker = ".permissions-v1"

// TightenHomeOnce runs the legacy permission migration at most once for a Wuu
// home. A successful walk writes an owner-only marker; later processes perform
// one stat instead of walking every session artifact on every CLI invocation.
//
// New sensitive paths remain safe after migration because securefs creation
// helpers and the process umask enforce owner-only modes at write time. If the
// walk or marker write fails, no completion marker is left and the next launch
// retries the migration.
func TightenHomeOnce(root string) error {
	marker := filepath.Join(root, permissionMigrationMarker)
	if info, err := os.Lstat(marker); err == nil && info.Mode()&os.ModeSymlink == 0 {
		if info.IsDir() {
			return fmt.Errorf("securefs: permission migration marker %s is a directory", marker)
		}
		if info.Mode().Perm() != FileMode {
			if err := os.Chmod(marker, FileMode); err != nil {
				return fmt.Errorf("securefs: chmod permission migration marker: %w", err)
			}
		}
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("securefs: stat permission migration marker: %w", err)
	}

	// The configured home itself may be a symlink; only links inside it are
	// excluded from migration.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("securefs: resolve home: %w", err)
	}
	root = resolved
	if err := TightenRecursive(root); err != nil {
		return err
	}
	// Replace a symlink marker itself; never chmod or write its target.
	if err := WriteFileAtomic(marker, []byte("completed\n")); err != nil {
		return fmt.Errorf("securefs: write permission migration marker: %w", err)
	}
	return nil
}

func tightenOne(path string, info os.FileInfo) error {
	current := info.Mode().Perm()
	var target os.FileMode
	if info.IsDir() {
		target = DirMode
	} else {
		target = FileMode
	}
	// Symlinks report mode 0o777 on Linux; leave them alone.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if current == target {
		return nil
	}
	if current&^target != 0 {
		// current has bits that target doesn't allow — group / other bits
		// set when target is owner-only. Strip them.
		return os.Chmod(path, target)
	}
	// current is more restrictive than target — nothing to do.
	return nil
}

// ModeString is a tiny helper for log lines that report what was
// tightened; keeps call sites free of strconv imports.
func ModeString(m os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(m.Perm()), 8)
}

// SafeToPrint returns true if the path lives under a known sensitive
// directory (anything that should never be logged in plaintext). It's
// a soft guard — callers should always prefer context-specific logic
// over this helper.
func SafeToPrint(path string) bool {
	lower := strings.ToLower(path)
	return !strings.Contains(lower, "auth.json") &&
		!strings.Contains(lower, "config.json") &&
		!strings.Contains(lower, ".key") &&
		!strings.Contains(lower, "session")
}

// Suppress unused import warnings for io (kept for future readers
// that want to extend WriteFileAtomic with io.Copy semantics).
var _ = io.EOF
