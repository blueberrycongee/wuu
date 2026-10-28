package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Count revision attempts without timing assertions. Git failure deliberately
// selects the filesystem fallback, so each attempt reads the workspace fixture.
func countWorkspaceRevisionAttempts(t *testing.T) func() int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("revision command fixture requires a POSIX shell")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "attempts")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf 'snapshot\\n' >> \"$WUU_TEST_REVISION_ATTEMPTS\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WUU_TEST_REVISION_ATTEMPTS", log)
	t.Setenv("WUU_HOME", t.TempDir())
	return func() int {
		t.Helper()
		data, err := os.ReadFile(log)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return strings.Count(string(data), "snapshot\n")
	}
}

type revisionOwnershipTool struct {
	name     string
	readOnly bool
	execute  func(context.Context, string) (string, error)
}

func (t *revisionOwnershipTool) Name() string { return t.name }
func (t *revisionOwnershipTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.name, InputSchema: map[string]any{"type": "object"}}
}
func (t *revisionOwnershipTool) IsReadOnly() bool        { return t.readOnly }
func (t *revisionOwnershipTool) IsConcurrencySafe() bool { return t.readOnly }
func (t *revisionOwnershipTool) Execute(ctx context.Context, args string) (string, error) {
	return t.execute(ctx, args)
}
func (t *revisionOwnershipTool) Classify(args string) ToolClassification {
	readOnly := t.readOnly || args == `{"action":"observe"}`
	return ToolClassification{ReadOnly: readOnly, ConcurrencySafe: readOnly, Risk: ToolRiskLow}
}
func (t *revisionOwnershipTool) IsOrchestrator(args string) bool {
	return args == `{"action":"delegate"}`
}

func TestToolTelemetryRevisionOwnership(t *testing.T) {
	for _, tc := range []struct {
		name         string
		readOnly     bool
		args         string
		delegate     bool
		childWrites  bool
		fails        bool
		wantAttempts int
	}{
		{name: "read-only leaf", readOnly: true},
		{name: "argument-classified read-only leaf", args: `{"action":"observe"}`},
		{name: "writing leaf", wantAttempts: 2},
		{name: "failed writing leaf", fails: true, wantAttempts: 2},
		{name: "custom orchestrator reading child", delegate: true},
		{name: "custom orchestrator writing child", delegate: true, childWrites: true, wantAttempts: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := countWorkspaceRevisionAttempts(t)
			root := t.TempDir()
			fixture := filepath.Join(root, "revision-count-fixture.txt")
			mustWriteFile(t, fixture, "before")
			leaf := &revisionOwnershipTool{name: "custom_leaf", readOnly: tc.readOnly || tc.delegate && !tc.childWrites}
			leaf.execute = func(_ context.Context, args string) (string, error) {
				if !leaf.Classify(args).ReadOnly {
					if err := os.WriteFile(fixture, []byte("after"), 0600); err != nil {
						return "", err
					}
				}
				if tc.fails {
					return "", errors.New("failed after mutation")
				}
				return `{"action":"complete"}`, nil
			}
			parent := &revisionOwnershipTool{name: "custom_coordinator"}
			parent.execute = func(ctx context.Context, _ string) (string, error) {
				nested, ok := toolctx.Nested(ctx)
				if !ok {
					return "", errors.New("missing nested executor")
				}
				result, err := nested.Invoke(ctx, providers.ToolCall{ID: "child", Name: leaf.Name(), Arguments: `{}`})
				return result.TextProjection(), err
			}
			kit := &Toolkit{env: &Env{RootDir: root}, registry: NewRegistry(leaf, parent), boundary: StandardBoundary()}
			args := tc.args
			if args == "" {
				args = `{}`
			}
			call := providers.ToolCall{ID: "root", Name: leaf.Name(), Arguments: args}
			if tc.delegate {
				call.Name, call.Arguments = parent.Name(), `{"action":"delegate"}`
			}
			ctx := context.Background()
			run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
			defer run.Cancel()
			messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
			if err != nil || len(messages) != 1 || messages[0].ToolResult == nil || messages[0].ToolResult.IsError != tc.fails {
				t.Fatalf("execution: %+v %v", messages, err)
			}
			if got := attempts(); got != tc.wantAttempts {
				t.Errorf("workspace revision attempts = %d, want %d", got, tc.wantAttempts)
			}
			records := kit.ToolTelemetry()
			wantRecords := 1
			if tc.delegate {
				wantRecords = 2
			}
			if len(records) != wantRecords {
				t.Fatalf("telemetry records = %+v", records)
			}
			for _, record := range records {
				writes := record.Name == leaf.Name() && !leaf.Classify(args).ReadOnly
				if writes {
					if record.RevisionBefore == "" || record.RevisionAfter == "" || record.RevisionBefore == record.RevisionAfter || record.Success == tc.fails {
						t.Errorf("mutation evidence lost: %+v", record)
					}
				} else if record.RevisionBefore != "" || record.RevisionAfter != "" {
					t.Errorf("non-mutating owner acquired workspace snapshots: %+v", record)
				}
			}
		})
	}
}

