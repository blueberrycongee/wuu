package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestProcessUpdateScheduleLifecycle(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-update-background")

	started := startBackgroundForTest(t, kit, map[string]any{"command": "sleep 60"})

	updateResp, err := executeEnvelope(kit, context.Background(), providers.ToolCall{
		Name:      "process",
		Arguments: `{"action":"update","process_id":"` + started.ID + `","recheck_minutes":10}`,
	})
	if err != nil {
		t.Fatalf("update background: %v", err)
	}
	var updated struct {
		RecheckMinutes int `json:"recheck_minutes"`
	}
	if err := json.Unmarshal([]byte(updateResp), &updated); err != nil {
		t.Fatalf("parse update response: %v\n%s", err, updateResp)
	}
	if updated.RecheckMinutes != 10 {
		t.Fatalf("recheck_minutes = %d, want 10", updated.RecheckMinutes)
	}
	record, err := manager.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.RecheckMinutes != 10 || record.NextRecheckAt.IsZero() {
		t.Fatalf("schedule not persisted: %+v", record)
	}
	if !record.TTY {
		t.Fatalf("background commands should default to an interactive PTY: %+v", record)
	}

	cancelResp, err := executeEnvelope(kit, context.Background(), providers.ToolCall{
		Name:      "process",
		Arguments: `{"action":"update","process_id":"` + started.ID + `","recheck_minutes":0}`,
	})
	if err != nil {
		t.Fatalf("cancel recheck: %v", err)
	}
	if !strings.Contains(cancelResp, `"recheck_minutes":0`) {
		t.Fatalf("cancellation should report recheck_minutes 0: %s", cancelResp)
	}

	// A resume-mode process holds the owning turn open; detaching releases it.
	if record.CompletionMode != proc.CompletionModeResume {
		t.Fatalf("background commands should default to resume mode: %+v", record)
	}
	if record.Lifecycle != proc.LifecycleManaged || record.RootThreadID != "thread-update-background" {
		t.Fatalf("background command must retain its owner for automatic wake-up: %+v", record)
	}
	if _, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "process",
		Arguments: `{"action":"update","process_id":"` + started.ID + `","completion_mode":"detached"}`,
	}); err != nil {
		t.Fatalf("detach process: %v", err)
	}
	record, err = manager.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.CompletionMode != proc.CompletionModeDetached || record.RecheckMinutes != 0 {
		t.Fatalf("completion mode update not persisted or clobbered the schedule: %+v", record)
	}
}

func TestBashRunTimeoutPromotesToBackground(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	kit.SetSessionID("thread-promoted-background")
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)

	resp, err := executeEnvelope(kit, context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"printf 'partial\\n'; sleep 30","timeout_seconds":1}`,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var result shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		t.Fatalf("parse run result: %v\n%s", err, resp)
	}
	if !result.TimedOut {
		t.Fatalf("run should report the timeout: %+v", result)
	}
	if result.PromotedProcessID == "" {
		t.Fatalf("run should promote the timed-out command instead of killing it: %+v", result)
	}
	if !strings.Contains(result.StdoutTail, "partial") {
		t.Fatalf("promotion should attach the output captured so far: %+v", result)
	}
	record, err := manager.Get(result.PromotedProcessID)
	if err != nil {
		t.Fatalf("promoted process should be managed: %v", err)
	}
	if record.Status != proc.StatusRunning {
		t.Fatalf("promoted process should keep running: %+v", record)
	}
	if record.Lifecycle != proc.LifecycleManaged || record.RootThreadID != "thread-promoted-background" {
		t.Fatalf("promoted process must retain its owner for automatic wake-up: %+v", record)
	}
	if record.RecheckMinutes != defaultPromotedRecheckMinutes || record.NextRecheckAt.IsZero() {
		t.Fatalf("promoted process should carry the safety-net recheck: %+v", record)
	}
	stopped, err := manager.Stop(result.PromotedProcessID)
	if err != nil {
		t.Fatalf("stop promoted process: %v", err)
	}
	if stopped.Status != proc.StatusStopped {
		t.Fatalf("promoted process should stop cleanly: %+v", stopped)
	}
}

func TestBashPromotedVerificationRemainsPending(t *testing.T) {
	root := t.TempDir()
	runner := filepath.Join(root, "node_modules", ".bin", "vitest")
	mustWriteFile(t, runner, "#!/bin/sh\nprintf 'partial\\n'\nsleep 30\n")
	if err := os.Chmod(runner, 0o755); err != nil {
		t.Fatal(err)
	}
	kit := newShellTestToolkit(t, root)
	kit.SetSessionID("thread-pending-verification")
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	resp, err := executeEnvelope(kit, context.Background(), providers.ToolCall{
		Name: "bash", Arguments: `{"command":"npx vitest --run","timeout_seconds":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var result shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		t.Fatal(err)
	}
	if result.PromotedProcessID == "" {
		t.Fatalf("verification must continue as a managed process: %s", resp)
	}
	record, err := manager.Get(result.PromotedProcessID)
	if err != nil || record.Status != proc.StatusRunning {
		t.Fatalf("verification must still be running: %+v, %v", record, err)
	}
	if result.Verification != nil {
		t.Error("a running verification must not acquire a completed verdict")
	}
}
