package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skipIfNotUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("mode bits not honored on windows")
	}
}

func TestWriteFileAtomic_0o600(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "secret.json")
	if err := WriteFileAtomic(path, []byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode = %s, want %s", ModeString(info.Mode()), ModeString(FileMode))
	}
}

func TestWriteFileAtomic_OverwriteReassertsMode(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	if err := WriteFileAtomic(path, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Widen the mode manually, then re-write — the atomic write should
	// re-tighten even if rename preserved the wider mode (which POSIX
	// shouldn't do, but belt-and-suspenders).
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widen: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode after overwrite = %s, want %s", ModeString(info.Mode()), ModeString(FileMode))
	}
}

func TestWriteFileAtomic_TempNotLeftBehind(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	if err := WriteFileAtomic(path, []byte("payload")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestMkdir_0o700(t *testing.T) {
	skipIfNotUnix(t)
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if err := Mkdir(dir); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != DirMode {
		t.Fatalf("dir mode = %s, want %s", ModeString(info.Mode()), ModeString(DirMode))
	}
}

func TestOpenFile_0o600Honored(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "explicit-mode")
	f, err := OpenFile(path, os.O_CREATE|os.O_RDWR, FileMode)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode = %s, want %s", ModeString(info.Mode()), ModeString(FileMode))
	}
}

func TestOpenFile_ParentDirCreated(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "out")
	f, err := OpenFile(deep, os.O_CREATE|os.O_WRONLY, FileMode)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()
	parentInfo, err := os.Stat(filepath.Dir(deep))
	if err != nil {
		t.Fatalf("stat parent: %v", err)
	}
	if got := parentInfo.Mode().Perm(); got != DirMode {
		t.Fatalf("parent dir mode = %s, want %s", ModeString(parentInfo.Mode()), ModeString(DirMode))
	}
}

func TestOpenFile_ExistingFileNotChmoded(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "preexisting")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}
	f, err := OpenFile(path, os.O_RDWR, FileMode)
	if err != nil {
		t.Fatalf("OpenFile existing: %v", err)
	}
	defer f.Close()
	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("existing file mode = %s, want 0o644 (unchanged)", ModeString(info.Mode()))
	}
}

func TestMkdir_Idempotent(t *testing.T) {
	skipIfNotUnix(t)
	dir := filepath.Join(t.TempDir(), "x")
	if err := Mkdir(dir); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := Mkdir(dir); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func TestOpenAppend_0o600(t *testing.T) {
	skipIfNotUnix(t)
	path := filepath.Join(t.TempDir(), "debug.log")
	f, err := OpenAppend(path)
	if err != nil {
		t.Fatalf("OpenAppend: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode = %s, want %s", ModeString(info.Mode()), ModeString(FileMode))
	}
}

func TestPreCreateFile_NewFile(t *testing.T) {
	skipIfNotUnix(t)
	path := filepath.Join(t.TempDir(), "sessions.db")
	if err := PreCreateFile(path); err != nil {
		t.Fatalf("PreCreateFile: %v", err)
	}
	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode = %s, want %s", ModeString(info.Mode()), ModeString(FileMode))
	}
	if info.Size() != 0 {
		t.Fatalf("precreated file should be empty, got size %d", info.Size())
	}
}

func TestPreCreateFile_ReassertsMode(t *testing.T) {
	skipIfNotUnix(t)
	path := filepath.Join(t.TempDir(), "sessions.db")
	if err := PreCreateFile(path); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widen: %v", err)
	}
	if err := PreCreateFile(path); err != nil {
		t.Fatalf("second: %v", err)
	}
	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode after re-tighten = %s, want %s", ModeString(info.Mode()), ModeString(FileMode))
	}
}

