package host

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHostProcessLockHelper(t *testing.T) {
	path := os.Getenv("WUU_TEST_HOST_LOCK")
	if path == "" {
		return
	}
	unlock, err := lockHostStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestHostStoreExcludesAnotherProcessAndRecoversAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote.json")
	child := exec.Command(os.Args[0], "-test.run=^TestHostProcessLockHelper$")
	child.Env = append(os.Environ(), "WUU_TEST_HOST_LOCK="+path)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("lock owner did not start: %q, %v", line, err)
	}
	// Rejection happens before relay dialing or execution service startup.
	h := &Host{store: &Store{path: path}}
	if err := h.Run(context.Background()); !errors.Is(err, ErrHostAlreadyRunning) {
		t.Fatalf("second host was not rejected: %v", err)
	}
	other, err := lockHostStore(filepath.Join(t.TempDir(), "remote.json"))
	if err != nil {
		t.Fatalf("independent homes must coexist: %v", err)
	}
	other()
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	unlock, err := lockHostStore(path)
	if err != nil {
		t.Fatalf("crashed host retained ownership: %v", err)
	}
	unlock()
}
