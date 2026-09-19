package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proc "github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestBashDefinitionExplainsInteractiveBackgroundFlow(t *testing.T) {
	def := NewBashTool(&Env{}).Definition()
	for _, want := range []string{"action=start_background", "interactive pseudo-terminal by default", "tty=false", "action=write_background", "action=read_background", "starts a new turn", "end the turn", "recheck_minutes", "update_background", "timeout it keeps running"} {
		if !strings.Contains(def.Description, want) {
			t.Fatalf("bash description must teach %q interactive flow: %q", want, def.Description)
		}
	}
	properties, ok := def.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("bash properties schema has unexpected type: %T", def.InputSchema["properties"])
	}
	command, ok := properties["command"].(map[string]any)
	if !ok {
		t.Fatalf("bash command schema has unexpected type: %T", properties["command"])
	}
	commandDescription, _ := command["description"].(string)
	if !strings.Contains(commandDescription, "action=run must be non-interactive") || !strings.Contains(commandDescription, "action=start_background is interactive by default") {
		t.Fatalf("bash command description does not distinguish run from interactive background use: %q", commandDescription)
	}
	wait, ok := properties["wait_ms"].(map[string]any)
	if !ok || !strings.Contains(wait["description"].(string), "Do not chain waits") {
		t.Fatalf("bash wait_ms description does not explain bounded waits: %+v", wait)
	}
	recheck, ok := properties["recheck_minutes"].(map[string]any)
	if !ok || !strings.Contains(recheck["description"].(string), "wake-ups") {
		t.Fatalf("bash recheck_minutes does not explain scheduled wake-ups: %+v", recheck)
	}
	completionMode, ok := properties["completion_mode"].(map[string]any)
	if !ok || !strings.Contains(completionMode["description"].(string), "long-lived services") {
		t.Fatalf("bash completion_mode does not explain detached services: %+v", completionMode)
	}
}

func TestBashBackgroundSuggestionsExplainTurnHandoff(t *testing.T) {
	for _, waitMS := range []int{0, 500} {
		suggestions := strings.Join(bashBackgroundNextSuggestions(waitMS, ""), " ")
		for _, want := range []string{"only remaining dependency", "end this turn", "start a new turn"} {
			if !strings.Contains(suggestions, want) {
				t.Fatalf("background suggestions for wait_ms=%d omitted %q: %s", waitMS, want, suggestions)
			}
		}
	}
	detached := strings.Join(bashBackgroundNextSuggestions(0, "detached"), " ")
	if !strings.Contains(detached, "will not start another model turn") {
		t.Fatalf("detached suggestions omitted non-resume behavior: %s", detached)
	}
}

func TestBashRunRecordsFullLogSHA256(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	kit.SetSessionDir(t.TempDir())
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"printf 'hello-log'"}`,
	})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	var parsed shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse bash result: %v\n%s", err, resp)
	}
	if parsed.FullLogRef == "" || parsed.FullLogSHA256 == "" {
		t.Fatalf("bash run must record a snapshot-bound full log: ref=%q sha=%q", parsed.FullLogRef, parsed.FullLogSHA256)
	}
	data, err := os.ReadFile(parsed.FullLogRef)
	if err != nil {
		t.Fatalf("read full log: %v", err)
	}
	if got := sha256Hex(data); got != parsed.FullLogSHA256 {
		t.Fatalf("full_log_sha256 = %q, file hash %q", parsed.FullLogSHA256, got)
	}
}

func TestBashRunAddsVerificationSummaryAndRepeatGuard(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), "module example.com/bashverify\n\ngo 1.22\n")
	mustWriteFile(t, filepath.Join(root, "fail_test.go"), `package bashverify

import "testing"

func TestBashVerificationFailure(t *testing.T) {
	t.Fatalf("expected green")
}
`)

	kit := newShellTestToolkit(t, root)
	kit.SetSessionDir(t.TempDir())

	call := providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"go test ./...","scope":"targeted","purpose":"verify bash summary"}`,
	}
	for i := 0; i < maxRepeatedRunTestFailures; i++ {
		resp, err := kit.Execute(context.Background(), call)
		if err != nil {
			t.Fatalf("bash verification run %d: %v", i+1, err)
		}
		var parsed shellExecutionResult
		if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
			t.Fatalf("parse bash result: %v\n%s", err, resp)
		}
		if parsed.Verification == nil {
			t.Fatalf("bash verification metadata missing: %s", resp)
		}
		if parsed.Verification.Passed {
			t.Fatalf("failing go test should not pass: %+v", parsed.Verification)
		}
		if !parsed.Verification.FailureSummary.Failed || !containsString(parsed.Verification.FailureSummary.FailingTests, "TestBashVerificationFailure") {
			t.Fatalf("failure summary did not identify failing test: %+v", parsed.Verification.FailureSummary)
		}
		if parsed.Verification.RepeatGuard["max_failed_runs_without_revision_change"] != float64(maxRepeatedRunTestFailures) {
			t.Fatalf("verification repeat guard missing: %+v", parsed.Verification.RepeatGuard)
		}
	}

	_, err := kit.Execute(context.Background(), call)
	if err == nil || !strings.Contains(err.Error(), "bash blocked repeated failing verification command") {
		t.Fatalf("expected bash verification repeat guard, got %v", err)
	}
}

