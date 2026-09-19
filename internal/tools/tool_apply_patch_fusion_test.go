package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func fusionCall(t *testing.T, patch string, next map[string]any) providers.ToolCall {
	t.Helper()
	args, err := json.Marshal(map[string]any{"patchText": patch, "then_run": next})
	if err != nil {
		t.Fatal(err)
	}
	return providers.ToolCall{ID: "fused", Name: "apply_patch", Arguments: string(args)}
}

func fusionRuntime(t *testing.T, kit *Toolkit) (*agent.TurnToolRuntime, *toolledger.Ledger) {
	t.Helper()
	kit.SetEditToolMode(EditToolModePatch)
	kit.SetSessionDir(t.TempDir())
	ledger, err := toolledger.New(kit.env.SessionDir, "fusion-test")
	if err != nil {
		t.Fatal(err)
	}
	// A single slot catches a fused parent accidentally occupying a leaf slot
	// while waiting for its own child.
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{
		Executor: kit, Ledger: ledger, OperationID: "fusion-operation", Gate: agent.NewToolExecutionGate(1),
	})
	t.Cleanup(runtime.Cancel)
	return runtime, ledger
}

func runFusion(t *testing.T, runtime *agent.TurnToolRuntime, call providers.ToolCall) providers.ChatMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
	if err != nil || len(messages) != 1 || messages[0].ToolResult == nil {
		t.Fatalf("fusion did not return one provider result: %+v, %v", messages, err)
	}
	return messages[0]
}

func fusionCommand(t *testing.T, result *toolresult.Result) (string, toolresult.Result) {
	t.Helper()
	var details struct {
		Files   []json.RawMessage `json:"files"`
		ThenRun struct {
			Status string            `json:"status"`
			Result toolresult.Result `json:"result"`
		} `json:"then_run"`
	}
	if err := json.Unmarshal(result.StructuredContent, &details); err != nil || len(details.Files) == 0 {
		t.Fatalf("fusion lost patch details: %s, %v; result: %s", result.StructuredContent, err, result.TextProjection())
	}
	return details.ThenRun.Status, details.ThenRun.Result
}

func TestActionFusionRunsVerificationAfterWholePatchAndRecordsChildren(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "pkg/go.mod"), "module fusiontest\n\ngo 1.22\n")
	mustWriteFile(t, filepath.Join(root, "pkg/value.go"), "package fusiontest\nfunc Value() int { return 1 }\n")
	mustWriteFile(t, filepath.Join(root, "pkg/value_test.go"), `package fusiontest
import ("os"; "testing")
func TestValue(t *testing.T) {
 if Value() != 2 { t.Fatal("old implementation") }
 data, err := os.ReadFile("ready.txt")
 if err != nil || string(data) != "ready\n" { t.Fatal("incomplete patch", err) }
}
`)
	kit := newShellTestToolkit(t, root)
	runtime, ledger := fusionRuntime(t, kit)
	call := fusionCall(t, "*** Begin Patch\n*** Update File: pkg/value.go\n@@\n-func Value() int { return 1 }\n+func Value() int { return 2 }\n*** Add File: pkg/ready.txt\n+ready\n*** End Patch", map[string]any{
		"command": "go test ./...", "cwd": "pkg", "scope": "affected", "timeout_seconds": 45,
	})
	message := runFusion(t, runtime, call)
	if message.ToolResult.IsError {
		t.Fatal(message.Content)
	}
	status, command := fusionCommand(t, message.ToolResult)
	var outcome shellExecutionResult
	if err := json.Unmarshal([]byte(command.TextProjection()), &outcome); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || outcome.Verification == nil || !outcome.Verification.Passed || outcome.Verification.Scope != "affected" {
		t.Fatalf("verification did not run against complete patch: %s, %+v", status, outcome)
	}
	log, err := os.ReadFile(outcome.FullLogRef)
	if err != nil || sha256Hex(log) != outcome.FullLogSHA256 {
		t.Fatalf("verification lost recoverable log: %+v, %v", outcome, err)
	}
	// Recollecting the same provider call cannot replay either mutation or test.
	if replay := runFusion(t, runtime, call); replay.Content != message.Content {
		t.Fatal("recollecting a settled fusion changed its result")
	}
	checks := 0
	for _, record := range kit.ToolTelemetry() {
		if record.Name == "bash" {
			checks++
		}
	}
	if checks != 1 {
		t.Fatalf("verification executed %d times", checks)
	}
	pending, err := ledger.PendingProjection(context.Background())
	if err != nil || len(pending) != 1 || pending[0].ProviderCallID != call.ID {
		t.Fatalf("nested calls leaked into provider recovery: %+v, %v", pending, err)
	}
	db, err := session.OpenStore(kit.env.SessionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var children int
	if err := db.QueryRow(`SELECT count(*) FROM tool_invocations WHERE parent_invocation_id = ? AND state = 'succeeded'`, message.ToolInvocationID).Scan(&children); err != nil || children != 2 {
		t.Fatalf("patch and command not independently settled: %d, %v", children, err)
	}
}