func TestTightenRecursive_TightensFiles(t *testing.T) {
	skipIfNotUnix(t)
	root := t.TempDir()
	dir := filepath.Join(root, "sub")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("writefile: %v", err)
	}
	if err := TightenRecursive(root); err != nil {
		t.Fatalf("TightenRecursive: %v", err)
	}
	dirInfo, _ := os.Stat(dir)
	if got := dirInfo.Mode().Perm(); got != DirMode {
		t.Fatalf("dir mode = %s, want %s", ModeString(dirInfo.Mode()), ModeString(DirMode))
	}
	fileInfo, _ := os.Stat(file)
	if got := fileInfo.Mode().Perm(); got != FileMode {
		t.Fatalf("file mode = %s, want %s", ModeString(fileInfo.Mode()), ModeString(FileMode))
	}
}

func TestTightenRecursive_LeavesMoreRestrictiveAlone(t *testing.T) {
	skipIfNotUnix(t)
	root := t.TempDir()
	dir := filepath.Join(root, "sub")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Tighten down to 0o500 (no group/other bits, no user write). This
	// is more restrictive than the canonical 0o700 target, so
	// TightenRecursive should leave it alone.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := TightenRecursive(root); err != nil {
		t.Fatalf("TightenRecursive: %v", err)
	}
	info, _ := os.Stat(dir)
	if got := info.Mode().Perm(); got != 0o500 {
		t.Fatalf("dir mode = %s, want %s (unchanged)", ModeString(info.Mode()), "0o500")
	}
}

func TestTightenRecursive_HandlesMissingRoot(t *testing.T) {
	skipIfNotUnix(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := TightenRecursive(missing); err == nil {
		t.Fatalf("expected error on missing root, got nil")
	}
}

func TestTightenHomeOnce_MarksSuccessfulMigrationAndSkipsLaterWalks(t *testing.T) {
	skipIfNotUnix(t)
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy.json")
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := os.Chmod(legacy, 0o644); err != nil {
		t.Fatalf("widen legacy: %v", err)
	}

	if err := TightenHomeOnce(root); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	legacyInfo, err := os.Stat(legacy)
	if err != nil {
		t.Fatalf("stat legacy: %v", err)
	}
	if got := legacyInfo.Mode().Perm(); got != FileMode {
		t.Fatalf("legacy mode = %s, want %s", ModeString(got), ModeString(FileMode))
	}
	marker := filepath.Join(root, permissionMigrationMarker)
	markerInfo, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	if got := markerInfo.Mode().Perm(); got != FileMode {
		t.Fatalf("marker mode = %s, want %s", ModeString(got), ModeString(FileMode))
	}

	// A path created outside securefs after the migration is deliberately not
	// rescanned. Production writes use securefs helpers plus umask 0o077; this
	// assertion proves later startup cost does not grow with session history.
	later := filepath.Join(root, "later.json")
	if err := os.WriteFile(later, []byte("later"), 0o600); err != nil {
		t.Fatalf("write later: %v", err)
	}
	if err := os.Chmod(later, 0o644); err != nil {
		t.Fatalf("widen later: %v", err)
	}
	if err := TightenHomeOnce(root); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	laterInfo, err := os.Stat(later)
	if err != nil {
		t.Fatalf("stat later: %v", err)
	}
	if got := laterInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("second call rescanned home: later mode = %s, want 0644", ModeString(got))
	}
}

