package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolExecutionRecordResultEnvelopeIsMetadataOnly(t *testing.T) {
	record := ToolExecutionRecord{
		Name:                 "run_shell",
		CallID:               "call_1",
		ArgumentsSHA256:      strings.Repeat("a", 64),
		ResultAction:         "restore",
		Kind:                 ToolKindShell,
		Exposure:             ToolExposureDirect,
		Risk:                 ToolRiskHigh,
		ClassificationReason: "destructive shell command",
		PolicyAction:         ToolPolicyAllow,
		ReadOnly:             false,
		ConcurrencySafe:      false,
		DurationMS:           42,
		RevisionBefore:       "git:before:worktree:aaa",
		RevisionAfter:        "git:after:worktree:bbb",
		Success:              false,
		Error:                "authorization: bearer secret-token error_kind=boundary_denied",
		ErrorKind:            "boundary_denied",
		RawOutputBytes:       1024,
		ReturnedOutputBytes:  256,
		ResultBudgeted:       true,
		ResultRef:            "/tmp/wuu/tool-results/call_1.txt",
		PatchRiskSummary:     &ToolPatchRisk{FileCount: 2, HunkCount: 2, RiskLevel: "medium", MultiFile: true},
	}

	envelope := record.ResultEnvelope()
	if envelope.OK || envelope.ToolCallID != "call_1" || envelope.DataRef != record.ResultRef || envelope.Revision != record.RevisionAfter {
		t.Fatalf("unexpected envelope identity: %+v", envelope)
	}
	if envelope.Summary != "run_shell failed in 42ms" {
		t.Fatalf("unexpected summary: %q", envelope.Summary)
	}
	if len(envelope.NextSuggestions) == 0 {
		t.Fatalf("expected next suggestions: %+v", envelope)
	}

	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if strings.Contains(string(raw), "secret-token") || strings.Contains(string(raw), "authorization") {
		t.Fatalf("envelope leaked raw error: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"error_present":true`) {
		t.Fatalf("envelope should retain error presence without raw error: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"error_kind":"boundary_denied"`) {
		t.Fatalf("envelope should include error kind: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"revision_before":"git:before:worktree:aaa"`) || !strings.Contains(string(raw), `"revision_after":"git:after:worktree:bbb"`) {
		t.Fatalf("envelope should include revisions: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"classification_reason":"destructive shell command"`) {
		t.Fatalf("envelope should include classification reason: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"arguments_sha256":"`+strings.Repeat("a", 64)+`"`) {
		t.Fatalf("envelope should include argument fingerprint: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"result_action":"restore"`) {
		t.Fatalf("envelope should include result action: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"patch_risk_summary"`) || !strings.Contains(string(raw), `"risk_level":"medium"`) {
		t.Fatalf("envelope should include patch risk summary: %s", string(raw))
	}
}

func TestToolExecutionRecordResultEnvelopeMarksTruncatedWithoutRef(t *testing.T) {
	record := ToolExecutionRecord{
		Name:           "grep_repo",
		Kind:           ToolKindSearch,
		Success:        true,
		ResultBudgeted: true,
	}

	envelope := record.ResultEnvelope()
	if !envelope.Truncated {
		t.Fatalf("expected truncated envelope without result ref: %+v", envelope)
	}
	if len(envelope.Warnings) == 0 {
		t.Fatalf("expected truncation warning: %+v", envelope)
	}
}
