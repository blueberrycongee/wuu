package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// Runtime state is not workspace content. Hashing its live SQLite files also
// closes raw descriptors and drops the process's SQLite POSIX locks.
func TestWorkspaceRevisionExcludesConfiguredRuntimeHome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit bool
		symlink  bool
	}{
		{name: "captured environment"},
		{name: "explicit runtime home", explicit: true},
		{name: "symlink runtime home", explicit: true, symlink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "runtime-data")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			selected := home
			if tc.symlink {
				selected = filepath.Join(t.TempDir(), "runtime-home")
				if err := os.Symlink(home, selected); err != nil {
					t.Skipf("directory symlink unavailable: %v", err)
				}
			}
			t.Setenv("WUU_HOME", selected)
			if tc.explicit {
				t.Setenv("WUU_HOME", t.TempDir())
			}
			kit, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			if tc.explicit {
				kit.SetRuntimeHome(selected)
			}
			t.Setenv("WUU_HOME", t.TempDir())
			clone, err := kit.CloneForRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			workspaceFile := filepath.Join(root, "source.txt")
			stateFile := filepath.Join(home, "state.sqlite3")
			for _, path := range []string{workspaceFile, stateFile} {
				if err := os.WriteFile(path, []byte("initial"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			revision := func(kit *Toolkit) string {
				t.Helper()
				result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: `{"path":"source.txt"}`})
				if err != nil || result.IsError {
					t.Fatalf("read workspace file: %+v %v", result, err)
				}
				records := kit.ToolTelemetry()
				value := records[len(records)-1].RevisionBefore
				if value == "" {
					t.Fatal("workspace revision is unavailable")
				}
				var payload struct {
					WorkspaceRevision string `json:"workspace_revision"`
				}
				if err := json.Unmarshal([]byte(result.TextProjection()), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.WorkspaceRevision != value {
					t.Fatalf("file result revision %q differs from telemetry %q", payload.WorkspaceRevision, value)
				}
				return value
			}
			before := revision(kit)
			if err := os.WriteFile(stateFile, []byte("runtime changed"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, active := range []*Toolkit{kit, clone} {
				if after := revision(active); after != before {
					t.Fatalf("runtime state changed workspace revision: %q != %q", after, before)
				}
			}
			if err := os.WriteFile(workspaceFile, []byte("workspace changed"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, active := range []*Toolkit{kit, clone} {
				if after := revision(active); after == before {
					t.Fatalf("workspace edit was not detected: %q", after)
				}
			}
		})
	}
}