func TestTightenHomeOnce_DoesNotMarkFailedMigration(t *testing.T) {
	skipIfNotUnix(t)
	root := filepath.Join(t.TempDir(), "missing")
	if err := TightenHomeOnce(root); err == nil {
		t.Fatal("expected missing-home migration error")
	}
	if _, err := os.Stat(filepath.Join(root, permissionMigrationMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed migration left marker: %v", err)
	}
}

func TestModeString(t *testing.T) {
	cases := []struct {
		m    os.FileMode
		want string
	}{
		{FileMode, "0600"},
		{DirMode, "0700"},
		{0o644, "0644"},
		{0o755, "0755"},
	}
	for _, c := range cases {
		if got := ModeString(c.m); got != c.want {
			t.Errorf("ModeString(%s) = %q, want %q", ModeString(c.m), got, c.want)
		}
	}
}

func TestSafeToPrint(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/home/u/.wuu/notes.txt", true},
		{"/home/u/.wuu/auth.json", false},
		{"/home/u/.wuu/config.json", false},
		{"/home/u/.wuu/sessions/foo.db", false},
		{"/home/u/.wuu/SessionBackup/db", false},
		{"/home/u/.wuu/private.key", false},
	}
	for _, c := range cases {
		if got := SafeToPrint(c.path); got != c.want {
			t.Errorf("SafeToPrint(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestRefusesReadOnlyDir — POSIX guarantee that 0o600 write to a file
// in a 0o400 directory fails. Locks down the security claim that
// tightening can't be bypassed by a hostile parent directory owner.
func TestRefusesReadOnlyDir(t *testing.T) {
	skipIfNotUnix(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "ro")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Make the directory read-only; subsequent writes should fail.
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(sub, 0o700)
	err := WriteFileAtomic(filepath.Join(sub, "x.json"), []byte("y"))
	if err == nil {
		t.Fatalf("expected error writing to read-only dir, got nil")
	}
}

func TestTightenHomeOnce_SkipsSymlinks(t *testing.T) {
	skipIfNotUnix(t)
	for _, kind := range []string{"directory", "file", "dangling", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			target := filepath.Join(external, "run.sh")
			content := "#!/bin/sh\nexit 0\n"
			if err := os.WriteFile(target, []byte(content), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(external, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "linked")
			destination := target
			switch kind {
			case "directory":
				destination = external
			case "dangling":
				destination = filepath.Join(external, "missing")
			case "cycle":
				destination = link
			}
			if err := os.Symlink(destination, link); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(root, "legacy.json")
			if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(legacy, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := TightenHomeOnce(root); err != nil {
				t.Errorf("migration: %v", err)
			}
			for path, want := range map[string]os.FileMode{target: 0o755, external: 0o755, legacy: FileMode, filepath.Join(root, permissionMigrationMarker): FileMode} {
				info, err := os.Stat(path)
				if err != nil {
					t.Error(err)
					continue
				}
				if info.Mode().Perm() != want {
					t.Errorf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want)
				}
			}
			if got, err := os.ReadFile(target); err != nil || string(got) != content {
				t.Errorf("external content = %q, err = %v", got, err)
			}
			if got, err := os.Readlink(link); err != nil || got != destination {
				t.Errorf("link = %q, err = %v", got, err)
			}
		})
	}
}

func TestTightenHomeOnce_ReplacesSymlinkMarker(t *testing.T) {
	skipIfNotUnix(t)
	for _, kind := range []string{"file", "directory", "dangling", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			marker := filepath.Join(root, permissionMigrationMarker)
			target := filepath.Join(external, "target")
			if kind == "directory" {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if kind == "file" {
				if err := os.WriteFile(target, []byte("untouched"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "file" || kind == "directory" {
				if err := os.Chmod(target, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "cycle" {
				target = marker
			}
			if err := os.Symlink(target, marker); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(root, "legacy")
			if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(legacy, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := TightenHomeOnce(root); err != nil {
				t.Errorf("migration: %v", err)
			}
			for _, path := range []string{marker, legacy} {
				info, err := os.Lstat(path)
				if err != nil {
					t.Error(err)
					continue
				}
				if !info.Mode().IsRegular() || info.Mode().Perm() != FileMode {
					t.Errorf("%s mode = %v, want regular 0600", path, info.Mode())
				}
			}
			if kind == "file" || kind == "directory" {
				info, err := os.Stat(target)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o755 {
					t.Errorf("target mode = %04o, want 0755", info.Mode().Perm())
				}
			}
			if kind == "file" {
				if got, err := os.ReadFile(target); err != nil || string(got) != "untouched" {
					t.Errorf("target content = %q, err = %v", got, err)
				}
			}
			if kind == "dangling" {
				if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("dangling target created: %v", err)
				}
			}
		})
	}
}
