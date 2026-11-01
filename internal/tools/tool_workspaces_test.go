package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/workspaces"
)

func TestDiscoverWorkspaceAndWorkFromScratch(t *testing.T) {
	kit, scratch, project, outside := newFileScopeKit(t)
	kit.SetRegisteredWorkspaces([]workspaces.Workspace{{ID: "project", Name: "My project", Root: project}})
	clone, err := kit.CloneForRoot(scratch)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := executeFileTool(t, clone, "list_workspaces", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Workspaces []workspaces.Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Workspaces) != 1 || result.Workspaces[0].Root != project {
		t.Fatalf("catalog = %s", raw)
	}
	file := filepath.Join(result.Workspaces[0].Root, "result.txt")
	args, _ := json.Marshal(map[string]string{"path": file, "content": "done"})
	if _, err := executeFileTool(t, clone, "write_file", string(args)); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "done" {
		t.Fatalf("project write: %q %v", data, err)
	}
	if clone.RootDir() != kit.RootDir() {
		t.Fatal("working in a project moved the conversation")
	}
	args, _ = json.Marshal(map[string]string{"path": filepath.Join(outside, "result.txt"), "content": "denied"})
	if _, err := executeFileTool(t, clone, "write_file", string(args)); err == nil {
		t.Fatal("outside write allowed")
	}
	clone.SetBoundary(ReadOnlyBoundary())
	if _, err := executeFileTool(t, clone, "list_workspaces", `{}`); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]string{"path": file, "content": "denied"})
	if _, err := executeFileTool(t, clone, "write_file", string(args)); err == nil {
		t.Fatal("read-only write allowed")
	}
	clone.ConfigureExternalCapabilities()
	if _, err := executeFileTool(t, clone, "list_workspaces", `{}`); err != nil {
		t.Fatal(err)
	}
}
