package agentthread

import "testing"

func TestAgentPathJoinAndResolve(t *testing.T) {
	root := RootAgentPath()
	child, err := JoinAgentPath(root, "researcher")
	if err != nil {
		t.Fatalf("JoinAgentPath: %v", err)
	}
	if child != "/root/researcher" {
		t.Fatalf("unexpected child path %q", child)
	}
	nested, err := ResolveAgentPath(child, "fix_bug")
	if err != nil {
		t.Fatalf("ResolveAgentPath relative: %v", err)
	}
	if nested != "/root/researcher/fix_bug" {
		t.Fatalf("unexpected nested path %q", nested)
	}
	absolute, err := ResolveAgentPath(child, "/root/other")
	if err != nil {
		t.Fatalf("ResolveAgentPath absolute: %v", err)
	}
	if absolute != "/root/other" {
		t.Fatalf("unexpected absolute path %q", absolute)
	}
}

func TestAgentPathRejectsNonCodexNames(t *testing.T) {
	for _, name := range []string{"", "root", ".", "..", "FixBug", "fix-bug", "fix bug", "fix/bug"} {
		if err := ValidateAgentName(name); err == nil {
			t.Fatalf("expected %q to be invalid", name)
		}
	}
	if err := ValidateAgentName("fix_bug_2"); err != nil {
		t.Fatalf("expected valid name: %v", err)
	}
}
