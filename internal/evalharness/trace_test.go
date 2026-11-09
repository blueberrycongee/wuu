package evalharness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayTraceSummarizesRecordedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace", "eval-trace.jsonl")
	err := WriteTrace(path, Result{
		TaskID:             "task-1",
		TaskName:           "Task One",
		Success:            true,
		VerificationReason: "passed",
		Observability: &Observability{
			SessionID:          "eval-task-1",
			SessionDir:         filepath.Dir(path),
			TracePath:          path,
			FinalAnswerPreview: "done",
			ModelProfile:       &ModelProfileObservation{ProviderName: "openai", Model: "gpt-5-codex", Family: "codex", DefaultWriteMode: "patch"},
			ContextBlocks: []ContextBlockObservation{{
				Kind:           "TASK",
				Title:          "Task",
				Source:         "system_reminder",
				TokenBudget:    600,
				ContentPreview: "Fix the task.",
			}},
			ToolInventory: []ToolInventoryObservation{{
				Name:     "read_file",
				Kind:     "file",
				Exposure: "direct",
				Risk:     "low",
				ReadOnly: true,
			}},
			ToolRecords: []ToolObservation{{
				Name:            "read_file",
				ArgumentsSHA256: strings.Repeat("a", 64),
				Kind:            "file",
				Risk:            "low",
				PolicyAction:    "allow",
				Success:         true,
			}, {
				Name:            "read_file",
				ArgumentsSHA256: strings.Repeat("a", 64),
				Kind:            "file",
				Risk:            "low",
				PolicyAction:    "allow",
				Success:         true,
			}, {
				Name:         "apply_patch",
				ResultAction: "apply",
				Kind:         "file",
				Risk:         "high",
				PolicyAction: "allow",
				Success:      true,
				PatchRiskSummary: &PatchRiskObservation{
					FileCount:    2,
					HunkCount:    2,
					AddedLines:   8,
					DeletedLines: 3,
					MultiFile:    true,
					RiskLevel:    "medium",
				},
			}, {
				Name:            "run_shell",
				ResultAction:    "restore",
				CallID:          "call-shell",
				Kind:            "shell",
				Risk:            "high",
				PolicyAction:    "deny",
				PolicyReason:    "workspace boundary",
				ErrorKind:       "boundary_denied",
				ArgumentsSHA256: strings.Repeat("c", 64),
				RevisionBefore:  "rev-before",
				Success:         false,
			}, {
				Name:            "start_process",
				CallID:          "call-process",
				Kind:            "process",
				Risk:            "high",
				PolicyAction:    "deny",
				PolicyReason:    "workspace boundary",
				ErrorKind:       "boundary_denied",
				ArgumentsSHA256: strings.Repeat("d", 64),
				RevisionBefore:  "rev-before",
				Success:         false,
			}},
			Attention: []AttentionObservation{{
				Source:  "harness_report",
				ID:      "report-1",
				Status:  "partial",
				Message: "tests failed",
				Path:    "shared.go",
			}},
			HarnessTasks:   []HarnessTaskObservation{{ID: "worker-1", Status: "completed"}},
			HarnessReports: []HarnessReportObservation{{ID: "report-1", Outcome: "completed"}},
		},
	})
	if err != nil {
		t.Fatalf("WriteTrace: %v", err)
	}

	summary, err := ReplayTrace(path)
	if err != nil {
		t.Fatalf("ReplayTrace: %v", err)
	}
	if !summary.Complete || summary.Mode != "deterministic_trace_replay" || summary.EventCount != 10 {
		t.Fatalf("unexpected replay summary envelope: %+v", summary)
	}
	if summary.Task == nil || summary.Task.ID != "task-1" || summary.Final == nil || !summary.Final.Success {
		t.Fatalf("replay did not preserve task/final outcome: %+v", summary)
	}
	if summary.ModelProfile == nil || summary.ModelProfile.Family != "codex" || summary.ModelProfile.DefaultWriteMode != "patch" {
		t.Fatalf("replay missing model profile: %+v", summary.ModelProfile)
	}
	if len(summary.ContextBlockKinds) != 1 || summary.ContextBlockKinds[0] != "TASK" {
		t.Fatalf("replay missing context block kinds: %+v", summary.ContextBlockKinds)
	}
	if len(summary.ContextBlocks) != 1 ||
		summary.ContextBlocks[0].Kind != "TASK" ||
		summary.ContextBlocks[0].Source != "system_reminder" ||
		summary.ContextBlocks[0].TokenBudget != 600 ||
		summary.ContextBlocks[0].ContentPreview != "Fix the task." {
		t.Fatalf("replay missing context block observations: %+v", summary.ContextBlocks)
	}
	if len(summary.ToolInventory) != 1 || summary.ToolInventory[0].Name != "read_file" || summary.ToolInventory[0].Exposure != "direct" {
		t.Fatalf("replay missing tool inventory: %+v", summary.ToolInventory)
	}
	if len(summary.ToolNames) != 5 || summary.ToolNames[0] != "read_file" || summary.ToolNames[1] != "read_file" || summary.ToolNames[2] != "apply_patch" || summary.ToolNames[3] != "run_shell" || summary.ToolNames[4] != "start_process" {
		t.Fatalf("replay missing tool records: %+v", summary.ToolNames)
	}
	if summary.ToolSummary == nil || summary.ToolSummary.Total != 5 || summary.ToolSummary.Succeeded != 3 || summary.ToolSummary.Failed != 2 {
		t.Fatalf("replay missing tool summary: %+v", summary.ToolSummary)
	}
	if summary.ToolSummary.ByKind["file"] != 3 ||
		summary.ToolSummary.ByRisk["high"] != 3 ||
		summary.ToolSummary.ByPolicyAction["deny"] != 2 ||
		summary.ToolSummary.ByResultAction["apply_patch:apply"] != 1 ||
		summary.ToolSummary.ByResultAction["run_shell:restore"] != 1 ||
		summary.ToolSummary.ByErrorKind["boundary_denied"] != 2 {
		t.Fatalf("replay tool summary missing dimensions: %+v", summary.ToolSummary)
	}
	if len(summary.ToolSummary.PolicyBlocks) != 2 {
		t.Fatalf("replay tool summary missing policy blocks: %+v", summary.ToolSummary.PolicyBlocks)
	}
	if block := summary.ToolSummary.PolicyBlocks[0]; block.ToolName != "run_shell" ||
		block.CallID != "call-shell" ||
		block.PolicyAction != "deny" ||
		block.PolicyReason != "workspace boundary" ||
		block.ErrorKind != "boundary_denied" ||
		block.ArgumentsSHA256 != strings.Repeat("c", 64) ||
		block.RevisionBefore != "rev-before" ||
		!strings.Contains(block.ModelNextAction, "workspace boundary") {
		t.Fatalf("deny policy block missing replay detail: %+v", block)
	}
	if block := summary.ToolSummary.PolicyBlocks[1]; block.ToolName != "start_process" ||
		block.PolicyAction != "deny" ||
		block.ErrorKind != "boundary_denied" ||
		!strings.Contains(block.ModelNextAction, "workspace boundary") {
		t.Fatalf("boundary policy block missing replay detail: %+v", block)
	}
	if len(summary.ToolSummary.RepeatedArguments) != 1 ||
		summary.ToolSummary.RepeatedArguments[0].ToolName != "read_file" ||
		summary.ToolSummary.RepeatedArguments[0].ArgumentsSHA256 != strings.Repeat("a", 64) ||
		summary.ToolSummary.RepeatedArguments[0].Count != 2 {
		t.Fatalf("replay tool summary missing repeated arguments: %+v", summary.ToolSummary.RepeatedArguments)
	}
	if summary.ToolSummary.PatchRisk == nil ||
		summary.ToolSummary.PatchRisk.Total != 1 ||
		summary.ToolSummary.PatchRisk.ByLevel["medium"] != 1 ||
		summary.ToolSummary.PatchRisk.MultiFile != 1 ||
		summary.ToolSummary.PatchRisk.FileCount != 2 ||
		summary.ToolSummary.PatchRisk.HunkCount != 2 ||
		summary.ToolSummary.PatchRisk.AddedLines != 8 ||
		summary.ToolSummary.PatchRisk.DeletedLines != 3 {
		t.Fatalf("replay tool summary missing patch risk: %+v", summary.ToolSummary.PatchRisk)
	}
	if len(summary.Attention) != 1 ||
		summary.Attention[0].Source != "harness_report" ||
		summary.Attention[0].Path != "shared.go" {
		t.Fatalf("replay missing attention: %+v", summary.Attention)
	}
}

