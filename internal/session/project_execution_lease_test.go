package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// Admission contracts:
// competing admissions share one bound, mutation ownership is conservative,
// membership is durable, and coordination sessions remain outside the pool.
func createProjectCapacityMember(t *testing.T, dir, id, project, role string) {
	t.Helper()
	_, err := CreateInitialized(dir, Session{ID: id, Source: "project-session", ParentID: project, ProjectRole: role}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectExecutionCapacityAtomicContenders(t *testing.T) {
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			dir := t.TempDir()
			for i := 0; i < limit+2; i++ {
				createProjectCapacityMember(t, dir, fmt.Sprint(i), "project", "worker")
			}
			gate := make(chan struct{})
			type result struct {
				lease    *ThreadExecutionLease
				acquired bool
				err      error
			}
			results := make(chan result, limit+2)
			var wg sync.WaitGroup
			for i := 0; i < limit+2; i++ {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					<-gate
					l, a, e := TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id, limit)
					results <- result{l, a, e}
				}(fmt.Sprint(i))
			}
			close(gate)
			wg.Wait()
			close(results)
			admitted, blocked := 0, 0
			for r := range results {
				if r.lease != nil {
					defer r.lease.Release()
				}
				if r.acquired {
					admitted++
					if r.err != nil || r.lease == nil {
						t.Fatalf("invalid success: %+v", r)
					}
				} else if errors.Is(r.err, ErrProjectWorkerCapacity) {
					blocked++
				} else {
					t.Fatalf("unexpected rejected result: %+v", r)
				}
			}
			if admitted != limit || blocked != 2 {
				t.Fatalf("admitted %d blocked %d; want %d and 2", admitted, blocked, limit)
			}
		})
	}
}

func TestProjectExecutionCapacityMembershipAndMutation(t *testing.T) {
	dir := t.TempDir()
	createProjectCapacityMember(t, dir, "held", "p", "worker")
	createProjectCapacityMember(t, dir, "target", "p", "")
	createProjectCapacityMember(t, dir, "side", "p", "side")
	createProjectCapacityMember(t, dir, "technical", "p", "technical_lead")
	createProjectCapacityMember(t, dir, "executor", "p", "executor")
	createProjectCapacityMember(t, dir, "other", "q", "worker")
	_, err := CreateInitialized(dir, Session{ID: "lead", Source: "project"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	held, ok, err := TryAcquireThreadExecutionLease(dir, "held")
	if err != nil || !ok {
		t.Fatalf("mutation acquire: %v %v", ok, err)
	}
	defer held.Release()
	l, ok, err := TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "target", 1)
	if l != nil {
		l.Release()
	}
	if ok || !errors.Is(err, ErrProjectWorkerCapacity) {
		t.Fatalf("legacy worker bypassed mutation occupancy: %v %v", ok, err)
	}
	for _, id := range []string{"technical", "executor"} {
		l, ok, err := TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id, 1)
		if l != nil {
			l.Release()
		}
		if ok || !errors.Is(err, ErrProjectWorkerCapacity) {
			t.Fatalf("background role %s bypassed quota: %v %v", id, ok, err)
		}
	}

	for _, id := range []string{"side", "other", "lead"} {
		l, ok, err := TryAcquireThreadExecutionLeaseWithProjectLimit(dir, id, 1)
		if l != nil {
			l.Release()
		}
		if err != nil || !ok {
			t.Fatalf("excluded %s blocked: %v %v", id, ok, err)
		}
	}
	if _, err := SetProjectMembership(dir, "target", "", "", ""); err != nil {
		t.Fatal(err)
	}
	l, ok, err = TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "target", 1)
	if err != nil || !ok {
		t.Fatalf("released member capped: %v %v", ok, err)
	}
	l.Release()
	if _, err := SetProjectMembership(dir, "target", "project-session", "p", ""); err != nil {
		t.Fatal(err)
	}
	l, ok, err = TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "target", 1)
	if l != nil {
		l.Release()
	}
	if ok || !errors.Is(err, ErrProjectWorkerCapacity) {
		t.Fatalf("adopted member uncapped: %v %v", ok, err)
	}
	// Reset requests must not count as completion or free the owner's slot.
	if requested, err := RequestThreadExecutionReset(dir, "held"); err != nil || !requested {
		t.Fatalf("request reset: %v %v", requested, err)
	}
	l, ok, err = TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "target", 1)
	if l != nil {
		l.Release()
	}
	if ok || !errors.Is(err, ErrProjectWorkerCapacity) {
		t.Fatalf("reset freed live owner: %v %v", ok, err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	l, ok, err = TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "target", 1)
	if err != nil || !ok {
		t.Fatalf("release did not restore capacity: %v %v", ok, err)
	}
	l.Release()
}

func TestProjectExecutionAdmissionMutationAndRelease(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a", "b"} {
		if _, err := CreateInitialized(dir, Session{ID: id, Source: "project-session", ParentID: "p"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	lease, ok, err := TryAcquireThreadExecutionLease(dir, "a")
	if err != nil || !ok {
		t.Fatalf("mutation admission: %v %v", ok, err)
	}
	defer lease.Release()
	if l, ok, err := TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "b", 1); ok || l != nil || !errors.Is(err, ErrProjectWorkerCapacity) {
		t.Fatalf("capacity: %v %v", ok, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	l, ok, err := TryAcquireThreadExecutionLeaseWithProjectLimit(dir, "b", 1)
	if err != nil || !ok {
		t.Fatalf("after release: %v %v", ok, err)
	}
	defer l.Release()
	mutation, ok, err := TryAcquireThreadExecutionLease(dir, "a")
	if err != nil || !ok {
		t.Fatalf("mutation while full: %v %v", ok, err)
	}
	defer mutation.Release()
}
