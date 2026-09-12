package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamedAgentWorkspaceContextTracksRegisteredProjects(t *testing.T) {
	wuuHome := t.TempDir()
	agentHome := filepath.Join(wuuHome, "channels", "agents", "agent-1")
	provider := namedAgentWorkspaceContextProvider(wuuHome, agentHome, filepath.Join(agentHome, "memory"), nil)

	initial := provider()
	if len(initial) != 1 || !strings.Contains(initial[0].Content, "Registered project workspaces: none") {
		t.Fatalf("initial workspace context = %+v", initial)
	}
	if err := os.WriteFile(filepath.Join(wuuHome, "projects.json"), []byte(`{
  "projects": [
    {"name":"Wuu","path":"/projects/wuu"},
    {"name":"Docs","path":"/projects/docs"}
  ]
}`), 0o644); err != nil {
		t.Fatalf("write projects: %v", err)
	}

	updated := provider()
	if len(updated) != 1 {
		t.Fatalf("updated workspace context = %+v", updated)
	}
	for _, want := range []string{
		"Agent home (identity/state anchor, not project scope): " + agentHome,
		"- Wuu — /projects/wuu",
		"- Docs — /projects/docs",
		"command-specific cwd",
	} {
		if !strings.Contains(updated[0].Content, want) {
			t.Fatalf("updated workspace context missing %q:\n%s", want, updated[0].Content)
		}
	}
	if initial[0].Source != updated[0].Source {
		t.Fatalf("typed block identity changed: %q != %q", initial[0].Source, updated[0].Source)
	}
}

func TestCollaborationNotebookContextRefreshesAndForgets(t *testing.T) {
	dir := t.TempDir()
	provider := namedAgentNotebookContextProvider(dir)
	path := filepath.Join(dir, "MEMORY.md")
	if err := os.WriteFile(path, []byte("- [Preference](preference.md) - Friday digest"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := provider(); len(got) != 1 || !strings.Contains(got[0].Content, "Friday") {
		t.Fatalf("initial memory = %+v", got)
	}
	if err := os.WriteFile(path, []byte("- [Preference](preference.md) - Monday digest"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := provider(); len(got) != 1 || strings.Contains(got[0].Content, "Friday") || !strings.Contains(got[0].Content, "Monday") {
		t.Fatalf("corrected memory = %+v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := provider(); len(got) != 1 || got[0].Content != "" {
		t.Fatalf("forgotten memory = %+v", got)
	}
}
