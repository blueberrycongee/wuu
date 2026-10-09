package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileMutationQueueSerializesSamePath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "real"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// An add-file patch and a legacy writer must share one queue before the file exists.
	path := filepath.Join(root, "real", "nested", "a.txt")
	alias := filepath.Join(root, "alias", "nested", "a.txt")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	secondEntered := make(chan struct{})

	go func() {
		defer close(firstDone)
		_, _ = withFileMutationQueue(path, func() (struct{}, error) {
			close(firstEntered)
			<-releaseFirst
			return struct{}{}, nil
		})
	}()
	<-firstEntered
	go func() {
		_, _ = withFileMutationQueue(alias, func() (struct{}, error) {
			close(secondEntered)
			return struct{}{}, nil
		})
	}()

	select {
	case <-secondEntered:
		close(releaseFirst)
		<-firstDone
		t.Fatal("second mutation entered before the first released the file")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	<-firstDone
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second mutation did not enter after the first released the file")
	}
}
