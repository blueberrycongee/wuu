package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Failure cases: fabricated workspace ownership, lost MIME/digest/empty-file
// metadata, unreadable snapshots after reopen, and a disabled-provider fallback.
func TestFixtureImportsArtifactsThroughSelectedProvider(t *testing.T) {
	for _, workspaceID := range []string{"", "registered-project"} {
		t.Run(workspaceID, func(t *testing.T) {
			home, workdir, cfg := fixtureHome(t, persistence.JSONLModule)
			cfg.Composition = nil
			raw, _ := json.Marshal(cfg)
			if err := os.WriteFile(filepath.Join(home, "config.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			inputs := []map[string]any{}
			for name, content := range map[string]string{"review.patch": "exact immutable patch\n", "empty.txt": ""} {
				path := filepath.Join(workdir, name)
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				inputs = append(inputs, map[string]any{"thread_id": "source", "path": path, "mime_type": "text/plain"})
			}
			var imported struct {
				Artifacts []pluginhost.ArtifactImportResult `json:"artifacts"`
			}
			fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "workspace_id": workspaceID, "op": "import-artifacts", "artifacts": inputs}, &imported)
			if len(imported.Artifacts) != len(inputs) {
				t.Fatalf("imported artifacts: %+v", imported)
			}
			if workspaceID != "" {
				relocated := filepath.Join(filepath.Dir(workdir), "relocated-project")
				if err := os.Rename(workdir, relocated); err != nil {
					t.Fatal(err)
				}
				workdir = relocated
			}
			t.Setenv("WUU_HOME", home)
			// A desktop-style runtime names the stable registered workspace, rather
			// than deriving artifact ownership from the current filesystem location.
			owner, err := runtime.NewSession(runtime.Options{RootDir: workdir, WorkspaceID: workspaceID, HomeDir: home, ConfigPath: filepath.Join(home, "config.json"), ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Cleanup()
			reader, err := owner.RetainState()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			services, err := reader.Services()
			if err != nil {
				t.Fatal(err)
			}
			for _, artifact := range imported.Artifacts {
				resolved, managed, err := services.Artifacts.Resolve(context.Background(), "source", artifact.URI)
				if err != nil || !managed || resolved != artifact || artifact.MIMEType != "text/plain" || len(artifact.SHA256) != 64 {
					t.Fatalf("reopened identity: %+v managed=%v err=%v", resolved, managed, err)
				}
				if _, _, err := services.Artifacts.Resolve(context.Background(), "other", artifact.URI); err == nil {
					t.Fatal("cross-thread identity resolved")
				}
				if artifact.Name == "empty.txt" {
					if artifact.Size != 0 {
						t.Fatalf("empty snapshot size = %d", artifact.Size)
					}
					continue
				}
				data, mime, err := services.Artifacts.Read(context.Background(), "source", artifact.URI, 1024)
				if err != nil || string(data) != "exact immutable patch\n" || mime != artifact.MIMEType {
					t.Fatalf("reopened bytes=%q MIME=%q err=%v", data, mime, err)
				}
			}
		})
	}
}

func TestFixtureArtifactImportDoesNotFallbackWhenProviderDisabled(t *testing.T) {
	home, workdir, cfg := fixtureHome(t, persistence.JSONLModule)
	*cfg.Composition.Modules = append(*cfg.Composition.Modules, runtime.StateServicesConsumer, config.Module)
	encoded, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(home, "config.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workdir, "review.txt")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"home": home, "workdir": workdir, "op": "import-artifacts", "artifacts": []map[string]any{{"thread_id": "source", "path": path}}})
	if err := run(context.Background(), bytes.NewReader(raw), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), runtime.ArtifactServiceName) {
		t.Fatalf("disabled artifact provider must fail at its selected service: %v", err)
	}
}
