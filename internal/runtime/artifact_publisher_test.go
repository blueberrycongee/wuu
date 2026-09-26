package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestThreadRuntimeBindsArtifactPublisher(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "fixture")
	s, err := NewSession(Options{RootDir: root, HomeDir: home, Config: config.Config{
		DefaultProvider: "test", Providers: map[string]config.ProviderConfig{"test": {
			Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "gpt-test",
		}}, PTC: config.PTCConfig{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.Cleanup() })
	if err := os.WriteFile(filepath.Join(root, "chart.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"thread-a", "thread-b"} {
		thread, err := s.NewThreadRuntime(id)
		if err != nil {
			t.Fatal(err)
		}
		executor, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
		if !ok {
			t.Fatal("thread executor lost rich results")
		}
		result, err := executor.ExecuteResult(context.Background(), providers.ToolCall{ID: "present", Name: "present_artifact", Arguments: `{"path":"chart.svg"}`})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Content) != 1 || !strings.Contains(result.Content[0].URI, "/"+id+"/") {
			t.Fatalf("wrong thread snapshot: %+v", result)
		}
	}
}

func TestPresentArtifactSnapshotsSurviveEditsAndReplay(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	state := filepath.Join(home, "workspaces", "workspace")
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetArtifactPublisher(newArtifactPublisher(home))
	kit.SetStateDir(state)
	kit.SetSessionID("thread")
	kit.SetSessionDir(statepath.SessionArtifactDir(state, "thread"))
	changed := false
	kit.SetOnFileChanged(func(string) { changed = true })
	source := filepath.Join(root, "chart.svg")
	firstSVG := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="80"><rect width="200" height="80" fill="red"/></svg>`
	if err := os.WriteFile(source, []byte(firstSVG), 0o600); err != nil {
		t.Fatal(err)
	}
	publish := func(id string) toolresult.ContentPart {
		t.Helper()
		result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{ID: id, Name: "present_artifact", Arguments: `{"path":"chart.svg"}`})
		if err != nil {
			t.Fatal(err)
		}
		if err := result.Validate(); err != nil {
			t.Fatal(err)
		}
		if len(result.Content) != 1 {
			t.Fatalf("result = %+v", result)
		}
		// The canonical ToolResult is the durable message payload. Rehydration
		// must retain the managed URI and identity, not a mutable workspace path.
		var replay toolresult.Result
		if err := json.Unmarshal([]byte(result.JSONProjection()), &replay); err != nil {
			t.Fatal(err)
		}
		part := replay.Content[0]
		if part.Type != "image" || part.MIMEType != "image/svg+xml" || part.Artifact == nil || part.Artifact.Placement != "inline" || part.Data != "" {
			t.Fatalf("bad image artifact: %+v", part)
		}
		projection := providers.ProjectToolResult(replay)
		if len(projection.ObservationImages) != 0 || !strings.Contains(projection.ToolText, "presented separately") {
			t.Fatalf("presentation must not feed SVG back as model input: %+v", projection)
		}
		return part
	}
	first := publish("call-1")
	repeated := publish("call-2")
	if first.Artifact.SHA256 != repeated.Artifact.SHA256 {
		t.Fatal("unchanged snapshot hash changed")
	}
	if err := os.WriteFile(source, []byte(strings.ReplaceAll(firstSVG, "red", "blue")), 0o600); err != nil {
		t.Fatal(err)
	}
	second := publish("call-3")
	if first.Artifact.SHA256 == second.Artifact.SHA256 {
		t.Fatal("new version reused old hash")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(first.URI)
	if err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(statepath.SessionArtifactDir(state, "thread"), "artifacts", first.Artifact.Ref, first.Name)
	data, err := os.ReadFile(stored)
	if err != nil || string(data) != firstSVG {
		t.Fatalf("old snapshot lost: %s, %v", data, err)
	}
	if parsed.Scheme != "wuu-artifact" || parsed.Query().Get("sha256") != first.Artifact.SHA256 {
		t.Fatalf("bad URI: %s", first.URI)
	}
	if _, managed, err := resolveManagedArtifact(home, state, "thread", first.URI); err != nil || !managed {
		t.Fatalf("cannot reopen snapshot: %v", err)
	}
	if _, _, err := resolveManagedArtifact(home, state, "other-thread", first.URI); err == nil {
		t.Fatal("cross-thread artifact accepted")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(stored), ".artifact.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest managedArtifactManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.CallID != "call-1" || manifest.ThreadID != "thread" {
		t.Fatalf("lost provenance: %+v", manifest)
	}
	if changed {
		t.Fatal("presenting an artifact fired a workspace edit hook")
	}
}

func TestArtifactPublisherTypesAndFailures(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	state := filepath.Join(home, "workspaces", "workspace")
	publish := newArtifactPublisher(home)
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	for _, tc := range []struct {
		name            string
		data            []byte
		mime, placement string
		fail            bool
	}{
		{"chart.png", png, "image/png", "inline", false},
		{"chart.bin", png, "image/png", "inline", false},
		{"chart.svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml", "inline", false},
		{"report.pdf", []byte("%PDF-1.7\nfixture"), "application/pdf", "turn_end", false},
		{"report.html", []byte("<!doctype html><p>Report</p>"), "text/html", "turn_end", false},
		{"spoof.svg", []byte(`<html/>`), "", "", true},
		{"spoof.png", []byte(`<html/>`), "", "", true},
		{"empty.svg", nil, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name)
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			part, err := publish(context.Background(), tools.ArtifactPublishRequest{Path: path, ThreadID: "thread", StateDir: state})
			if tc.fail {
				if err == nil {
					t.Fatal("accepted malformed image")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if part.MIMEType != tc.mime || part.Artifact.Placement != tc.placement {
				t.Fatalf("part = %+v", part)
			}
		})
	}
	large := filepath.Join(root, "large.bin")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImportedArtifactBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "missing.png"), large} {
		if _, err := publish(context.Background(), tools.ArtifactPublishRequest{Path: path, ThreadID: "thread", StateDir: state}); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	if _, err := publish(context.Background(), tools.ArtifactPublishRequest{Path: large, ThreadID: "../escape", StateDir: state}); err == nil {
		t.Fatal("accepted unsafe thread")
	}
}
