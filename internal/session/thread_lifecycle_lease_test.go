package session

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestThreadLifecycleLeaseSerializesWritersAndKeepsSidecar(t *testing.T) {
	sessDir := t.TempDir()
	threadID := "thread/with unsafe path characters"

	first, err := AcquireThreadLifecycleLease(sessDir, threadID)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	t.Cleanup(func() { _ = first.Release() })
	path := threadLifecycleLeasePath(sessDir, threadID)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lifecycle lease sidecar: %v", err)
	}
	if before.Mode().Perm() != 0o600 {
		t.Fatalf("lifecycle lease sidecar mode = %o, want 600", before.Mode().Perm())
	}

	acquired := make(chan *ThreadLifecycleLease, 1)
	errCh := make(chan error, 1)
	go func() {
		second, acquireErr := AcquireThreadLifecycleLease(sessDir, threadID)
		if acquireErr != nil {
			errCh <- acquireErr
			return
		}
		acquired <- second
	}()
	select {
	case second := <-acquired:
		_ = second.Release()
		t.Fatal("contender acquired while the first lifecycle lease was held")
	case err := <-errCh:
		t.Fatalf("contending acquire: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release first lease: %v", err)
	}
	var second *ThreadLifecycleLease
	select {
	case second = <-acquired:
	case err := <-errCh:
		t.Fatalf("acquire after release: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("contender did not acquire after release")
	}
	defer second.Release()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat reacquired lifecycle lease sidecar: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("release must not replace or remove the lifecycle lease sidecar inode")
	}
}

func TestThreadLifecycleLeaseReleasedWhenHolderProcessExits(t *testing.T) {
	sessDir := t.TempDir()
	threadID := "process-owned-thread"
	cmd := exec.Command(os.Args[0], "-test.run=^TestThreadLifecycleLeaseProcessHelper$")
	cmd.Env = append(
		os.Environ(),
		"WUU_THREAD_LIFECYCLE_LEASE_HELPER=1",
		"WUU_THREAD_LIFECYCLE_LEASE_DIR="+sessDir,
		"WUU_THREAD_LIFECYCLE_LEASE_ID="+threadID,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	ready := make(chan error, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		if readErr != nil {
			ready <- fmt.Errorf("read readiness: %w", readErr)
			return
		}
		if strings.TrimSpace(line) != "acquired" {
			ready <- fmt.Errorf("unexpected readiness output %q", line)
			return
		}
		ready <- nil
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("helper readiness: %v; stderr: %s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("helper did not acquire lifecycle lease; stderr: %s", stderr.String())
	}

	blocked := make(chan *ThreadLifecycleLease, 1)
	blockedErr := make(chan error, 1)
	go func() {
		lease, acquireErr := AcquireThreadLifecycleLease(sessDir, threadID)
		if acquireErr != nil {
			blockedErr <- acquireErr
			return
		}
		blocked <- lease
	}()
	select {
	case lease := <-blocked:
		_ = lease.Release()
		t.Fatal("expected helper process to retain the lifecycle lease while alive")
	case err := <-blockedErr:
		t.Fatalf("contender acquire: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected killed helper to exit unsuccessfully")
	}
	select {
	case lease := <-blocked:
		if err := lease.Release(); err != nil {
			t.Fatalf("release post-crash lifecycle lease: %v", err)
		}
	case err := <-blockedErr:
		t.Fatalf("acquire after helper exit: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("OS did not release the lifecycle lease after holder exit")
	}
}

func TestThreadLifecycleLeaseProcessHelper(t *testing.T) {
	if os.Getenv("WUU_THREAD_LIFECYCLE_LEASE_HELPER") != "1" {
		return
	}
	lease, err := AcquireThreadLifecycleLease(
		os.Getenv("WUU_THREAD_LIFECYCLE_LEASE_DIR"),
		os.Getenv("WUU_THREAD_LIFECYCLE_LEASE_ID"),
	)
	if err != nil {
		t.Fatalf("helper acquire: %v", err)
	}
	defer lease.Release()
	fmt.Println("acquired")
	for {
		time.Sleep(time.Hour)
	}
}
