//go:build !windows

package tools

import (
	"encoding/json"
	"path/filepath"
	"syscall"
	"testing"

	proc "github.com/blueberrycongee/wuu/internal/process"
)

func TestActionFusionTimeoutKeepsManagedProcessReachable(t *testing.T) {
	root := t.TempDir()
	// The reader cannot finish until a writer opens the FIFO, so the test does
	// not race a fixed sleep against the foreground timeout.
	if err := syscall.Mkfifo(filepath.Join(root, "hold.fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.CleanupSession() })
	kit.SetProcessManager(manager)
	runtime, _ := fusionRuntime(t, kit)
	message := runFusion(t, runtime, fusionCall(t, "*** Begin Patch\n*** Add File: retained.txt\n+retained\n*** End Patch", map[string]any{
		"command": "printf waiting; read -r line < hold.fifo", "timeout_seconds": 1,
	}))
	status, command := fusionCommand(t, message.ToolResult)
	var outcome shellExecutionResult
	if err := json.Unmarshal([]byte(producerText(command)), &outcome); err != nil {
		t.Fatal(err)
	}
	if status != "running" || message.ToolResult.IsError || !outcome.TimedOut || outcome.PromotedProcessID == "" {
		t.Fatalf("timeout lost the unfinished command: %s", message.Content)
	}
	process, err := manager.Get(outcome.PromotedProcessID)
	if err != nil || process.Status != proc.StatusRunning {
		t.Fatalf("promoted command is not running: %+v, %v", process, err)
	}
	if mustReadFile(t, filepath.Join(root, "retained.txt")) != "retained\n" {
		t.Fatal("timeout rolled back the patch")
	}
	stopped, err := manager.Stop(outcome.PromotedProcessID)
	if err != nil || stopped.Status != proc.StatusStopped {
		t.Fatalf("fused process cannot be stopped normally: %+v, %v", stopped, err)
	}
}
