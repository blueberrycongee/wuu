package approvefor

import (
	"path/filepath"
	"testing"
)

func TestNeedsReviewSkipsOrdinaryWorkspaceReads(t *testing.T) {
	req := Request{
		PermissionMode: "standard",
		Tool:           Tool{Name: "read_file", Kind: "file", ReadOnly: true, Risk: "low"},
		Arguments:      `{"path":"README.md"}`,
	}
	if NeedsReview(req) {
		t.Fatal("ordinary workspace reads should not enter Approve for me")
	}
}

func TestNeedsReviewInspectsHighRiskShell(t *testing.T) {
	req := Request{
		PermissionMode: "standard",
		Tool:           Tool{Name: "bash", Kind: "shell", Risk: "high", Destructive: true},
		Arguments:      `{"command":"rm -rf build"}`,
	}
	if !NeedsReview(req) {
		t.Fatal("high-risk shell should enter Approve for me")
	}
}

func TestNeedsReviewStaysOffOutsideStandard(t *testing.T) {
	req := Request{
		PermissionMode: "unconfined",
		Tool:           Tool{Name: "bash", Kind: "shell", Risk: "high"},
	}
	if NeedsReview(req) {
		t.Fatal("unconfined sessions already have user-granted full authority")
	}
}

func TestHardDenyBlocksCredentialFilesAndModeEscalation(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", home)
	cred := Request{
		PermissionMode: "standard",
		Tool:           Tool{Name: "read_file", Kind: "file", ReadOnly: true},
		Arguments:      `{"path":` + jsonPath(filepath.Join(home, "auth.json")) + `}`,
	}
	if reason := HardDenyReason(cred); reason == "" {
		t.Fatal("credential files must stay unreviewable")
	}
	escalation := Request{
		PermissionMode: "standard",
		Tool:           Tool{Name: "bash", Kind: "shell", Risk: "high"},
		Arguments:      `{"command":"echo permission_mode=unconfined"}`,
	}
	if reason := HardDenyReason(escalation); reason == "" {
		t.Fatal("permission-mode escalation must stay unreviewable")
	}
}

func jsonPath(path string) string {
	return `"` + filepath.ToSlash(path) + `"`
}
