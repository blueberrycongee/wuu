package approvefor

import (
	"encoding/json"
	"os"
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

func TestHardDenyCredentialChecksActualPathsNotDocumentText(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", home)
	for _, name := range []string{"auth.json", "credentials.json"} {
		args, _ := json.Marshal(map[string]string{"path": "README.md", "content": "Document " + name})
		if reason := HardDenyReason(Request{Tool: Tool{Name: "write_file"}, CWD: filepath.Dir(home), Arguments: string(args)}); reason != "" {
			t.Fatalf("document text denied: %s", reason)
		}
		for _, path := range []string{filepath.Join(home, name), filepath.Join(".wuu", name)} {
			args, _ := json.Marshal(map[string]string{"path": path})
			if reason := HardDenyReason(Request{Tool: Tool{Name: "read_file"}, CWD: filepath.Dir(home), Arguments: string(args)}); reason == "" {
				t.Fatalf("credential path allowed: %s", path)
			}
		}
	}
}

func TestHardDenyCredentialSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	root := t.TempDir()
	backing := filepath.Join(root, "backing.json")
	if err := os.WriteFile(backing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(home, "auth.json")
	if err := os.Symlink(backing, credential); err != nil {
		t.Fatal(err)
	}
	otherCredential := filepath.Join(home, "credentials.json")
	if err := os.WriteFile(otherCredential, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.json")
	if err := os.Symlink(otherCredential, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{credential, alias} {
		args, _ := json.Marshal(map[string]string{"path": path})
		if reason := HardDenyReason(Request{Arguments: string(args)}); reason == "" {
			t.Fatalf("credential symlink allowed: %s", path)
		}
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