func TestActionFusionFailedPatchDoesNotRunCommandOrPartiallyWrite(t *testing.T) {
	for _, tt := range []struct {
		name     string
		sections string
	}{
		{"stale anchor", "*** Update File: existing.txt\n@@\n-stale\n+changed\n"},
		{"duplicate target", "*** Update File: existing.txt\n@@\n-original\n+changed\n*** Update File: ./existing.txt\n@@\n-original\n+changed again\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			mustWriteFile(t, filepath.Join(root, "existing.txt"), "original\n")
			kit := newShellTestToolkit(t, root)
			runtime, _ := fusionRuntime(t, kit)
			patch := "*** Begin Patch\n*** Add File: new.txt\n+new\n" + tt.sections + "*** End Patch"
			message := runFusion(t, runtime, fusionCall(t, patch, map[string]any{"command": "printf ran > ran.txt"}))
			if !message.ToolResult.IsError {
				t.Fatal("invalid patch reported success")
			}
			for _, path := range []string{"new.txt", "ran.txt"} {
				if _, err := os.Stat(filepath.Join(root, path)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed patch produced %s: %v", path, err)
				}
			}
			if got := mustReadFile(t, filepath.Join(root, "existing.txt")); got != "original\n" {
				t.Fatalf("invalid patch changed existing file: %q", got)
			}
			for _, record := range kit.ToolTelemetry() {
				if record.Name == "bash" {
					t.Fatal("command was attempted after a failed patch")
				}
			}
		})
	}
}

type fusionDenyBash struct{}

func (fusionDenyBash) Check(_ ToolInfo, call providers.ToolCall) error {
	if call.Name == "bash" {
		return errors.New("test policy denies bash")
	}
	return nil
}

func TestActionFusionCommandFailureKeepsPatch(t *testing.T) {
	for _, mode := range []string{"nonzero", "disabled", "denied", "outside-cwd"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			kit := newShellTestToolkit(t, root)
			runtime, _ := fusionRuntime(t, kit)
			next := map[string]any{"command": "i=0; while [ \"$i\" -lt 2000 ]; do printf 'diagnostic %s\\n' \"$i\"; i=$((i+1)); done; exit 7"}
			switch mode {
			case "disabled":
				kit.DisableTools("bash")
			case "denied":
				boundary := StandardBoundary()
				boundary.Guards = []Guard{fusionDenyBash{}}
				kit.SetBoundary(boundary)
			case "outside-cwd":
				kit.SetBoundary(StandardBoundary())
				next["cwd"] = t.TempDir()
			}
			message := runFusion(t, runtime, fusionCall(t, "*** Begin Patch\n*** Add File: retained.txt\n+retained\n*** End Patch", next))
			status, command := fusionCommand(t, message.ToolResult)
			if !message.ToolResult.IsError || status != "failed" {
				t.Fatalf("failed command reported success: %s", message.Content)
			}
			if got := mustReadFile(t, filepath.Join(root, "retained.txt")); got != "retained\n" {
				t.Fatalf("failed command rolled back patch: %q", got)
			}
			if mode == "nonzero" {
				var outcome shellExecutionResult
				if err := json.Unmarshal([]byte(command.TextProjection()), &outcome); err != nil || outcome.ExitCode != 7 || !strings.Contains(outcome.StdoutTail, "diagnostic") {
					t.Fatalf("lost command failure evidence: %s, %v", command.TextProjection(), err)
				}
				log, err := os.ReadFile(outcome.FullLogRef)
				if err != nil || !strings.Contains(string(log), "diagnostic 1000\n") || sha256Hex(log) != outcome.FullLogSHA256 {
					t.Fatalf("large fused result lost omitted evidence: %v", err)
				}
			} else if !command.IsError {
				t.Fatalf("follow-up bypassed %s restriction: %s", mode, command.TextProjection())
			}
		})
	}
}