func TestToolTelemetryListAndRunCodeKeepLeafEvidence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		code         string
		wantAttempts int
		writes       bool
	}{
		{name: "direct listing", wantAttempts: 1},
		{name: "orchestrated listing", code: `text(await tools.list_files({path:"."}));`, wantAttempts: 1},
		{name: "orchestrated write", code: `text(await tools.write_file({path:"created.txt",content:"written"}));`, wantAttempts: 3, writes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := countWorkspaceRevisionAttempts(t)
			var kit *Toolkit
			var result toolresult.Result
			if tc.code == "" {
				var err error
				kit, err = New(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				mustWriteFile(t, filepath.Join(kit.RootDir(), "revision-count-fixture.txt"), "before")
				result, err = kit.ExecuteResult(context.Background(), providers.ToolCall{ID: "listing", Name: "list_files", Arguments: `{"path":"."}`})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				kit = newCodeModeTestToolkit(t)
				mustWriteFile(t, filepath.Join(kit.RootDir(), "revision-count-fixture.txt"), "before")
				result = runPTCProgram(t, kit, tc.code)
			}
			if result.IsError || !strings.Contains(result.TextProjection(), "workspace_revision") {
				t.Fatalf("leaf revision receipt lost: %+v", result)
			}
			if got := attempts(); got != tc.wantAttempts {
				t.Errorf("workspace revision attempts = %d, want %d", got, tc.wantAttempts)
			}
			for _, record := range kit.ToolTelemetry() {
				if record.Name == "write_file" && (record.RevisionBefore == "" || record.RevisionAfter == "" || record.RevisionBefore == record.RevisionAfter) {
					t.Errorf("writing child lost mutation evidence: %+v", record)
				}
			}
			if tc.writes && mustReadFile(t, filepath.Join(kit.RootDir(), "created.txt")) != "written" {
				t.Fatal("writing child did not complete")
			}
		})
	}
}

type revisionEnvironment struct{ calls int }

func (e *revisionEnvironment) Execute(_ context.Context, _ executionenv.ToolRequest) (toolresult.Result, error) {
	e.calls++
	return toolresult.FromText(`{"workspace_revision":"provider-owned-revision"}`), nil
}

func TestToolTelemetryLeavesRemoteWorkspaceEvidenceWithProvider(t *testing.T) {
	attempts := countWorkspaceRevisionAttempts(t)
	kit, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := &revisionEnvironment{}
	kit.SetExecutionEnvironment(provider)
	for _, name := range []string{"list_files", "write_file"} {
		result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: name, Arguments: `{"path":"remote.txt","content":"remote write"}`})
		if err != nil || !strings.Contains(result.TextProjection(), "provider-owned-revision") {
			t.Fatalf("selected environment evidence changed: %+v %v", result, err)
		}
	}
	if provider.calls != 2 || attempts() != 0 {
		t.Fatalf("provider calls=%d, host snapshots=%d", provider.calls, attempts())
	}
}

func TestToolTelemetryRejectedCallsDoNotReadWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		deny bool
	}{
		{name: "invalid JSON", args: `[`},
		{name: "invalid input", args: `{}`},
		{name: "denied write", args: `{"path":"created.txt","content":"blocked"}`, deny: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := countWorkspaceRevisionAttempts(t)
			kit, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			mustWriteFile(t, filepath.Join(kit.RootDir(), "revision-count-fixture.txt"), "before")
			if tc.deny {
				kit.SetAuthorizer(&recordingAuthorizer{decision: AuthorizationDecision{Outcome: "deny", Reason: "fixture policy"}})
			}
			if _, err := kit.ExecuteResult(context.Background(), providers.ToolCall{ID: "rejected", Name: "write_file", Arguments: tc.args}); err == nil {
				t.Fatal("rejected call executed")
			}
			if got := attempts(); got != 0 {
				t.Errorf("rejected call read workspace: %d revision attempts", got)
			}
			records := kit.ToolTelemetry()
			if len(records) != 1 || records[0].Success || records[0].Error == "" || records[0].CallID != "rejected" || tc.deny && records[0].PolicyAction != ToolPolicyDeny {
				t.Fatalf("rejection audit lost: %+v", records)
			}
			if _, err := os.Stat(filepath.Join(kit.RootDir(), "created.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected call changed workspace: %v", err)
			}
		})
	}
}
