//go:build darwin

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/processsandbox"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBashStartBackgroundDefaultTTYEnforcesSandbox(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(home, ".wuu-background-sandbox-test-")
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
	kit, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetStateDir(filepath.Join(base, "state"))
	kit.SetSessionID("background-sandbox-test")
	manager, err := proc.NewManager(workspace, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	insideFile := filepath.Join(workspace, "inside")
	outsideFile := filepath.Join(outside, "outside")
	arguments, err := json.Marshal(map[string]any{
		"action":  "start",
		"command": `printf temp > "$TMPDIR/background"; printf inside > "` + insideFile + `"; printf outside > "` + outsideFile + `"`,
		"wait_ms": 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := kit.Execute(context.Background(), providers.ToolCall{Name: "process", Arguments: string(arguments)})
	if err != nil {
		t.Fatalf("process start: %v", err)
	}
	var started proc.Process
	if err := json.Unmarshal([]byte(response), &started); err != nil {
		t.Fatalf("decode response: %v\n%s", err, response)
	}
	deadline := time.Now().Add(5 * time.Second)
	for started.Status != proc.StatusStopped && started.Status != proc.StatusFailed {
		current, err := manager.Get(started.ID)
		if err != nil {
			t.Fatal(err)
		}
		started = *current
		if time.Now().After(deadline) {
			t.Fatalf("process did not finish: %+v", started)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started.TTY || started.SandboxMode != processsandbox.ModeWorkspaceWrite || !started.SandboxDenied || started.SandboxRunnerFailed {
		t.Fatalf("managed sandbox facts = %+v", started)
	}
	if _, err := os.Stat(insideFile); err != nil {
		t.Fatalf("inside write missing: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(base, "state", "process-tmp", "background")); err != nil || string(data) != "temp" {
		t.Fatalf("background private temp write = %q, %v", data, err)
	}
	if _, err := os.Stat(outsideFile); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}
}