func TestActionFusionRejectsInvalidInputBeforeMutation(t *testing.T) {
	for _, raw := range []string{
		`{"then_run":{}}`,
		`{"then_run":{"command":"true","timeout_seconds":0}}`,
		`{"then_run":{"command":"true","timeout_seconds":3601}}`,
		`{"then_run":{"command":"true","scope":"unknown"}}`,
		`{"then_run":{"command":"true","action":"start_background"}}`,
		`{"then_run":{"command":"true"},"dry_run":true}`,
		`{"then_run":{"command":"true"},"dryRun":true}`,
	} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			kit, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			runtime, _ := fusionRuntime(t, kit)
			var input map[string]any
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				t.Fatal(err)
			}
			input["patchText"] = "*** Begin Patch\n*** Add File: unexpected.txt\n+changed\n*** End Patch"
			arguments, _ := json.Marshal(input)
			message := runFusion(t, runtime, providers.ToolCall{ID: "invalid-fusion", Name: "apply_patch", Arguments: string(arguments)})
			if !message.ToolResult.IsError {
				t.Fatal("invalid fusion was accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "unexpected.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid fusion mutated workspace: %v", err)
			}
		})
	}
}

func TestActionFusionReadOnlyBoundaryPreventsBothActions(t *testing.T) {
	root := t.TempDir()
	kit := newShellTestToolkit(t, root)
	runtime, _ := fusionRuntime(t, kit)
	kit.SetBoundary(ReadOnlyBoundary())
	message := runFusion(t, runtime, fusionCall(t, "*** Begin Patch\n*** Add File: denied.txt\n+changed\n*** End Patch", map[string]any{"command": "printf ran > ran.txt"}))
	if !message.ToolResult.IsError {
		t.Fatal("read-only fusion was accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only fusion wrote files: %+v, %v", entries, err)
	}
}

type fusionNestedFunc func(context.Context, providers.ToolCall) (toolresult.Result, error)

func (f fusionNestedFunc) Invoke(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	return f(ctx, call)
}

func TestActionFusionPreservesPatchWhenFollowUpIsCancelled(t *testing.T) {
	root := t.TempDir()
	kit, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewApplyPatchTool(kit.env)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := fusionNestedFunc(func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
		if call.Name == "bash" {
			return toolresult.Result{}, ctx.Err()
		}
		result, err := tool.ExecuteResult(ctx, call.Arguments)
		cancel()
		return result, err
	})
	call := fusionCall(t, "*** Begin Patch\n*** Add File: retained.txt\n+retained\n*** End Patch", map[string]any{"command": "true"})
	result, err := tool.ExecuteResult(toolctx.WithNestedExecutor(ctx, executor), call.Arguments)
	if err != nil || !result.IsError {
		t.Fatalf("cancelled follow-up lost combined result: %+v, %v", result, err)
	}
	status, command := fusionCommand(t, &result)
	if status != "failed" || !command.IsError || mustReadFile(t, filepath.Join(root, "retained.txt")) != "retained\n" {
		t.Fatalf("cancelled command lost successful mutation: %s", result.TextProjection())
	}
}
