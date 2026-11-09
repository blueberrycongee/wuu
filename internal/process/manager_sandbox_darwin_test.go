//go:build darwin

package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/processsandbox"
)

func TestManagedProcessReportsFilesystemSandboxDenial(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(workspace, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	insideFile := filepath.Join(workspace, "inside")
	outsideFile := filepath.Join(outside, "outside")
	policy := &processsandbox.Policy{Mode: processsandbox.ModeWorkspaceWrite, WritableRoots: []string{workspace}}
	p, err := m.Start(context.Background(), StartOptions{
		Command:       `printf inside > "` + insideFile + `"; printf outside > "` + outsideFile + `"`,
		OwnerKind:     OwnerMainAgent,
		OwnerID:       "sandbox-test",
		TTY:           false,
		SandboxPolicy: policy,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err = m.Get(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.Status == StatusStopped || p.Status == StatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process did not finish: %+v", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p.SandboxMode != processsandbox.ModeWorkspaceWrite || !p.SandboxDenied || p.SandboxRunnerFailed {
		t.Fatalf("sandbox facts = mode=%q denied=%v runner_failed=%v", p.SandboxMode, p.SandboxDenied, p.SandboxRunnerFailed)
	}
	if _, err := os.Stat(insideFile); err != nil {
		t.Fatalf("inside write missing: %v", err)
	}
	if _, err := os.Stat(outsideFile); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}
}
