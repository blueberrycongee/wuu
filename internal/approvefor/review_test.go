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

func TestNeedsReviewStaysOffOutsideStandard(t *testing.T) {
	req := Request{
		PermissionMode: "unconfined",
		Tool:           Tool{Name: "bash", Kind: "shell", Risk: "high"},
	}
	if NeedsReview(req) {
		t.Fatal("unconfined sessions already have user-granted full authority")
	}
}

func TestHardDenyUsesTargetsNotContent(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", home)
	credential := filepath.Join(home, "auth.json")
	tests := []struct {
		name, tool string
		args       map[string]any
		deny       bool
	}{
		{"source content", "write_file", map[string]any{"path": "review.go", "content": `permission_mode unconfined auth.json credentials.json`}, false},
		{"notes", "write_file", map[string]any{"path": "notes.md", "content": credential}, false},
		{"shell echo", "bash", map[string]any{"command": "echo permission_mode=unconfined auth.json"}, false},
		{"nested example", "write_file", map[string]any{"path": "example.json", "content": map[string]any{"permission_mode": "unconfined"}}, false},
		{"mode change", "session_update", map[string]any{"permission_mode": "unconfined"}, true},
		{"mode alias", "session_update", map[string]any{"permission-mode": "unconfined"}, true},
		{"standard mode", "session_update", map[string]any{"permission_mode": "standard", "note": "unconfined"}, false},
		{"credential read", "read_file", map[string]any{"path": credential}, true},
		{"relative credential", "read_file", map[string]any{"path": "auth.json"}, true},
		{"normalized credential", "edit_file", map[string]any{"path": "child/../credentials.json"}, true},
		{"fixture basename", "read_file", map[string]any{"path": "testdata/auth.json"}, false},
		{"credential patch", "apply_patch", map[string]any{"patchText": "*** Begin Patch\n*** Update File: " + credential + "\n@@\n-old\n+new\n*** End Patch"}, true},
		{"credential move", "apply_patch", map[string]any{"patch_text": "*** Begin Patch\n*** Update File: source.json\n*** Move to: phone.json\n@@\n-old\n+new\n*** End Patch"}, true},
		{"credential delete", "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Delete File: remote.json\n*** End Patch"}, true},
		{"sensitive patch", "apply_patch", map[string]any{"patchText": "*** Begin Patch\n*** Add File: .env\n+SECRET=value\n*** End Patch"}, true},
		{"sensitive edit", "edit_file", map[string]any{"path": ".ssh/id_ed25519"}, true},
		{"patch examples", "apply_patch", map[string]any{"patchText": "*** Begin Patch\n*** Update File: review_test.go\n@@\n+permission_mode unconfined auth.json credentials.json\n+*** Delete File: " + credential + "\n*** End Patch"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			reason := HardDenyReason(Request{Tool: Tool{Name: tt.tool}, CWD: home, Arguments: string(args)})
			if (reason != "") != tt.deny {
				t.Fatalf("HardDenyReason = %q, want denied %v", reason, tt.deny)
			}
		})
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

func TestNeedsReviewShellCannotUseReadOnlyShortcut(t *testing.T) {
	// These are command strings only; no shell runs and no credentials exist.
	home := filepath.Join(t.TempDir(), ".wuu")
	t.Setenv("WUU_HOME", home)
	for _, command := range []string{
		"cat " + filepath.Join(home, "auth.json"),
		"cat '" + filepath.Join(home, "credentials.json") + "'",
		"cat remote.json phone.json",
		"cat child/../auth.json",
		"cat auth.json",
		`cat "$WUU_HOME/auth.json"`,
		`cat ~/.wuu/auth.json`,
		`cat < auth.json`,
		`env cat auth.json`,
		`sh -c 'cat auth.json'`,
		`echo "$(cat auth.json)"`,
		"cat <<'EOF'\nauth.json\nEOF",
		`python -c 'print("auth.json")'`,
		`echo permission_mode=unconfined auth.json`,
		`cat README.md`,
		`cat 'unterminated`,
	} {
		t.Run(command, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"command": command, "cwd": home})
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range []Tool{
				{Name: "bash", Kind: "shell", ReadOnly: true, Risk: "low"},
				{Name: "bash", ReadOnly: true, Risk: "medium"},
				{Name: "other_shell", Kind: "shell", ReadOnly: true},
			} {
				req := Request{PermissionMode: "standard", Tool: tool, CWD: t.TempDir(), Arguments: string(args)}
				if !NeedsReview(req) {
					t.Fatalf("shell call bypassed review: %+v", tool)
				}
				// Programs are reviewed contextually, not hard-denied merely
				// because credential or permission-mode strings occur in them.
				if reason := HardDenyReason(req); reason != "" {
					t.Fatalf("command text caused a hard deny: %s", reason)
				}
				for _, mode := range []string{"unconfined", "read_only"} {
					req.PermissionMode = mode
					if NeedsReview(req) {
						t.Fatalf("review unexpectedly enabled in %s", mode)
					}
				}
			}
		})
	}
}
