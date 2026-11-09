package storelock

import (
	"testing"
)

// Membership liveness is an OS-lock fact, including in the same process; no
// timeout or stale heartbeat may steal another owner's membership.
func TestTryAcquireRetainsLiveOwnership(t *testing.T) {
	dir := t.TempDir()
	first, acquired, err := TryAcquire(dir)
	if err != nil || !acquired {
		t.Fatalf("first: %v %v", acquired, err)
	}
	defer first.Release()
	second, acquired, err := TryAcquire(dir)
	if err != nil || acquired || second != nil {
		t.Fatalf("stole active lock: %v %v", acquired, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, acquired, err = TryAcquire(dir)
	if err != nil || !acquired {
		t.Fatalf("released owner blocked successor: %v %v", acquired, err)
	}
	defer second.Release()
}