func TestBashRunResolvesLocalNpxVerificationRunner(t *testing.T) {
	root := t.TempDir()
	runnerPath := filepath.Join(root, "node_modules", ".bin", "vitest")
	mustWriteFile(t, runnerPath, "#!/usr/bin/env bash\nprintf 'local vitest %s\\n' \"$*\"\n")
	if err := os.Chmod(runnerPath, 0o755); err != nil {
		t.Fatalf("chmod runner: %v", err)
	}

	kit := newShellTestToolkit(t, root)
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"npx vitest --run","scope":"targeted"}`,
	})
	if err != nil {
		t.Fatalf("bash npx vitest: %v", err)
	}
	var parsed shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse bash result: %v\n%s", err, resp)
	}
	if parsed.RequestedCommand != "npx vitest --run" {
		t.Fatalf("requested command not preserved: %+v", parsed)
	}
	if parsed.Command != "./node_modules/.bin/vitest --run" || parsed.ResolvedCommand != parsed.Command {
		t.Fatalf("npx command was not resolved to local runner: %+v", parsed)
	}
	if parsed.Verification == nil || !parsed.Verification.Passed {
		t.Fatalf("local vitest verification should pass: %+v", parsed.Verification)
	}
}

func TestBashRunResolvesLocalNpxTypecheckRunner(t *testing.T) {
	root := t.TempDir()
	runnerPath := filepath.Join(root, "desktop", "node_modules", ".bin", "tsc")
	mustWriteFile(t, runnerPath, "#!/usr/bin/env bash\nprintf 'local tsc %s\\n' \"$*\"\n")
	if err := os.Chmod(runnerPath, 0o755); err != nil {
		t.Fatalf("chmod runner: %v", err)
	}

	kit := newShellTestToolkit(t, root)
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"cd desktop && npx tsc --noEmit","scope":"targeted"}`,
	})
	if err != nil {
		t.Fatalf("bash npx tsc: %v", err)
	}
	var parsed shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse bash result: %v\n%s", err, resp)
	}
	if parsed.RequestedCommand != "cd desktop && npx tsc --noEmit" {
		t.Fatalf("requested command not preserved: %+v", parsed)
	}
	if parsed.Command != "cd desktop && ./node_modules/.bin/tsc --noEmit" || parsed.ResolvedCommand != parsed.Command {
		t.Fatalf("npx tsc command was not resolved to local runner: %+v", parsed)
	}
	if parsed.Verification == nil || !parsed.Verification.Passed {
		t.Fatalf("local tsc verification should pass: %+v", parsed.Verification)
	}
	if !strings.Contains(parsed.Output, "local tsc --noEmit") {
		t.Fatalf("local tsc output missing: %+v", parsed)
	}
}

