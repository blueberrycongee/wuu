package securefs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Streaming must retain the old file until the callback, fsync and rename all
// succeed. Callback errors must not expose partial sensitive data or leak temps.
func TestWriteFileAtomicFuncPublication(t *testing.T) {
	skipIfNotUnix(t)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "callback_failure"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private", "head.json")
			previous := []byte("previous")
			if err := WriteFileAtomic(path, previous); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("callback failed")
			err := WriteFileAtomicFunc(path, func(w io.Writer) error {
				if _, err := io.WriteString(w, "next"); err != nil {
					return err
				}
				visible, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(visible, previous) {
					t.Fatalf("partial replacement visible during callback: %q, %v", visible, err)
				}
				if fail {
					return failure
				}
				return nil
			})
			want := []byte("next")
			if fail {
				want = previous
				if !errors.Is(err, failure) {
					t.Fatalf("callback error lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("published bytes: %q, %v", got, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != FileMode {
				t.Fatalf("private file mode: %v, %v", info, err)
			}
			parent, err := os.Stat(filepath.Dir(path))
			if err != nil || parent.Mode().Perm() != DirMode {
				t.Fatalf("private directory mode: %v, %v", parent, err)
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".securefs-*.tmp"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("temporary file leaked: %v, %v", leftovers, err)
			}
		})
	}
}

func TestWriteFileAtomicFuncRenameFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing-directory")
	if err := os.Mkdir(path, DirMode); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomicFunc(path, func(w io.Writer) error {
		_, err := io.WriteString(w, "complete-but-unpublished")
		return err
	}); err == nil {
		t.Fatal("rename failure reported success")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("original target replaced: %v, %v", info, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".securefs-*.tmp"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary file leaked: %v, %v", leftovers, err)
	}
}
