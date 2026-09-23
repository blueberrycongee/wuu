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

func TestBashReadBackgroundNextSuggestions(t *testing.T) {
	liveProcess := proc.Process{Status: proc.StatusRunning}
	deadProcess := proc.Process{Status: proc.StatusStopped}

	if got := bashReadBackgroundNextSuggestions(0, 0, proc.OutputSnapshot{}, liveProcess); got != nil {
		t.Fatalf("plain snapshots need no guidance: %v", got)
	}

	terminal := bashReadBackgroundNextSuggestions(5000, backgroundWaitMinDwell, proc.OutputSnapshot{}, deadProcess)
	if len(terminal) == 0 || !strings.Contains(terminal[0], "terminal") {
		t.Fatalf("terminal processes should close the wait path: %v", terminal)
	}

	timedOut := bashReadBackgroundNextSuggestions(5000, backgroundWaitMinDwell, proc.OutputSnapshot{TimedOut: true}, liveProcess)
	if len(timedOut) < 2 || !strings.Contains(timedOut[0], "do not immediately wait again") || !strings.Contains(timedOut[1], "update_background") {
		t.Fatalf("an expired wait should steer away from re-waiting and toward rechecks: %v", timedOut)
	}

	chatty := bashReadBackgroundNextSuggestions(120000, backgroundWaitMinDwell, proc.OutputSnapshot{Duration: backgroundWaitMinDwell}, liveProcess)
	if len(chatty) == 0 || !strings.Contains(chatty[0], "continuously") {
		t.Fatalf("a wait released at the pacing floor should name the chatty pattern: %v", chatty)
	}

	quiet := bashReadBackgroundNextSuggestions(120000, backgroundWaitMinDwell, proc.OutputSnapshot{Duration: 90 * time.Second}, liveProcess)
	if len(quiet) == 0 || !strings.Contains(quiet[0], "end_offset") {
		t.Fatalf("a normal early return should just teach incremental offsets: %v", quiet)
	}
}

func TestBashUpdateBackgroundRequiresProcessID(t *testing.T) {
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"update_background","recheck_minutes":10}`,
	})
	if err == nil || !strings.Contains(err.Error(), "process_id") {
		t.Fatalf("update_background without process_id should fail: %v", err)
	}
}

func TestBashUpdateBackgroundScheduleLifecycle(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-update-background")

	startResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"start_background","command":"sleep 60"}`,
	})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(startResp), &started); err != nil || started.ID == "" {
		t.Fatalf("parse start response: %v\n%s", err, startResp)
	}

	updateResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"update_background","process_id":"` + started.ID + `","recheck_minutes":10}`,
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

	cancelResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"update_background","process_id":"` + started.ID + `","recheck_minutes":0}`,
	})
	if err != nil {
		t.Fatalf("cancel recheck: %v", err)
	}
	if !strings.Contains(cancelResp, `"recheck_minutes":0`) {
		t.Fatalf("cancellation should report recheck_minutes 0: %s", cancelResp)
	}
}

func TestBashStartBackgroundAllowsExplicitLogOnlyMode(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-log-only-background")

	response, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"start_background","command":"sleep 60","tty":false}`,
	})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	var started struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(response), &started); err != nil || started.ID == "" {
		t.Fatalf("parse start response: %v\n%s", err, response)
	}
	record, err := manager.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.TTY {
		t.Fatalf("tty=false should preserve log-only background execution: %+v", record)
	}
}

func TestBashStartBackgroundRejectsInvalidRecheck(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-invalid-recheck")

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"start_background","command":"sleep 5","recheck_minutes":-3}`,
	})
	if err == nil || !strings.Contains(err.Error(), "recheck_minutes") {
		t.Fatalf("negative recheck_minutes should fail: %v", err)
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

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"run","command":"printf 'partial\\n'; sleep 30","timeout_seconds":1}`,
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