func TestBashRunResolvesLocalNpxTypecheckRunnerWithProjectOptionOrder(t *testing.T) {
	root := t.TempDir()
	runnerPath := filepath.Join(root, "desktop", "node_modules", ".bin", "tsc")
	mustWriteFile(t, runnerPath, "#!/usr/bin/env bash\nprintf 'local tsc %s\\n' \"$*\"\n")
	if err := os.Chmod(runnerPath, 0o755); err != nil {
		t.Fatalf("chmod runner: %v", err)
	}

	kit := newShellTestToolkit(t, root)
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"cd desktop && npx tsc -p tsconfig.json --noEmit","scope":"targeted"}`,
	})
	if err != nil {
		t.Fatalf("bash npx tsc project noEmit: %v", err)
	}
	var parsed shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse bash result: %v\n%s", err, resp)
	}
	if parsed.RequestedCommand != "cd desktop && npx tsc -p tsconfig.json --noEmit" {
		t.Fatalf("requested command not preserved: %+v", parsed)
	}
	if parsed.Command != "cd desktop && ./node_modules/.bin/tsc -p tsconfig.json --noEmit" || parsed.ResolvedCommand != parsed.Command {
		t.Fatalf("npx tsc command was not resolved to local runner: %+v", parsed)
	}
	if parsed.Verification == nil || !parsed.Verification.Passed {
		t.Fatalf("local tsc verification should pass: %+v", parsed.Verification)
	}
	if !strings.Contains(parsed.Output, "local tsc -p tsconfig.json --noEmit") {
		t.Fatalf("local tsc output missing: %+v", parsed)
	}
}

func TestBashDoesNotTreatMutatingNpxTscCommandsAsVerification(t *testing.T) {
	for _, command := range []string{
		"cd desktop && npx tsc --init",
		"cd desktop && npx tsc --noEmit --init",
		"cd desktop && npx tsc --build",
		"cd desktop && npx tsc --noEmit --build",
		"cd desktop && npx tsc",
	} {
		if testCommandLooksLikeLocalRunnerVerification(command) {
			t.Fatalf("mutating tsc command classified as verification: %q", command)
		}
	}
}

func TestBashRunResolvesWrappedLocalNpxVerificationRunner(t *testing.T) {
	root := t.TempDir()
	runnerPath := filepath.Join(root, "node_modules", ".bin", "vitest")
	mustWriteFile(t, runnerPath, "#!/usr/bin/env bash\nprintf 'wrapped local vitest %s\\n' \"$*\"\n")
	if err := os.Chmod(runnerPath, 0o755); err != nil {
		t.Fatalf("chmod runner: %v", err)
	}

	kit := newShellTestToolkit(t, root)
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"nice npx vitest --run","scope":"targeted"}`,
	})
	if err != nil {
		t.Fatalf("bash wrapped npx vitest: %v", err)
	}
	var parsed shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse bash result: %v\n%s", err, resp)
	}
	if parsed.RequestedCommand != "nice npx vitest --run" {
		t.Fatalf("requested command not preserved: %+v", parsed)
	}
	if parsed.Command != "nice ./node_modules/.bin/vitest --run" || parsed.ResolvedCommand != parsed.Command {
		t.Fatalf("wrapped npx command was not resolved to local runner: %+v", parsed)
	}
	if parsed.Verification == nil || !parsed.Verification.Passed {
		t.Fatalf("wrapped local vitest verification should pass: %+v", parsed.Verification)
	}
	if !strings.Contains(parsed.Output, "wrapped local vitest --run") {
		t.Fatalf("wrapped local vitest output missing: %+v", parsed)
	}
}

func TestBashRunUsesCWD(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "root-only.txt"), "root\n")
	subdir := filepath.Join(root, "desktop")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	kit := newShellTestToolkit(t, root)
	expectedRevision := workspaceRevision(context.Background(), kit.env.RootDir)

	tests := []struct {
		name       string
		includeCWD bool
		cwd        string
		want       string
	}{
		{name: "default root", want: kit.env.RootDir},
		{name: "relative cwd", includeCWD: true, cwd: "desktop", want: filepath.Join(kit.env.RootDir, "desktop")},
		{name: "absolute cwd", includeCWD: true, cwd: filepath.Join(kit.env.RootDir, "desktop"), want: filepath.Join(kit.env.RootDir, "desktop")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"command": "pwd -P"}
			if tc.includeCWD {
				args["cwd"] = tc.cwd
			}
			argsJSON, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("marshal args: %v", err)
			}
			resp, err := kit.Execute(context.Background(), providers.ToolCall{
				Name:      "bash",
				Arguments: string(argsJSON),
			})
			if err != nil {
				t.Fatalf("bash pwd: %v", err)
			}
			var parsed shellExecutionResult
			if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
				t.Fatalf("parse bash result: %v\n%s", err, resp)
			}
			got := filepath.Clean(strings.TrimSpace(parsed.StdoutTail))
			want := filepath.Clean(tc.want)
			if got != want {
				t.Fatalf("pwd = %q, want %q; full result: %+v", got, want, parsed)
			}
			if parsed.WorkspaceRevision != expectedRevision {
				t.Fatalf("workspace revision = %q, want root revision %q", parsed.WorkspaceRevision, expectedRevision)
			}
		})
	}
}

