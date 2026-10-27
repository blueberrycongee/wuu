package storelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// Every release must wake local contenders without a polling tick, but only
// one contender may own the OS lock. Exercise both ways to acquire the owner.
func TestLocalContendersHandoffWithoutPolling(t *testing.T) {
	for _, name := range []string{"Acquire", "TryAcquire", "relative_owner"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ownerDir := dir
			if name == "relative_owner" {
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				ownerDir, err = filepath.Rel(cwd, dir)
				if err != nil {
					t.Fatal(err)
				}
			}
			synctest.Test(t, func(t *testing.T) {
				var owner *Lock
				var err error
				if name == "TryAcquire" {
					var acquired bool
					owner, acquired, err = TryAcquire(ownerDir)
					if err == nil && !acquired {
						err = errors.New("uncontended TryAcquire failed")
					}
				} else {
					owner, err = Acquire(ownerDir)
				}
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				type result struct {
					lock *Lock
					err  error
				}
				const contenders = 8
				results := make(chan result, contenders)
				defer func() {
					cancel()
					if owner != nil {
						_ = owner.Release()
					}
					synctest.Wait()
					for len(results) > 0 {
						if pending := <-results; pending.lock != nil {
							_ = pending.lock.Release()
						}
					}
				}()
				for range contenders {
					go func() {
						next, err := AcquireContext(ctx, dir)
						results <- result{next, err}
					}()
				}
				synctest.Wait()
				if len(results) != 0 {
					t.Fatal("contender escaped the live owner's fence")
				}
				started := time.Now()
				for range contenders {
					if err := owner.Release(); err != nil {
						t.Fatal(err)
					}
					owner = nil
					synctest.Wait()
					select {
					case next := <-results:
						owner = next.lock
						if next.err != nil {
							t.Fatal(next.err)
						}
					default:
						t.Fatal("released local owner left contenders waiting for a polling tick")
					}
					if probe, acquired, err := TryAcquire(dir); err != nil || acquired {
						_ = probe.Release()
						t.Fatalf("handoff lost exclusivity: acquired=%v err=%v", acquired, err)
					}
				}
				if elapsed := time.Since(started); elapsed != 0 {
					t.Fatalf("local handoff advanced the retry clock: %s", elapsed)
				}
				if err := owner.Release(); err != nil {
					t.Fatal(err)
				}
				assertLocalWaiterRefs(t, dir, 0)
			})
		})
	}
}

func assertLocalWaiterRefs(t *testing.T, dir string, want int) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	localWaiters.Lock()
	defer localWaiters.Unlock()
	got := 0
	if waiters := localWaiters.paths[path]; waiters != nil {
		got = waiters.refs
		if got == 0 {
			t.Fatal("empty local waiter entry was retained")
		}
	}
	if got != want {
		t.Fatalf("live lock/waiter references = %d, want %d", got, want)
	}
}

func TestCanceledLocalContenderPreservesOwnerAndReleasesReferences(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		owner, err := Acquire(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Release()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			next, err := AcquireUntilCanceled(ctx, dir)
			if next != nil {
				err = errors.Join(err, next.Release())
			}
			result <- err
		}()
		synctest.Wait()
		assertLocalWaiterRefs(t, dir, 2)
		cancel()
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled contender returned %v", err)
		}
		assertLocalWaiterRefs(t, dir, 1)
		if next, acquired, err := TryAcquire(dir); next != nil || acquired || err != nil {
			_ = next.Release()
			t.Fatalf("cancellation changed live ownership: %v %v", acquired, err)
		}
		// Duplicate concurrent releases must neither double-broadcast nor retain
		// the old path entry after its last owner goes away.
		releaseErrors := make(chan error, 8)
		for range cap(releaseErrors) {
			go func() { releaseErrors <- owner.Release() }()
		}
		synctest.Wait()
		for range cap(releaseErrors) {
			if err := <-releaseErrors; err != nil {
				t.Fatal(err)
			}
		}
		assertLocalWaiterRefs(t, dir, 0)
		next, err := Acquire(dir)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()
		go func() {
			contender, err := AcquireContext(ctx, dir)
			if contender != nil {
				err = errors.Join(err, contender.Release())
			}
			result <- err
		}()
		synctest.Wait()
		go cancel()
		go func() { releaseErrors <- next.Release() }()
		synctest.Wait()
		if err := <-releaseErrors; err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("release/cancellation race returned %v", err)
		}
		assertLocalWaiterRefs(t, dir, 0)
	})
}

// An owner without a matching local subscription (another process or a path
// alias) must still be fenced by the OS lock and discovered by the retry timer.
func TestUnnotifiedOwnerReleaseUsesOSLockFallback(t *testing.T) {
	for _, name := range []string{"unregistered_OS_owner", "hardlink_alias"} {
		t.Run(name, func(t *testing.T) {
			dir, contenderDir := t.TempDir(), t.TempDir()
			synctest.Test(t, func(t *testing.T) {
				var release func() error
				if name == "hardlink_alias" {
					owner, err := Acquire(dir)
					if err != nil {
						t.Fatal(err)
					}
					release = owner.Release
					defer release()
					if err := os.Link(filepath.Join(dir, FileName), filepath.Join(contenderDir, FileName)); err != nil {
						t.Fatal(err)
					}
				} else {
					contenderDir = dir
					file, err := os.OpenFile(filepath.Join(dir, FileName), os.O_CREATE|os.O_RDWR, 0o600)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					if err := tryLockFile(file); err != nil {
						t.Fatal(err)
					}
					release = func() error { return errors.Join(unlockFile(file), file.Close()) }
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result := make(chan error, 1)
				go func() {
					next, err := AcquireContext(ctx, contenderDir)
					if next != nil {
						err = errors.Join(err, next.Release())
					}
					result <- err
				}()
				synctest.Wait()
				if len(result) != 0 {
					t.Fatal("contender bypassed the OS owner")
				}
				if err := release(); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if len(result) != 0 {
					t.Fatal("unrelated path unexpectedly notified this contender")
				}
				time.Sleep(acquireRetryDelay)
				synctest.Wait()
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("fallback did not discover the released OS lock")
				}
				assertLocalWaiterRefs(t, dir, 0)
				assertLocalWaiterRefs(t, contenderDir, 0)
			})
		})
	}
}

func TestReleaseErrorStillSettlesLocalReferences(t *testing.T) {
	dir := t.TempDir()
	owner, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A descriptor closed unexpectedly makes both unlock/close report errors.
	// Release must preserve that error without leaking its notification owner.
	if err := owner.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Release(); err == nil {
		t.Fatal("failed release was reported as success")
	}
	assertLocalWaiterRefs(t, dir, 0)
	if err := owner.Release(); err != nil {
		t.Fatalf("duplicate Release was not idempotent: %v", err)
	}
	next, acquired, err := TryAcquire(dir)
	if err != nil || !acquired {
		t.Fatalf("released descriptor stranded its successor: %v %v", acquired, err)
	}
	if err := next.Release(); err != nil {
		t.Fatal(err)
	}
	assertLocalWaiterRefs(t, dir, 0)
}
