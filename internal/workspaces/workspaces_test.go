package workspaces

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListMissingFileReturnsEmpty(t *testing.T) {
	list, err := List(t.TempDir())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no workspaces, got %+v", list)
	}
}

func TestListReadsProjectsStore(t *testing.T) {
	home := t.TempDir()
	store := `{"projects":[{"id":"a","name":"wuu","path":"/repos/wuu"},{"id":"b","name":"  ","path":"/repos/other"},{"id":"c","name":"empty","path":"  "}],"active_context":{"kind":"project"}}`
	if err := os.WriteFile(filepath.Join(home, "projects.json"), []byte(store), 0o644); err != nil {
		t.Fatalf("write projects.json: %v", err)
	}
	list, err := List(home)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 workspaces (empty path dropped), got %+v", list)
	}
	if list[0].Name != "wuu" || list[0].Root != "/repos/wuu" {
		t.Fatalf("first workspace = %+v", list[0])
	}
	if roots := Roots(list); len(roots) != 2 || roots[0] != "/repos/wuu" || roots[1] != "/repos/other" {
		t.Fatalf("Roots = %+v", roots)
	}
}

func TestListEmptyHome(t *testing.T) {
	list, err := List("  ")
	if err != nil || list != nil {
		t.Fatalf("empty home should be empty, got %+v err=%v", list, err)
	}
}

func TestBoundaryRootsIncludesRuntimeWuuHomeWorkspacesTempAndExtras(t *testing.T) {
	home := t.TempDir()
	store := `{"projects":[{"name":"wuu","path":"/repos/wuu"},{"name":"other","path":"/repos/other"}]}`
	if err := os.WriteFile(filepath.Join(home, "projects.json"), []byte(store), 0o644); err != nil {
		t.Fatalf("write projects.json: %v", err)
	}

	roots := BoundaryRoots("/runtime/root", home, "  ", "/session/artifacts", "/memory")
	want := []string{"/runtime/root", home, "/repos/wuu", "/repos/other", os.TempDir(), "/session/artifacts", "/memory"}
	if len(roots) != len(want) {
		t.Fatalf("BoundaryRoots length = %d, want %d: %+v", len(roots), len(want), roots)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("BoundaryRoots[%d] = %q, want %q (all=%+v)", i, roots[i], want[i], roots)
		}
	}
}
