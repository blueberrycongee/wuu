package session

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func TestThreadExecutionLeaseExcludesContendersAndKeepsSidecar(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		sessDir := t.TempDir()
		threadID := "thread/with unsafe path characters"

		first, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil {
			t.Fatalf("first acquire: %v", err)
		}
		if !acquired || first == nil {
			t.Fatal("expected first lease acquisition to succeed")
		}
		t.Cleanup(func() { _ = first.Release() })

		path := threadExecutionLeasePath(sessDir, threadID)
		before, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat lease sidecar: %v", err)
		}
		if before.Mode().Perm() != 0o600 {
			t.Fatalf("lease sidecar mode = %o, want 600", before.Mode().Perm())
		}

		second, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil {
			t.Fatalf("contending acquire: %v", err)
		}
		if acquired || second != nil {
			if second != nil {
				_ = second.Release()
			}
			t.Fatal("expected a live holder to exclude a same-process contender")
		}

		if err := first.Release(); err != nil {
			t.Fatalf("release first lease: %v", err)
		}
		if err := first.Release(); err != nil {
			t.Fatalf("second release of first lease: %v", err)
		}

		third, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil {
			t.Fatalf("acquire after release: %v", err)
		}
		if !acquired || third == nil {
			t.Fatal("expected lease acquisition after release to succeed")
		}
		defer third.Release()
		after, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat reacquired lease sidecar: %v", err)
		}
		if !os.SameFile(before, after) {
			t.Fatal("release must not replace or remove the lease sidecar inode")
		}
	})
}

func TestThreadExecutionResetTargetsOnlyCurrentLease(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		sessDir := t.TempDir()
		threadID := "resettable-thread"

		requested, err := store.RequestThreadExecutionReset(sessDir, threadID)
		if err != nil {
			t.Fatalf("reset idle thread: %v", err)
		}
		if requested {
			t.Fatal("idle thread must not publish a reset")
		}

		first, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil || !acquired || first == nil {
			t.Fatalf("first acquire = lease %v, acquired %v, err %v", first, acquired, err)
		}
		active, err := ThreadExecutionActive(sessDir, threadID)
		if err != nil || !active {
			t.Fatalf("active probe = %v, err %v", active, err)
		}
		requested, err = store.RequestThreadExecutionReset(sessDir, threadID)
		if err != nil {
			t.Fatalf("request live reset: %v", err)
		}
		if !requested {
			t.Fatal("live owner should receive a reset request")
		}
		reset, err := first.ResetRequested()
		if err != nil || !reset {
			t.Fatalf("first lease reset = %v, err %v", reset, err)
		}
		if err := first.Release(); err != nil {
			t.Fatalf("release first lease: %v", err)
		}
		active, err = ThreadExecutionActive(sessDir, threadID)
		if err != nil || active {
			t.Fatalf("idle probe = %v, err %v", active, err)
		}

		second, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil || !acquired || second == nil {
			t.Fatalf("second acquire = lease %v, acquired %v, err %v", second, acquired, err)
		}
		defer second.Release()
		reset, err = second.ResetRequested()
		if err != nil {
			t.Fatalf("read second reset state: %v", err)
		}
		if reset {
			t.Fatal("reset for the prior owner must not cancel a successor")
		}
	})
}

func TestThreadExecutionLeaseReleasedWhenHolderProcessExits(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		sessDir := t.TempDir()
		threadID := "process-owned-thread"
		cmd := exec.Command(os.Args[0], "-test.run=^TestThreadExecutionLeaseProcessHelper$")
		cmd.Env = append(
			os.Environ(),
			"WUU_THREAD_EXECUTION_LEASE_HELPER=1",
			"WUU_THREAD_EXECUTION_LEASE_DIR="+sessDir,
			"WUU_THREAD_EXECUTION_LEASE_ID="+threadID,
			"WUU_THREAD_EXECUTION_LEASE_DOCUMENTS="+store.documents.(*sessionTestDocuments).dir,
			"WUU_THREAD_EXECUTION_LEASE_BACKEND="+store.documents.(*sessionTestDocuments).backend,
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
			t.Fatalf("helper did not acquire lease; stderr: %s", stderr.String())
		}

		contender, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil {
			t.Fatalf("acquire while helper alive: %v", err)
		}
		if acquired || contender != nil {
			if contender != nil {
				_ = contender.Release()
			}
			t.Fatal("expected helper process to retain the lease while alive")
		}

		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("kill helper: %v", err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("expected killed helper to exit unsuccessfully")
		}

		afterCrash, acquired, err := store.TryAcquireThreadExecutionLease(sessDir, threadID)
		if err != nil {
			t.Fatalf("acquire after helper exit: %v", err)
		}
		if !acquired || afterCrash == nil {
			t.Fatal("expected the OS to release the lease when its holder exited")
		}
		if err := afterCrash.Release(); err != nil {
			t.Fatalf("release post-crash lease: %v", err)
		}
	})
}

func TestThreadExecutionLeaseProcessHelper(t *testing.T) {
	if os.Getenv("WUU_THREAD_EXECUTION_LEASE_HELPER") != "1" {
		return
	}
	documents, err := persistence.Open(context.Background(), os.Getenv("WUU_THREAD_EXECUTION_LEASE_DOCUMENTS"), os.Getenv("WUU_THREAD_EXECUTION_LEASE_BACKEND"))
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	store := newDocumentStore(context.Background(), documents)
	lease, acquired, err := store.TryAcquireThreadExecutionLease(
		os.Getenv("WUU_THREAD_EXECUTION_LEASE_DIR"),
		os.Getenv("WUU_THREAD_EXECUTION_LEASE_ID"),
	)
	if err != nil {
		t.Fatalf("helper acquire: %v", err)
	}
	if !acquired || lease == nil {
		t.Fatal("expected helper to acquire lease")
	}
	defer lease.Release()
	fmt.Println("acquired")
	<-time.After(30 * time.Second)
	t.Fatal("helper was not terminated by its parent")
}
