package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestShellCommandBaseNameStripsExecutableExtensions(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"rm", "rm"},
		{"rm.exe", "rm"},
		{"RM.EXE", "RM"},
		{"git.cmd", "git"},
		{"build.bat", "build"},
		{"tool.com", "tool"},
		{"/usr/bin/rm", "rm"},
		{`C:\Windows\system32\cmd.exe`, "cmd"},
		{`tools\rm.exe`, "rm"},
		// The extension alone is a (weird) name, not an extension.
		{".exe", ".exe"},
		// Non-executable extensions stay.
		{"script.sh", "script.sh"},
	}
	for _, tc := range cases {
		if got := shellCommandBaseName(tc.in); got != tc.want {
			t.Errorf("shellCommandBaseName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestClassifyShellCommandCatchesExecutableExtensionDestructives(t *testing.T) {
	classification := classifyShellCommand(`rm.exe -rf build`)
	if !classification.Destructive {
		t.Fatalf("rm.exe not classified destructive: %+v", classification)
	}
}

func TestShellSensitivePathTokenReasonSeesBackslashPaths(t *testing.T) {
	if _, ok := shellSensitivePathTokenReason(`C:\Users\dev\secrets\api`); !ok {
		t.Fatal("backslash secrets path not routed to sensitive-path detection")
	}
	if _, ok := shellSensitivePathTokenReason(`..\project\.env`); !ok {
		t.Fatal("backslash .env path not detected")
	}
}

func TestRelInsideRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "repo")
	cases := []struct {
		path    string
		wantRel string
		wantOK  bool
	}{
		{filepath.Join(root, "src", "a.go"), filepath.Join("src", "a.go"), true},
		{root, ".", true},
		{filepath.Join(string(filepath.Separator), "work", "repo-evil", "x"), "", false},
		{filepath.Join(string(filepath.Separator), "work"), "", false},
	}
	for _, tc := range cases {
		rel, ok := relInsideRoot(root, tc.path)
		if ok != tc.wantOK || rel != tc.wantRel {
			t.Errorf("relInsideRoot(%q, %q) = (%q, %t), want (%q, %t)", root, tc.path, rel, ok, tc.wantRel, tc.wantOK)
		}
	}
}

func TestMergeEnvKeepsSingleEntryPerKey(t *testing.T) {
	out := mergeEnv([]string{"PATH=/bin", "HOME=/home/dev"}, map[string]string{"PAGER": "cat"})
	seen := map[string]int{}
	for _, entry := range out {
		key, _, _ := strings.Cut(entry, "=")
		seen[strings.ToUpper(key)]++
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("key %s appears %d times in %v", key, count, out)
		}
	}
	if seen["PAGER"] != 1 || seen["PATH"] != 1 {
		t.Fatalf("merged env missing expected keys: %v", out)
	}
}