func TestReplayTraceBuildsModelProfileRecommendationsFromEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace", "eval-trace.jsonl")
	start := time.Unix(100, 0).UTC()
	err := WriteTrace(path, Result{
		TaskID:   "task-1",
		TaskName: "Task One",
		Success:  true,
		Observability: &Observability{
			ModelProfile: &ModelProfileObservation{
				ProviderName:          "portable",
				Model:                 "coder",
				AllowParallelReadOnly: false,
			},
			ToolRecords: []ToolObservation{{
				Name:            "read_file",
				ReadOnly:        true,
				ConcurrencySafe: true,
				StartedAt:       start,
				DurationMS:      200,
				Success:         true,
			}, {
				Name:            "grep",
				ReadOnly:        true,
				ConcurrencySafe: true,
				StartedAt:       start.Add(50 * time.Millisecond),
				DurationMS:      100,
				Success:         true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("WriteTrace: %v", err)
	}

	summary, err := ReplayTrace(path)
	if err != nil {
		t.Fatalf("ReplayTrace: %v", err)
	}
	if len(summary.ModelProfileRecommendations) != 1 {
		t.Fatalf("expected one profile recommendation, got %+v", summary.ModelProfileRecommendations)
	}
	rec := summary.ModelProfileRecommendations[0]
	if rec.Field != "execution.allow_parallel_read_only" ||
		rec.CurrentValue != "false" ||
		rec.RecommendedValue != "true" ||
		len(rec.Evidence) == 0 ||
		!strings.Contains(rec.Evidence[0], "read_file overlaps grep") {
		t.Fatalf("unexpected profile recommendation: %+v", rec)
	}
}

func TestReplayTraceSummarizesValidationLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace", "eval-trace.jsonl")
	err := WriteTrace(path, Result{
		TaskID:   "task-1",
		TaskName: "Task One",
		Success:  true,
		VerificationEvidence: []VerificationEvidence{{
			Check:    "go tests",
			Passed:   true,
			Command:  "go test ./...",
			Expected: "exit_code=0",
			Observed: "ok",
		}},
		Observability: &Observability{
			ToolRecords: []ToolObservation{{
				Name:                 "bash",
				CallID:               "call-test",
				ResultAction:         "run",
				ClassificationReason: "local verification command",
				Success:              true,
				DurationMS:           1234,
				RevisionBefore:       "rev-before",
				RevisionAfter:        "rev-after",
				ResultRef:            "/tmp/wuu/tool-results/test.log",
				ArtifactRefs:         []string{"/tmp/wuu/tool-results/test.log"},
			}, {
				Name:         "status_probe",
				CallID:       "call-status",
				ResultAction: "status",
				Success:      true,
			}, {
				Name:                 "bash",
				CallID:               "call-shell",
				ClassificationReason: "simple read-only shell command",
				Success:              false,
				ErrorKind:            "policy_denied",
				PolicyAction:         "deny",
			}},
		},
	})
	if err != nil {
		t.Fatalf("WriteTrace: %v", err)
	}

	summary, err := ReplayTrace(path)
	if err != nil {
		t.Fatalf("ReplayTrace: %v", err)
	}
	if summary.Validation == nil {
		t.Fatal("replay missing validation ledger")
	}
	if summary.Validation.Status != "passed" || len(summary.Validation.Evidence) != 1 || len(summary.Validation.ToolCalls) != 1 {
		t.Fatalf("unexpected validation ledger: %+v", summary.Validation)
	}
	if summary.Validation.Evidence[0].Command != "go test ./..." {
		t.Fatalf("validation evidence command not preserved: %+v", summary.Validation.Evidence)
	}
	if summary.Validation.ToolCalls[0].ToolName != "bash" ||
		summary.Validation.ToolCalls[0].ResultRef != "/tmp/wuu/tool-results/test.log" ||
		summary.Validation.ToolCalls[0].RevisionAfter != "rev-after" {
		t.Fatalf("validation tool call missing metadata: %+v", summary.Validation.ToolCalls)
	}
	for _, call := range summary.Validation.ToolCalls {
		if call.CallID == "call-status" {
			t.Fatalf("ordinary status probes should not be treated as validation: %+v", summary.Validation.ToolCalls)
		}
		if call.CallID == "call-shell" {
			t.Fatalf("generic bash should not be treated as validation without structured validation metadata: %+v", summary.Validation.ToolCalls)
		}
	}
	if len(summary.Validation.NextActions) == 0 {
		t.Fatalf("validation ledger missing next actions: %+v", summary.Validation)
	}
}

func TestBuildValidationSummaryFromEvalResult(t *testing.T) {
	summary := BuildValidationSummary(Result{
		TaskID:             "task-1",
		TaskName:           "Task One",
		Success:            false,
		ForbiddenToolsUsed: []string{"deprecated_tool"},
		VerificationEvidence: []VerificationEvidence{{
			Check:    "marker",
			Passed:   false,
			Path:     "marker.txt",
			Observed: "missing",
		}},
		Observability: &Observability{
			Attention: []AttentionObservation{{
				Source:  "harness_report",
				ID:      "report-1",
				Status:  "partial",
				Message: "tests failed",
			}},
			ToolRecords: []ToolObservation{{
				Name:                 "bash",
				CallID:               "call-test",
				ResultAction:         "run",
				ClassificationReason: "local verification command",
				Success:              false,
				ErrorKind:            "test_failed",
				ResultRef:            "/tmp/wuu/test.log",
			}},
		},
	})
	if summary == nil {
		t.Fatal("missing validation summary")
	}
	if summary.Status != "incomplete" {
		t.Fatalf("validation status = %q, want incomplete: %+v", summary.Status, summary)
	}
	if len(summary.Missing) != 2 ||
		summary.Missing[0] != "forbidden_tool:deprecated_tool" ||
		summary.Missing[1] != "attention_issue:harness_report:report-1:status=partial" {
		t.Fatalf("validation missing requirements not summarized: %+v", summary.Missing)
	}
	if len(summary.Failures) != 2 ||
		summary.Failures[0] != "bash:test_failed:call_id=call-test" ||
		summary.Failures[1] != "marker" {
		t.Fatalf("validation failures not summarized: %+v", summary.Failures)
	}
	if len(summary.ToolCalls) != 1 || summary.ToolCalls[0].ResultRef != "/tmp/wuu/test.log" {
		t.Fatalf("validation tool call not summarized: %+v", summary.ToolCalls)
	}
}

func TestAttentionValidationIssuesSummarizesAttention(t *testing.T) {
	issues := AttentionValidationIssues([]AttentionObservation{{
		Source:  "goal_approval",
		ID:      "approval-1",
		Status:  "pending",
		Message: "Approve merge",
	}, {
		Source: "harness",
		ID:     "task-1",
		Status: "failed",
	}})
	want := []string{
		"goal_approval:approval-1:status=pending",
		"harness:task-1:status=failed",
	}
	if strings.Join(issues, "\n") != strings.Join(want, "\n") {
		t.Fatalf("goal issues = %+v, want %+v", issues, want)
	}
}
