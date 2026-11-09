package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/statepath"
)

func seedWorkspaceStateDir(t *testing.T, stateDir string) {
	t.Helper()
	seed := map[string]string{
		filepath.Join("sessions", "thread-1", "runtime.json"):  "{}\n",
		filepath.Join("plugin-storage", "plugin.example.json"): "{}\n",
		filepath.Join("memory", "MEMORY.md"):                   "# legacy plugin data\n",
	}
	for rel, content := range seed {
		path := filepath.Join(stateDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("seed mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("seed write %s: %v", rel, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stateDir, "scheduled_tasks.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("seed scheduled tasks: %v", err)
	}
}

func TestServerWorkspaceStateCleanupArchivesExtensionDataAndDeletesCoreState(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	const workspaceID = "removed-project-id"
	stateDir, err := statepath.WorkspaceDirByID(home, workspaceID)
	if err != nil {
		t.Fatalf("WorkspaceDirByID: %v", err)
	}
	seedWorkspaceStateDir(t, stateDir)

	payload, err := json.Marshal(map[string]any{
		"id":     "1",
		"method": MethodWorkspaceStateCleanup,
		"params": WorkspaceStateCleanupParams{WorkspaceID: workspaceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("workspace/state/cleanup: %v", err)
	}
	result := remarshal[WorkspaceStateCleanupResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])
	if !result.Removed || !result.DataArchived || result.StateDir != stateDir {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatalf("read state dir after cleanup: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != workspaceStateArchiveDirName {
		t.Fatalf("state dir should only keep %s, got %v", workspaceStateArchiveDirName, entries)
	}
	archivedLegacy := filepath.Join(stateDir, workspaceStateArchiveDirName, "memory", "MEMORY.md")
	if data, err := os.ReadFile(archivedLegacy); err != nil || string(data) != "# legacy plugin data\n" {
		t.Fatalf("archived legacy plugin data missing: err=%v data=%q", err, data)
	}
	if _, err := os.Stat(filepath.Join(stateDir, workspaceStateArchiveDirName, "plugin-storage", "plugin.example.json")); err != nil {
		t.Fatalf("plugin storage was not archived: %v", err)
	}
}

func TestServerWorkspaceStateCleanupIsRepeatableAndKeepsArchives(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	home, err := statepath.Home("")
	if err != nil {
		t.Fatalf("statepath.Home: %v", err)
	}
	const workspaceID = "re-added-project-id"
	stateDir, err := statepath.WorkspaceDirByID(home, workspaceID)
	if err != nil {
		t.Fatalf("WorkspaceDirByID: %v", err)
	}

	cleanup := func(reqID string) WorkspaceStateCleanupResult {
		payload, err := json.Marshal(map[string]any{
			"id":     reqID,
			"method": MethodWorkspaceStateCleanup,
			"params": WorkspaceStateCleanupParams{WorkspaceID: workspaceID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.handleLine(context.Background(), payload); err != nil {
			t.Fatalf("workspace/state/cleanup: %v", err)
		}
		return remarshal[WorkspaceStateCleanupResult](t, responseByID(t, parseOutput(t, out.String()), reqID)["result"])
	}

	// A missing state dir is not an error — nothing to clean.
	if result := cleanup("1"); result.Removed || result.DataArchived {
		t.Fatalf("missing state dir should be a no-op, got %+v", result)
	}

	// First real cleanup archives extension data; the second (after the project was
	// re-added and used again) must keep the earlier archive.
	seedWorkspaceStateDir(t, stateDir)
	if result := cleanup("2"); !result.Removed || !result.DataArchived {
		t.Fatalf("first cleanup should archive extension data, got %+v", result)
	}
	seedWorkspaceStateDir(t, stateDir)
	if result := cleanup("3"); !result.Removed || !result.DataArchived {
		t.Fatalf("second cleanup should archive extension data again, got %+v", result)
	}
	archiveEntries, err := os.ReadDir(filepath.Join(stateDir, workspaceStateArchiveDirName))
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	legacyArchives := 0
	for _, entry := range archiveEntries {
		// Counts one legacy directory plus timestamp-suffixed snapshots.
		if entry.Name() == "memory" || strings.HasPrefix(entry.Name(), "memory-2") {
			legacyArchives++
		}
	}
	if legacyArchives < 2 {
		t.Fatalf("both durable archives should survive, got entries %v", archiveEntries)
	}
}

func TestServerWorkspaceStateCleanupRefusesActiveWorkspace(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	payload, err := json.Marshal(map[string]any{
		"id":     "1",
		"method": MethodWorkspaceStateCleanup,
		"params": WorkspaceStateCleanupParams{WorkspacePath: rt.RootDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("workspace/state/cleanup: %v", err)
	}
	resp := responseByID(t, parseOutput(t, out.String()), "1")
	if resp["error"] == nil {
		t.Fatalf("cleaning the active workspace must fail, got %+v", resp)
	}
}
