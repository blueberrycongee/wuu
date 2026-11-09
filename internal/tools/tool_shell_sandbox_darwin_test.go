//go:build darwin

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteShellCommandEnforcesWorkspaceBoundary(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(home, ".wuu-process-sandbox-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	insideFile := filepath.Join(workspace, "inside")
	outsideFile := filepath.Join(outside, "outside")
	command := `printf inside > "` + insideFile + `"; printf outside > "` + outsideFile + `"`
	result, err := executeShellCommandInDir(context.Background(), &Env{RootDir: workspace}, command, 10, workspace)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Sandbox == nil || !result.Sandbox.Denied || result.Sandbox.Enforcement != "full" {
		t.Fatalf("sandbox result = %#v", result.Sandbox)
	}
	if _, err := os.Stat(insideFile); err != nil {
		t.Fatalf("inside write missing: %v", err)
	}
	if _, err := os.Stat(outsideFile); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}
	if len(result.NextSuggestions) == 0 {
		t.Fatal("denial omitted model next action")
	}
}

func TestExecuteShellCommandUsesPrivateWritableTemp(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(home, ".wuu-process-sandbox-temp-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	workspace := filepath.Join(base, "workspace")
	stateDir := filepath.Join(base, "state")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	sharedTempFile := filepath.Join(os.TempDir(), "wuu-sandbox-shared-temp-"+filepath.Base(base))
	t.Cleanup(func() { _ = os.Remove(sharedTempFile) })
	command := `printf private > "$TMPDIR/private"; printf blocked > "` + sharedTempFile + `"`
	result, err := executeShellCommandInDir(context.Background(), &Env{RootDir: workspace, StateDir: stateDir}, command, 10, workspace)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Sandbox == nil || !result.Sandbox.Denied {
		t.Fatalf("sandbox result = %#v, want denial", result.Sandbox)
	}
	if data, err := os.ReadFile(filepath.Join(stateDir, "process-tmp", "private")); err != nil || string(data) != "private" {
		t.Fatalf("private temp write = %q, %v", data, err)
	}
	if _, err := os.Stat(sharedTempFile); !os.IsNotExist(err) {
		t.Fatalf("shared temp file exists or stat failed unexpectedly: %v", err)
	}
}

func TestExecuteShellCommandUnconfinedBypassesSandbox(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "allowed")
	env := &Env{RootDir: workspace, Unconfined: true}
	result, err := executeShellCommandInDir(context.Background(), env, `printf allowed > "`+target+`"`, 10, workspace)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("unconfined execute result=%+v err=%v", result, err)
	}
	if result.Sandbox != nil {
		t.Fatalf("unconfined execution reported sandbox: %#v", result.Sandbox)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("unconfined write missing: %v", err)
	}
}
