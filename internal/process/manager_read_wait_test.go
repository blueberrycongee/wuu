package process

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitForProcessStatus(t *testing.T, m *Manager, id string, want Status, timeout time.Duration) Process {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		p, err := m.Get(id)
		if err == nil && p.Status == want {
			return *p
		}
		if time.Now().After(deadline) {
			current, _ := m.Get(id)
			t.Fatalf("process %q did not reach status %q within %s (current: %+v)", id, want, timeout, current)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestReadOutputSnapshotMinDwellPacesOutputReturn(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Start(context.Background(), StartOptions{Command: "printf 'tick\\n'; sleep 30", OwnerKind: OwnerMainAgent, OwnerID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Stop(p.ID) }()

	zero := int64(0)
	paced, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{
		MaxBytes:    4096,
		OffsetBytes: &zero,
		Wait:        3 * time.Second,
		MinDwell:    time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paced.Output, "tick") {
		t.Fatalf("paced wait should return the new output: %+v", paced)
	}
	if paced.Duration < 900*time.Millisecond {
		t.Fatalf("min dwell should pace the output-driven return, returned after %s", paced.Duration)
	}
	if paced.TimedOut {
		t.Fatal("output arrived before the deadline; the wait should not time out")
	}

	// Without a minimum dwell the same read releases as soon as output is
	// visible.
	unpaced, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{
		MaxBytes:    4096,
		OffsetBytes: &zero,
		Wait:        3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unpaced.Duration > 900*time.Millisecond {
		t.Fatalf("zero min dwell should return on first output, took %s", unpaced.Duration)
	}
}

func TestReadOutputSnapshotExitReleasesImmediatelyWithMinDwell(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Start(context.Background(), StartOptions{Command: "exit 0", OwnerKind: OwnerMainAgent, OwnerID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	waitForProcessStatus(t, m, p.ID, StatusStopped, 5*time.Second)

	zero := int64(0)
	snapshot, err := m.ReadOutputSnapshot(context.Background(), p.ID, OutputReadOptions{
		MaxBytes:    4096,
		OffsetBytes: &zero,
		Wait:        5 * time.Second,
		MinDwell:    30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Duration > time.Second {
		t.Fatalf("process exit must release the wait immediately regardless of min dwell, took %s", snapshot.Duration)
	}
}
