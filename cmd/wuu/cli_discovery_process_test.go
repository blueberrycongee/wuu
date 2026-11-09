//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Build the real product entry point: a Go test executable cannot establish
// that the advertised path actually supports the public authoring commands.
func TestAuthoringCLIProcessDiscovery(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "Current App", "wuu-core")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build authoring CLI: %v\n%s", err, out)
	}
	oldDir := filepath.Join(root, "Old App")
	if err := os.MkdirAll(oldDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldBinary := filepath.Join(oldDir, "wuu")
	if err := os.WriteFile(oldBinary, []byte("#!/bin/sh\nprintf 'old-cli\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)
	t.Setenv("WUU_HOME", filepath.Join(root, "state"))
	t.Setenv("WUU_CLI_EXECUTABLE", oldBinary)
	t.Setenv("WUU_TEST_OLD_CLI_DIR", oldDir)
	client := executionenv.NewClient([]string{binary, "execution-worker"}, os.Environ())
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workspace := filepath.Join(root, "workspace")
	// An explicitly forwarded host hint must not override the worker's own CLI.
	if _, err := client.Call(ctx, "initialize", executionenv.Init{Version: executionenv.ProtocolVersion, Root: workspace, Session: "cli-discovery", Environment: map[string]string{"WUU_CLI_EXECUTABLE": oldBinary}}); err != nil {
		t.Fatal(err)
	}
	for _, background := range []bool{false, true} {
		name := "foreground-plugin"
		if background {
			name = "background-plugin"
		}
		t.Run(name, func(t *testing.T) {
			// The ordinary wuu command stays user-owned. Only the explicit hint
			// selects the CLI that owns this execution, including paths with spaces.
			command := `export PATH="$WUU_TEST_OLD_CLI_DIR:$PATH"; test "$(wuu --version)" = old-cli && "$WUU_CLI_EXECUTABLE" plugin create ` + name
			args, _ := json.Marshal(map[string]any{"command": command, "run_in_background": background})
			raw, err := client.Call(ctx, "execute", executionenv.ToolRequest{Call: providers.ToolCall{Name: "bash", Arguments: string(args)}, PermissionMode: "unconfined", Actor: "author"})
			if err != nil {
				t.Fatal(err)
			}
			var result toolresult.Result
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			output := result.TextProjection()
			if background {
				var started process.Process
				if err := json.Unmarshal([]byte(output), &started); err != nil || started.ID == "" {
					t.Fatalf("background start: %s (%v)", output, err)
				}
				output = ""
				offset := int64(0)
				for {
					raw, err := client.Call(ctx, "process/read", map[string]any{"id": started.ID, "options": process.OutputReadOptions{OffsetBytes: &offset, MaxBytes: 4096, Wait: time.Second}})
					if err != nil {
						t.Fatal(err)
					}
					var snapshot process.OutputSnapshot
					if err := json.Unmarshal(raw, &snapshot); err != nil {
						t.Fatal(err)
					}
					output += snapshot.Output
					offset = snapshot.EndOffset
					if snapshot.Process.Status != process.StatusRunning {
						break
					}
				}
			}
			manifest, err := os.ReadFile(filepath.Join(workspace, name, "plugin.json"))
			if err != nil || !strings.Contains(string(manifest), name) {
				t.Fatalf("current CLI did not create %s: %v\n%s", name, err, output)
			}
			t.Logf("current CLI created %s while PATH still selected old-cli", name)
		})
	}
}