func TestBashRunRejectsInvalidCWD(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "not-a-dir"), "file\n")
	outside := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"pwd","cwd":"missing"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "working directory") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing cwd error, got %v", err)
	}

	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"pwd","cwd":"not-a-dir"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "working directory") || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected file cwd error, got %v", err)
	}

	argsJSON, err := json.Marshal(map[string]any{"command": "pwd", "cwd": outside})
	if err != nil {
		t.Fatalf("marshal outside cwd args: %v", err)
	}
	_, err = kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: string(argsJSON),
	})
	if err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("expected outside cwd rejection, got %v", err)
	}
}

func TestBashRunWithCWDResolvesLocalNpxVerificationRunner(t *testing.T) {
	root := t.TempDir()
	runnerPath := filepath.Join(root, "desktop", "node_modules", ".bin", "vitest")
	mustWriteFile(t, runnerPath, "#!/usr/bin/env bash\nprintf 'desktop vitest %s\\n' \"$*\"\n")
	if err := os.Chmod(runnerPath, 0o755); err != nil {
		t.Fatalf("chmod runner: %v", err)
	}

	kit := newShellTestToolkit(t, root)
	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"command":"npx vitest --run","cwd":"desktop","scope":"targeted"}`,
	})
	if err != nil {
		t.Fatalf("bash npx vitest from cwd: %v", err)
	}
	var parsed shellExecutionResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatalf("parse bash result: %v\n%s", err, resp)
	}
	if parsed.Command != "./node_modules/.bin/vitest --run" || parsed.ResolvedCommand != parsed.Command {
		t.Fatalf("npx command was not resolved relative to cwd: %+v", parsed)
	}
	if !strings.Contains(parsed.Output, "desktop vitest --run") {
		t.Fatalf("cwd-local vitest output missing: %+v", parsed)
	}
	if parsed.Verification == nil || !parsed.Verification.Passed {
		t.Fatalf("cwd-local vitest verification should pass: %+v", parsed.Verification)
	}
}

func TestBashBackgroundModeUsesManagedProcessBackend(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-bash-background")

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"start_background","command":"printf 'ready\n'; sleep 5","wait_ms":500,"max_bytes":4096}`,
	})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	var started startProcessResponse
	if err := json.Unmarshal([]byte(resp), &started); err != nil {
		t.Fatalf("parse start background: %v\n%s", err, resp)
	}
	if started.Action != bashActionStartBackground || started.ID == "" {
		t.Fatalf("unexpected start response: %+v", started)
	}
	if !strings.Contains(started.InitialOutput, "ready") {
		t.Fatalf("initial output should include readiness line: %+v", started)
	}

	listResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"list_background"}`,
	})
	if err != nil {
		t.Fatalf("list background: %v", err)
	}
	if !strings.Contains(listResp, started.ID) || !strings.Contains(listResp, bashActionListBackground) {
		t.Fatalf("list response missing process metadata: %s", listResp)
	}

	readResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"read_background","process_id":"` + started.ID + `","offset_bytes":0,"max_bytes":4096}`,
	})
	if err != nil {
		t.Fatalf("read background: %v", err)
	}
	if !strings.Contains(readResp, bashActionReadBackground) || !strings.Contains(readResp, `"process"`) {
		t.Fatalf("read response missing managed process metadata: %s", readResp)
	}

	stopResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"stop_background","process_id":"` + started.ID + `"}`,
	})
	if err != nil {
		t.Fatalf("stop background: %v", err)
	}
	if !strings.Contains(stopResp, bashActionStopBackground) {
		t.Fatalf("stop response should use bash background action: %s", stopResp)
	}
}

func TestBashReadBackgroundConsumesCompletionWhenTerminalResultIsReturned(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-bash-completion")

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"start_background","command":"printf 'done\\n'; sleep 0.2","wait_ms":2000,"max_bytes":4096}`,
	})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	var started startProcessResponse
	if err := json.Unmarshal([]byte(resp), &started); err != nil {
		t.Fatalf("parse start background: %v\n%s", err, resp)
	}
	if !strings.Contains(started.InitialOutput, "done") {
		t.Fatalf("initial process output was not returned: %+v", started)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pending, pendingErr := manager.CompletionPending(started.ID)
		if pendingErr != nil {
			t.Fatal(pendingErr)
		}
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process did not reach a natural terminal state")
		}
		time.Sleep(10 * time.Millisecond)
	}
	readResp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"read_background","process_id":"` + started.ID + `","offset_bytes":0,"max_bytes":4096}`,
	})
	if err != nil {
		t.Fatalf("read background: %v", err)
	}
	var read struct {
		Output  string       `json:"output"`
		Status  proc.Status  `json:"status"`
		Process proc.Process `json:"process"`
	}
	if err := json.Unmarshal([]byte(readResp), &read); err != nil {
		t.Fatalf("parse read background: %v\n%s", err, readResp)
	}
	if read.Status != proc.StatusStopped || read.Process.Status != proc.StatusStopped || !strings.Contains(read.Output, "done") {
		t.Fatalf("terminal process result was not returned: %+v", read)
	}
	pending, err := manager.CompletionPending(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("terminal result returned by bash should suppress a redundant model wakeup")
	}
	processes, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	var stored proc.Process
	for _, candidate := range processes {
		if candidate.ID == started.ID {
			stored = candidate
			break
		}
	}
	if stored.ID == "" {
		t.Fatalf("process %q not found", started.ID)
	}
	if stored.CompletionConsumedBy != "bash_result" {
		t.Fatalf("completion consumer = %q, want bash_result", stored.CompletionConsumedBy)
	}
}

func TestBashReadBackgroundPagesForwardFromExplicitOffset(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)

	started, err := manager.Start(context.Background(), proc.StartOptions{
		Command:   "printf '0123456789abcdef'; sleep 1",
		OwnerKind: proc.OwnerMainAgent,
		OwnerID:   "main",
		Lifecycle: proc.LifecycleSession,
	})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	zero := int64(0)
	if _, err := manager.ReadOutputSnapshot(context.Background(), started.ID, proc.OutputReadOptions{
		OffsetBytes: &zero,
		Wait:        2 * time.Second,
	}); err != nil {
		t.Fatalf("wait for output: %v", err)
	}

	type pageResponse struct {
		Output      string `json:"output"`
		Truncated   bool   `json:"truncated"`
		StartOffset int64  `json:"start_offset"`
		EndOffset   int64  `json:"end_offset"`
		TotalBytes  int64  `json:"total_bytes"`
	}
	readPage := func(offset int64) pageResponse {
		t.Helper()
		resp, err := kit.Execute(context.Background(), providers.ToolCall{
			Name:      "bash",
			Arguments: fmt.Sprintf(`{"action":"read_background","process_id":%q,"offset_bytes":%d,"max_bytes":5}`, started.ID, offset),
		})
		if err != nil {
			t.Fatalf("read background page at %d: %v", offset, err)
		}
		var page pageResponse
		if err := json.Unmarshal([]byte(resp), &page); err != nil {
			t.Fatalf("parse page at %d: %v\n%s", offset, err, resp)
		}
		return page
	}

	first := readPage(4)
	if first.Output != "45678" || !first.Truncated || first.StartOffset != 4 || first.EndOffset != 9 || first.TotalBytes != 16 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second := readPage(first.EndOffset)
	if second.Output != "9abcd" || !second.Truncated || second.StartOffset != 9 || second.EndOffset != 14 || second.TotalBytes != 16 {
		t.Fatalf("unexpected second page: %+v", second)
	}
	last := readPage(second.EndOffset)
	if last.Output != "ef" || last.Truncated || last.StartOffset != 14 || last.EndOffset != 16 || last.TotalBytes != 16 {
		t.Fatalf("unexpected final page: %+v", last)
	}
}

func TestBashBackgroundUsesCWD(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "server")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	kit := newShellTestToolkit(t, root)
	manager, err := proc.NewManager(root, filepath.Join(t.TempDir(), "runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = manager.CleanupSession() }()
	kit.SetProcessManager(manager)
	kit.SetSessionID("thread-bash-background-cwd")

	resp, err := kit.Execute(context.Background(), providers.ToolCall{
		Name:      "bash",
		Arguments: `{"action":"start_background","command":"pwd -P; sleep 5","cwd":"server","wait_ms":500,"max_bytes":4096}`,
	})
	if err != nil {
		t.Fatalf("start background: %v", err)
	}
	var started startProcessResponse
	if err := json.Unmarshal([]byte(resp), &started); err != nil {
		t.Fatalf("parse start background: %v\n%s", err, resp)
	}
	defer func() {
		_, _ = kit.Execute(context.Background(), providers.ToolCall{Name: "bash", Arguments: `{"action":"stop_background","process_id":"` + started.ID + `"}`})
	}()
	want := canonicalTestPath(t, subdir)
	got := canonicalTestPath(t, started.CWD)
	if got != want {
		t.Fatalf("background cwd = %q, want %q", got, want)
	}
	if !strings.Contains(started.InitialOutput, want) {
		t.Fatalf("initial output should include background cwd %q: %+v", want, started)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		path = evaluated
	}
	return filepath.Clean(path)
}
