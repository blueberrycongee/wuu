package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestProcessReadNextSuggestions(t *testing.T) {
	liveProcess := proc.Process{Status: proc.StatusRunning}
	deadProcess := proc.Process{Status: proc.StatusStopped}

	if got := processReadNextSuggestions(0, 0, proc.OutputSnapshot{}, liveProcess); got != nil {
		t.Fatalf("plain snapshots need no guidance: %v", got)
	}
	if got := processReadNextSuggestions(5000, processWaitMinDwell, proc.OutputSnapshot{}, deadProcess); got != nil {
		t.Fatalf("terminal processes need no wait guidance: %v", got)
	}
	timedOut := processReadNextSuggestions(5000, processWaitMinDwell, proc.OutputSnapshot{TimedOut: true}, liveProcess)
	if len(timedOut) != 1 || !strings.Contains(timedOut[0], "Do not wait on it again") || !strings.Contains(timedOut[0], "recheck_minutes") {
		t.Fatalf("an expired wait should steer away from re-waiting and toward rechecks: %v", timedOut)
	}
	chatty := processReadNextSuggestions(120000, processWaitMinDwell, proc.OutputSnapshot{Duration: processWaitMinDwell}, liveProcess)
	if len(chatty) != 1 || !strings.Contains(chatty[0], "continuously") {
		t.Fatalf("a wait released at the pacing floor should name the chatty pattern: %v", chatty)
	}
	if quiet := processReadNextSuggestions(120000, processWaitMinDwell, proc.OutputSnapshot{Duration: 90 * time.Second}, liveProcess); quiet != nil {
		t.Fatalf("a normal early return needs no guidance: %v", quiet)
	}
}

func TestProcessUpdateRequiresProcessID(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "process",
		Arguments: `{"action":"update","recheck_minutes":10}`,
	})
	if err == nil || !strings.Contains(err.Error(), "process_id") {
		t.Fatalf("update without process_id should fail: %v", err)
	}
}

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

func TestProcessUpdateRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-invalid-update")
	started := startBackgroundForTest(t, kit, map[string]any{"command": "sleep 5"})

	for args, want := range map[string]string{
		`{"action":"update","process_id":"` + started.ID + `","recheck_minutes":-3}`:          "recheck_minutes",
		`{"action":"update","process_id":"` + started.ID + `","completion_mode":"sometimes"}`: "completion_mode",
	} {
		if _, err := kit.Execute(context.Background(), providers.ToolCall{Name: "process", Arguments: args}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s should fail on %s: %v", args, want, err)
		}
	}
}

func TestBashRunTimeoutPromotesToBackground(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
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
